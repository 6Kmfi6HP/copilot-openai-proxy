package copilot

import (
	"context"
	"encoding/json"
	"errors"
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
	janitorCancel  context.CancelFunc
	janitorDone    chan struct{}
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
	c.startJanitor()
	return c, nil
}

// Complete sends a prompt and returns the full completion text.
func (c *Client) Complete(ctx context.Context, input CompletionInput) (string, string, error) {
	session, events, err := c.startPromptEvents(ctx, input)
	if err != nil {
		return "", "", err
	}
	defer c.releaseSession(session)

	// Collect until done.
	var b strings.Builder
	var messageID string
	for {
		select {
		case <-ctx.Done():
			c.InvalidateSession(session.ConversationID)
			return b.String(), messageID, ctx.Err()
		case evt, ok := <-events:
			if !ok {
				return b.String(), messageID, nil
			}

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
	}
}

func (c *Client) startPromptEvents(ctx context.Context, input CompletionInput) (*SessionState, <-chan StreamEvent, error) {
	for attempt := 0; attempt < 2; attempt++ {
		var (
			session *SessionState
			err     error
		)
		if attempt == 0 {
			session, err = c.getOrCreateSession(ctx)
		} else {
			session, err = c.getFreshSession(ctx)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("get session: %w", err)
		}

		events := make(chan StreamEvent, 128)
		pump := c.newConnPump(ctx, session.Conn)
		go func() {
			defer close(events)
			pump.run(events)
		}()

		log.Printf("copilot send event=send session_id=%s conversation_id=%s prompt_len=%d",
			session.ClientSessionID, session.ConversationID, len(input.Prompt))
		if err := pump.send(ctx, newSendMessage(input.Prompt, session.ConversationID, input.Mode)); err == nil {
			return session, events, nil
		} else {
			c.InvalidateSession(session.ConversationID)
			if !c.shouldRetryPromptSend(ctx, err, attempt) {
				return nil, nil, fmt.Errorf("copilot websocket send: %w", err)
			}
			if c.sessionMgr != nil {
				c.sessionMgr.dropIdleSessions()
			}
		}
	}

	return nil, nil, fmt.Errorf("copilot websocket send: exhausted retries")
}

func (c *Client) shouldRetryPromptSend(ctx context.Context, err error, attempt int) bool {
	if attempt > 0 || err == nil {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

func (c *Client) waitForConnected(ctx context.Context, conn *websocket.Conn) error {
	if ctx == nil {
		ctx = context.Background()
	}

	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()
	defer close(stop)

	if deadline := c.connectedReadDeadline(ctx); !deadline.IsZero() {
		_ = conn.SetReadDeadline(deadline)
		defer conn.SetReadDeadline(time.Time{})
	}
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
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

func (c *Client) connectedReadDeadline(ctx context.Context) time.Time {
	deadline := time.Time{}
	if c.timeout > 0 {
		deadline = time.Now().Add(c.timeout)
	}
	if ctx != nil {
		if ctxDeadline, ok := ctx.Deadline(); ok && (deadline.IsZero() || ctxDeadline.Before(deadline)) {
			deadline = ctxDeadline
		}
	}
	return deadline
}

// ---------- Event loop ----------

func (c *Client) readLoop(session *SessionState, events chan<- StreamEvent) {
	defer func() {
		session.setConnected(false)
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
