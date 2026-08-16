package copilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestConnPump_SerializesPromptAndChallengeWrites(t *testing.T) {
	t.Parallel()

	outbound := make(chan []byte, 2)
	serverErrCh := make(chan error, 1)
	upgrader := websocket.Upgrader{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErrCh <- err
			return
		}
		defer conn.Close()

		_, prompt, err := conn.ReadMessage()
		if err != nil {
			serverErrCh <- err
			return
		}
		outbound <- prompt

		if err := conn.WriteJSON(serverEnvelope{Event: "challenge", Parameter: "seed:1"}); err != nil {
			serverErrCh <- err
			return
		}

		_, answer, err := conn.ReadMessage()
		if err != nil {
			serverErrCh <- err
			return
		}
		outbound <- answer

		if err := conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second),
		); err != nil {
			serverErrCh <- err
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket.DefaultDialer.Dial() error = %v", err)
	}
	defer clientConn.Close()

	pump := newConnPump(context.Background(), clientConn, false)
	events := make(chan StreamEvent, 4)
	done := make(chan struct{})
	go func() {
		defer close(events)
		pump.run(events)
		close(done)
	}()

	if err := pump.send(context.Background(), newSendMessage("hello", "conv-pump", "smart", nil)); err != nil {
		t.Fatalf("pump.send() error = %v", err)
	}

	rawPrompt := waitForRawMessage(t, outbound, serverErrCh)
	rawAnswer := waitForRawMessage(t, outbound, serverErrCh)

	var prompt sendMessage
	if err := json.Unmarshal(rawPrompt, &prompt); err != nil {
		t.Fatalf("json.Unmarshal(prompt) error = %v", err)
	}
	if prompt.Event != "send" {
		t.Fatalf("prompt event = %q, want %q", prompt.Event, "send")
	}
	if prompt.ConversationID != "conv-pump" {
		t.Fatalf("prompt conversationID = %q, want %q", prompt.ConversationID, "conv-pump")
	}

	var answer challengeResponseMessage
	if err := json.Unmarshal(rawAnswer, &answer); err != nil {
		t.Fatalf("json.Unmarshal(answer) error = %v", err)
	}
	if answer.Event != "challengeResponse" {
		t.Fatalf("answer event = %q, want %q", answer.Event, "challengeResponse")
	}
	if answer.Method != "hashcash" {
		t.Fatalf("answer method = %q, want %q", answer.Method, "hashcash")
	}
	if answer.Token != solveHashcash("seed:1") {
		t.Fatalf("answer token = %q, want %q", answer.Token, solveHashcash("seed:1"))
	}

	select {
	case evt := <-events:
		if evt.Type != EventDone {
			t.Fatalf("event type = %v, want %v", evt.Type, EventDone)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for terminal event")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for conn pump to stop")
	}
}

func TestConnPump_ForwardsStreamEvents(t *testing.T) {
	t.Parallel()

	serverErrCh := make(chan error, 1)
	upgrader := websocket.Upgrader{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErrCh <- err
			return
		}
		defer conn.Close()

		for _, msg := range []serverEnvelope{
			{Event: "startMessage", ConversationID: "conv-forward", MessageID: "msg-forward"},
			{Event: "appendText", Text: "hello"},
			{Event: "done", MessageID: "msg-forward"},
		} {
			if err := conn.WriteJSON(msg); err != nil {
				serverErrCh <- err
				return
			}
		}

		if err := conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second),
		); err != nil {
			serverErrCh <- err
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket.DefaultDialer.Dial() error = %v", err)
	}
	defer clientConn.Close()

	pump := newConnPump(context.Background(), clientConn, false)
	events := make(chan StreamEvent, 8)
	done := make(chan struct{})
	go func() {
		defer close(events)
		pump.run(events)
		close(done)
	}()

	got := collectStreamEvents(t, events)
	if len(got) < 4 {
		t.Fatalf("event count = %d, want at least 4", len(got))
	}
	if got[0].Type != EventStartMessage || got[0].ConversationID != "conv-forward" || got[0].MessageID != "msg-forward" {
		t.Fatalf("start event = %+v, want conv-forward/msg-forward startMessage", got[0])
	}
	if got[1].Type != EventAppendText || got[1].Text != "hello" {
		t.Fatalf("append event = %+v, want hello appendText", got[1])
	}
	if got[2].Type != EventDone || got[3].Type != EventDone {
		t.Fatalf("done events = %v/%v, want done/done", got[2].Type, got[3].Type)
	}

	select {
	case err := <-serverErrCh:
		t.Fatalf("websocket test server failed: %v", err)
	default:
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for conn pump to stop")
	}
}

func TestConnPump_StopsOnPeerClose(t *testing.T) {
	t.Parallel()

	serverErrCh := make(chan error, 1)
	upgrader := websocket.Upgrader{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErrCh <- err
			return
		}
		defer conn.Close()

		if err := conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second),
		); err != nil {
			serverErrCh <- err
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket.DefaultDialer.Dial() error = %v", err)
	}
	defer clientConn.Close()

	pump := newConnPump(context.Background(), clientConn, false)
	events := make(chan StreamEvent, 2)
	done := make(chan struct{})
	go func() {
		defer close(events)
		pump.run(events)
		close(done)
	}()

	select {
	case evt := <-events:
		if evt.Type != EventDone {
			t.Fatalf("event type = %v, want %v", evt.Type, EventDone)
		}
	case err := <-serverErrCh:
		t.Fatalf("websocket test server failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for peer-close event")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for conn pump goroutine to exit")
	}
}

func TestConnPump_RefreshesReadDeadlineOnPong(t *testing.T) {
	t.Parallel()

	serverErrCh := make(chan error, 1)
	upgrader := websocket.Upgrader{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErrCh <- err
			return
		}
		defer conn.Close()

		time.Sleep(60 * time.Millisecond)
		if err := conn.WriteControl(websocket.PongMessage, nil, time.Now().Add(time.Second)); err != nil {
			serverErrCh <- err
			return
		}

		time.Sleep(80 * time.Millisecond)
		if err := conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second),
		); err != nil {
			serverErrCh <- err
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket.DefaultDialer.Dial() error = %v", err)
	}
	defer clientConn.Close()

	pump := newConnPump(context.Background(), clientConn, false)
	pump.readTimeout = 120 * time.Millisecond

	events := make(chan StreamEvent, 2)
	done := make(chan struct{})
	go func() {
		defer close(events)
		pump.run(events)
		close(done)
	}()

	select {
	case evt := <-events:
		if evt.Type != EventDone {
			t.Fatalf("event type = %v, want %v after pong-refreshed deadline", evt.Type, EventDone)
		}
	case err := <-serverErrCh:
		t.Fatalf("websocket test server failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for pong-refreshed close")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for conn pump goroutine to exit")
	}
}

func waitForRawMessage(t *testing.T, outbound <-chan []byte, serverErrCh <-chan error) []byte {
	t.Helper()

	select {
	case raw := <-outbound:
		return raw
	case err := <-serverErrCh:
		t.Fatalf("websocket test server failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for outbound websocket message")
	}
	return nil
}
