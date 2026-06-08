package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// CopilotOrigin is the origin header for WebSocket connections.
	copilotOrigin = "https://copilot.microsoft.com"

	copilotUserAgent = "CopilotNative/30.0.440527002-prod (Android 9; Xiaomi; Redmi Note 7)"

	// CookieAnon is the anonymous session cookie name.
	CookieAnon = "__Host-copilot-anon"

	// AcceptLanguage sent with requests to Copilot.
	acceptLanguage = "zh-CN,zh;q=0.9,en;q=0.8"
)

// CompletionInput is the input for a Copilot completion request.
type CompletionInput struct {
	Prompt         string
	ConversationID string // multi-turn conversation ID (empty = new)
	Mode           string
	Stream         bool
	StreamModel    string // model name echoed back in SSE chunks
}

// Client manages Copilot WebSocket sessions.
type Client struct {
	http           *http.Client
	wsDialer       *websocket.Dialer
	sessionMgr     *sessionManager
	maxSessions    int
	warmSessions   int
	sessionTTL     time.Duration
	cleanupInt     time.Duration
	connTimeout    time.Duration
	timeout        time.Duration
	wsReadTimeout  time.Duration
	wsWriteTimeout time.Duration
	wsPingInterval time.Duration
	debug          bool
	timeZone       string // timezone sent in start request body
	startURL       string
	wsURL          string
}

// NewClient creates a Copilot client that obtains anonymous cookies
// from the Copilot service and pools WebSocket sessions.
func NewClient(cfg ClientConfig) (*Client, error) {
	cfg = cfg.normalized()

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("create cookie jar: %w", err)
	}
	proxyFunc, err := newProxyFunc(cfg.ProxyURL)
	if err != nil {
		return nil, fmt.Errorf("configure outbound proxy: %w", err)
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("default transport has unexpected type %T", http.DefaultTransport)
	}

	c := &Client{
		http: &http.Client{
			Jar:       jar,
			Timeout:   cfg.Timeout,
			Transport: transport.Clone(),
		},
		wsDialer: &websocket.Dialer{
			Proxy:            proxyFunc,
			HandshakeTimeout: cfg.ConnTimeout,
		},
		maxSessions:    cfg.MaxSessions,
		warmSessions:   cfg.WarmSessions,
		sessionTTL:     cfg.SessionTTL,
		cleanupInt:     cfg.CleanupInt,
		connTimeout:    cfg.ConnTimeout,
		timeout:        cfg.Timeout,
		wsReadTimeout:  cfg.WSReadTimeout,
		wsWriteTimeout: cfg.WSWriteTimeout,
		wsPingInterval: cfg.WSPingInterval,
		debug:          cfg.Debug,
		timeZone:       cfg.TimeZone,
		startURL:       cfg.StartURL,
		wsURL:          cfg.WSURL,
	}
	httpTransport, ok := c.http.Transport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("copilot transport has unexpected type %T", c.http.Transport)
	}
	httpTransport.Proxy = proxyFunc
	c.sessionMgr = newSessionManagerWithWarmPool(cfg.MaxSessions, cfg.WarmSessions, c.startAnon)
	return c, nil
}

// Complete sends a prompt and returns the full completion text.
func (c *Client) Complete(ctx context.Context, input CompletionInput) (string, string, error) {
	session, err := c.getOrCreateSession(ctx)
	if err != nil {
		return "", "", fmt.Errorf("get session: %w", err)
	}
	defer c.releaseSession(session)

	events := make(chan StreamEvent, 128)
	done := make(chan struct{})
	pump := c.newConnPump(ctx, session.Conn)
	go func() {
		defer close(done)
		defer close(events)
		pump.run(events)
	}()

	// Send the prompt via "send" event with the conversation ID.
	log.Printf("copilot send event=send session_id=%s conversation_id=%s prompt_len=%d",
		session.ClientSessionID, session.ConversationID, len(input.Prompt))
	if err := pump.send(ctx, newSendMessage(input.Prompt, session.ConversationID, input.Mode)); err != nil {
		c.InvalidateSession(session.ConversationID)
		return "", "", fmt.Errorf("copilot websocket send: %w", err)
	}

	// Collect until done.
	var b strings.Builder
	var messageID string
	for evt := range events {
		switch evt.Type {
		case EventAppendText:
			b.WriteString(evt.Text)
		case EventStartMessage:
			messageID = evt.MessageID
		case EventError:
			return b.String(), messageID, evt.Err
		case EventDone:
			return b.String(), messageID, nil
		}
	}
	return b.String(), messageID, nil
}

// StreamEvents returns a channel of StreamEvent for SSE streaming.
func (c *Client) StreamEvents(ctx context.Context, input CompletionInput) (<-chan StreamEvent, error) {
	session, err := c.getOrCreateSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	events := make(chan StreamEvent, 128)
	pump := c.newConnPump(ctx, session.Conn)
	go func() {
		defer c.releaseSession(session)
		defer close(events)
		pump.run(events)
	}()

	if err := pump.send(ctx, newSendMessage(input.Prompt, session.ConversationID, input.Mode)); err != nil {
		c.InvalidateSession(session.ConversationID)
		return nil, fmt.Errorf("copilot websocket send: %w", err)
	}

	return events, nil
}

func (c *Client) waitForConnected(conn *websocket.Conn) error {
	if c.timeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(c.timeout))
		defer conn.SetReadDeadline(time.Time{})
	}
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("copilot websocket read failed: %w", err)
		}
		var env serverEnvelope
		if err := json.Unmarshal(msg, &env); err != nil {
			continue
		}
		if c.debug {
			log.Printf("copilot event raw bytes=%d data=%q", len(msg), string(msg))
		}
		switch env.Event {
		case "connected", "ready":
			return nil
		case "error":
			body := firstNonEmpty(env.Body, env.Text)
			if strings.Contains(body, "blocked") {
				return fmt.Errorf("copilot start reports session user is blocked; websocket may not produce completions")
			}
			return upstreamErrorf("copilot error during connect: %s", body)
		}
	}
}

// ---------- Event loop ----------

func (c *Client) readLoop(session *SessionState, events chan<- StreamEvent) {
	defer func() {
		session.Connected = false
	}()
	c.newConnPump(context.Background(), session.Conn).run(events)
}

func (c *Client) newConnPump(ctx context.Context, conn *websocket.Conn) *connPump {
	pump := newConnPump(ctx, conn, c.debug)
	pump.readTimeout = c.wsReadTimeout
	pump.writeTimeout = c.wsWriteTimeout
	pump.pingInterval = c.wsPingInterval
	return pump
}
