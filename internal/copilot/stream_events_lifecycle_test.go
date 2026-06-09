package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestStreamEvents_ClosesWebSocket_whenUpstreamDoneKeepsSocketOpen(t *testing.T) {
	t.Parallel()

	wsClosed := make(chan struct{})
	serverErrCh := make(chan error, 1)
	server := newDoneHoldingUpstream(t, wsClosed, serverErrCh)
	client := newLifecycleClient(t, server)

	events, err := client.StreamEvents(context.Background(), CompletionInput{
		Prompt: "finish and close",
		Mode:   "smart",
	})
	if err != nil {
		t.Fatalf("StreamEvents() error = %v", err)
	}

	waitForEventDone(t, events)

	select {
	case <-wsClosed:
	case err := <-serverErrCh:
		t.Fatalf("fake upstream error = %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for client to close websocket after done")
	}

	waitForSessionSnapshot(t, client.sessionMgr, sessionSnapshot{})
}

func newDoneHoldingUpstream(t *testing.T, wsClosed chan<- struct{}, serverErrCh chan<- error) *httptest.Server {
	t.Helper()

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/c/api/start":
			handleLifecycleStart(t, w, r)
		case "/c/api/chat":
			handleDoneHoldingChat(r, w, upgrader, wsClosed, serverErrCh)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func handleLifecycleStart(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()

	defer r.Body.Close()
	http.SetCookie(w, &http.Cookie{
		Name:     CookieAnon,
		Value:    "anon-cookie",
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
	})
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(startResponse{
		CurrentConversationID: "conv-done-hold",
		IsBlocked:             false,
	}); err != nil {
		t.Fatalf("json.NewEncoder().Encode() error = %v", err)
	}
}

func handleDoneHoldingChat(
	r *http.Request,
	w http.ResponseWriter,
	upgrader websocket.Upgrader,
	wsClosed chan<- struct{},
	serverErrCh chan<- error,
) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		serverErrCh <- fmt.Errorf("upgrade: %w", err)
		return
	}
	defer conn.Close()

	if err := conn.WriteJSON(serverEnvelope{Event: "connected"}); err != nil {
		serverErrCh <- fmt.Errorf("write connected: %w", err)
		return
	}
	if err := readLifecycleJSON(conn); err != nil {
		if isLifecyclePreTerminalClose(err) {
			return
		}
		serverErrCh <- fmt.Errorf("read setOptions: %w", err)
		return
	}
	if err := readLifecycleJSON(conn); err != nil {
		if isLifecyclePreTerminalClose(err) {
			return
		}
		serverErrCh <- fmt.Errorf("read send: %w", err)
		return
	}
	if err := conn.WriteJSON(serverEnvelope{
		Event:          "startMessage",
		ConversationID: "conv-done-hold",
		MessageID:      "msg-done-hold",
	}); err != nil {
		serverErrCh <- fmt.Errorf("write startMessage: %w", err)
		return
	}
	if err := conn.WriteJSON(serverEnvelope{Event: "done", MessageID: "msg-done-hold"}); err != nil {
		serverErrCh <- fmt.Errorf("write done: %w", err)
		return
	}

	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if _, _, err := conn.ReadMessage(); err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			serverErrCh <- fmt.Errorf("client did not close websocket after terminal done")
			return
		}
		close(wsClosed)
		return
	}
	serverErrCh <- fmt.Errorf("client left websocket readable after terminal done")
}

func readLifecycleJSON(conn *websocket.Conn) error {
	var body map[string]any
	return conn.ReadJSON(&body)
}

func isLifecyclePreTerminalClose(err error) bool {
	return websocket.IsCloseError(
		err,
		websocket.CloseNormalClosure,
		websocket.CloseGoingAway,
		websocket.CloseAbnormalClosure,
	)
}

func newLifecycleClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()

	return newClientForTestServer(t, server, ClientConfig{
		MaxSessions:    1,
		WarmSessions:   0,
		SessionTTL:     time.Minute,
		CleanupInt:     time.Minute,
		ConnTimeout:    time.Second,
		Timeout:        time.Second,
		WSReadTimeout:  time.Minute,
		WSWriteTimeout: time.Second,
		WSPingInterval: 25 * time.Second,
		Debug:          false,
		TimeZone:       "UTC",
	})
}

func waitForEventDone(t *testing.T, events <-chan StreamEvent) {
	t.Helper()

	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()

	for {
		select {
		case evt, ok := <-events:
			if !ok {
				t.Fatal("stream closed before done event")
			}
			if evt.Type == EventDone {
				return
			}
		case <-timeout.C:
			t.Fatal("timed out waiting for done event")
		}
	}
}
