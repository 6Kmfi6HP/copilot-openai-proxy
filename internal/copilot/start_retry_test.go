package copilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type startRetryScenario struct {
	htmlStartFailures int32
	chatFailures      int32
}

type startRetryUpstream struct {
	t             *testing.T
	scenario      startRetryScenario
	server        *httptest.Server
	startAttempts atomic.Int32
	chatAttempts  atomic.Int32
}

func newStartRetryUpstream(t *testing.T, scenario startRetryScenario) *startRetryUpstream {
	t.Helper()

	upstream := &startRetryUpstream{
		t:        t,
		scenario: scenario,
	}
	upstream.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/c/api/start":
			upstream.handleStart(w, r)
		case "/c/api/user/sessions/temporary":
			upstream.handleTemporarySession(w, r)
		case "/c/api/chat":
			upstream.handleChat(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.server.Close)

	return upstream
}

func (u *startRetryUpstream) newClient(t *testing.T) *Client {
	t.Helper()

	return newClientForTestServer(t, u.server, ClientConfig{
		MaxSessions:    1,
		WarmSessions:   0,
		SessionTTL:     time.Minute,
		CleanupInt:     time.Minute,
		ConnTimeout:    time.Second,
		Timeout:        time.Second,
		WSReadTimeout:  time.Minute,
		WSWriteTimeout: time.Second,
		WSPingInterval: 25 * time.Second,
		TimeZone:       "UTC",
	})
}

func (u *startRetryUpstream) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "only POST is supported", http.StatusMethodNotAllowed)
		return
	}

	attempt := u.startAttempts.Add(1)
	if attempt <= u.scenario.htmlStartFailures {
		w.Header().Set("Content-Type", "text/html")
		if _, err := w.Write([]byte("<html>temporary challenge</html>")); err != nil {
			u.t.Fatalf("Write(html start) error = %v", err)
		}
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     CookieAnon,
		Value:    "anon-cookie",
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
	})
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(startResponse{
		CurrentConversationID: "conv-retry",
		IsBlocked:             false,
	}); err != nil {
		u.t.Fatalf("Encode(start response) error = %v", err)
	}
}

func (u *startRetryUpstream) handleTemporarySession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(temporarySessionResponse{SessionKey: "temp-session-retry"}); err != nil {
		u.t.Fatalf("Encode(temporary session response) error = %v", err)
	}
}

func (u *startRetryUpstream) handleChat(w http.ResponseWriter, r *http.Request) {
	attempt := u.chatAttempts.Add(1)
	if attempt <= u.scenario.chatFailures {
		http.Error(w, "<html>bad handshake</html>", http.StatusForbidden)
		return
	}

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		u.t.Fatalf("Upgrade() error = %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(serverEnvelope{Event: "connected"}); err != nil {
		u.t.Fatalf("WriteJSON(connected) error = %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		u.t.Fatalf("SetReadDeadline() error = %v", err)
	}
	var env struct {
		Event string `json:"event"`
	}
	if err := conn.ReadJSON(&env); err != nil {
		u.t.Fatalf("ReadJSON(setOptions) error = %v", err)
	}
	if env.Event != "setOptions" {
		u.t.Fatalf("first outbound event = %q, want setOptions", env.Event)
	}
	<-r.Context().Done()
}

func TestStartAnon_RetriesStartResponseHTML_whenFirstStartReturnsHTML(t *testing.T) {
	t.Parallel()

	// Given
	upstream := newStartRetryUpstream(t, startRetryScenario{htmlStartFailures: 1})
	client := upstream.newClient(t)
	defer closeTestClient(t, client)

	// When
	session, err := client.getOrCreateSession(context.Background())

	// Then
	if err != nil {
		t.Fatalf("getOrCreateSession() error = %v", err)
	}
	if session.ConversationID != "conv-retry" {
		t.Fatalf("conversationID = %q, want conv-retry", session.ConversationID)
	}
	client.InvalidateSession(session.ConversationID)
	if got := upstream.startAttempts.Load(); got != 2 {
		t.Fatalf("start attempts = %d, want 2", got)
	}
	if got := upstream.chatAttempts.Load(); got != 1 {
		t.Fatalf("chat attempts = %d, want 1", got)
	}
}

func TestStartAnon_RetriesWebSocketHandshake_whenFirstDialFails(t *testing.T) {
	t.Parallel()

	// Given
	upstream := newStartRetryUpstream(t, startRetryScenario{chatFailures: 1})
	client := upstream.newClient(t)
	defer closeTestClient(t, client)

	// When
	session, err := client.getOrCreateSession(context.Background())

	// Then
	if err != nil {
		t.Fatalf("getOrCreateSession() error = %v", err)
	}
	if session.ConversationID != "conv-retry" {
		t.Fatalf("conversationID = %q, want conv-retry", session.ConversationID)
	}
	client.InvalidateSession(session.ConversationID)
	if got := upstream.startAttempts.Load(); got != 2 {
		t.Fatalf("start attempts = %d, want 2", got)
	}
	if got := upstream.chatAttempts.Load(); got != 2 {
		t.Fatalf("chat attempts = %d, want 2", got)
	}
}

func closeTestClient(t *testing.T, client *Client) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Close(ctx); err != nil {
		t.Fatalf("Client.Close() error = %v", err)
	}
}
