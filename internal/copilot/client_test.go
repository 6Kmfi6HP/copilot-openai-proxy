package copilot

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func Test_readLoop_answersChallengeWithEventField(t *testing.T) {
	responseCh := make(chan []byte, 1)
	serverErrCh := make(chan error, 1)
	upgrader := websocket.Upgrader{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErrCh <- err
			return
		}
		defer conn.Close()

		if err := conn.WriteJSON(serverEnvelope{Event: "challenge", Parameter: "seed:1"}); err != nil {
			serverErrCh <- err
			return
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			serverErrCh <- err
			return
		}
		responseCh <- msg

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
		t.Fatalf("websocket.DefaultDialer.Dial returned error: %v", err)
	}
	defer clientConn.Close()

	client := &Client{}
	session := &SessionState{Conn: clientConn}
	events := make(chan StreamEvent, 2)
	done := make(chan struct{})
	go func() {
		client.readLoop(session, events)
		close(done)
	}()

	var raw []byte
	select {
	case raw = <-responseCh:
	case err := <-serverErrCh:
		t.Fatalf("websocket test server failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for challenge answer")
	}

	var got struct {
		Event  string `json:"event"`
		Type   string `json:"type"`
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if got.Event != "answer" {
		t.Fatalf("event = %q, want answer", got.Event)
	}
	if got.Type != "" {
		t.Fatalf("type = %q, want empty", got.Type)
	}
	if got.Answer != solveHashcash("seed:1") {
		t.Fatalf("answer = %q, want %q", got.Answer, solveHashcash("seed:1"))
	}

	select {
	case evt := <-events:
		if evt.Type != EventDone {
			t.Fatalf("event type = %v, want EventDone", evt.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for readLoop completion")
	}

	select {
	case err := <-serverErrCh:
		t.Fatalf("websocket test server failed: %v", err)
	default:
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for readLoop goroutine to exit")
	}
}
