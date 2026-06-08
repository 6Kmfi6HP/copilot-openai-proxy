package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// CopilotStartURL fetches an anonymous session cookie (POST, not GET).
	copilotStartURL = "https://copilot.microsoft.com/c/api/start"

	// CopilotWSURL is the WebSocket endpoint.
	copilotWSURL = "wss://copilot.microsoft.com/c/api/chat"

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
	http        *http.Client
	wsDialer    *websocket.Dialer
	mu          sync.Mutex
	sessions    map[string]*SessionState // conversationID → session
	byAge       []string                 // LRU order for eviction
	maxSessions int
	sessionTTL  time.Duration
	cleanupInt  time.Duration
	connTimeout time.Duration
	timeout     time.Duration
	debug       bool
	timeZone    string // timezone sent in start request body
}

// NewClient creates a Copilot client that obtains anonymous cookies
// from the Copilot service and pools WebSocket sessions.
func NewClient(maxSessions int, sessionTTL, cleanupInt, connTimeout, timeout time.Duration, debug bool, timeZone string) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("create cookie jar: %w", err)
	}

	if timeZone == "" {
		timeZone = "Asia/Shanghai"
	}

	c := &Client{
		http: &http.Client{
			Jar:     jar,
			Timeout: 15 * time.Second,
		},
		wsDialer: &websocket.Dialer{
			Proxy:            http.ProxyFromEnvironment,
			HandshakeTimeout: connTimeout,
		},
		sessions:    make(map[string]*SessionState),
		maxSessions: maxSessions,
		sessionTTL:  sessionTTL,
		cleanupInt:  cleanupInt,
		connTimeout: connTimeout,
		timeout:     timeout,
		debug:       debug,
		timeZone:    timeZone,
	}
	return c, nil
}

// Complete sends a prompt and returns the full completion text.
func (c *Client) Complete(ctx context.Context, input CompletionInput) (string, string, error) {
	session, err := c.getOrCreateSession(ctx)
	if err != nil {
		return "", "", fmt.Errorf("get session: %w", err)
	}

	events := make(chan StreamEvent, 128)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(events)
		c.readLoop(session, events)
	}()

	// Send the prompt via "send" event with the conversation ID.
	log.Printf("copilot send event=send session_id=%s conversation_id=%s prompt_len=%d",
		session.ClientSessionID, session.ConversationID, len(input.Prompt))
	if err := sendEvent(session.Conn, newSendMessage(input.Prompt, session.ConversationID, input.Mode)); err != nil {
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
	go func() {
		defer close(events)
		c.readLoop(session, events)
	}()

	if err := sendEvent(session.Conn, newSendMessage(input.Prompt, session.ConversationID, input.Mode)); err != nil {
		return nil, fmt.Errorf("copilot websocket send: %w", err)
	}

	return events, nil
}

func (c *Client) waitForConnected(conn *websocket.Conn) error {
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

	for {
		_, msg, err := session.Conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				events <- StreamEvent{Type: EventDone}
				return
			}
			events <- StreamEvent{Type: EventError, Err: fmt.Errorf("copilot websocket read failed: %w", err)}
			return
		}

		evt, err := parseServerEvent(msg)
		if err != nil {
			continue
		}

		if c.debug {
			log.Printf("copilot event raw bytes=%d data=%q", len(msg), string(msg))
		}

		// Handle challenge events inline (they need a response on the WebSocket).
		if evt.Type == EventChallenge {
			answer := solveHashcash(evt.ChallengeParam)
			log.Printf("copilot challenge solved: param=%s answer=%s", evt.ChallengeParam, answer)
			if err := sendEvent(session.Conn, map[string]string{
				"type":   "answer",
				"answer": answer,
			}); err != nil {
				log.Printf("copilot failed to send challenge answer: %v", err)
			}
			continue // Don't forward challenge to caller
		}
		if evt.Type == EventIgnore {
			continue
		}

		events <- evt
	}
}
