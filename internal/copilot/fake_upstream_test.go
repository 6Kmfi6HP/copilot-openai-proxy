package copilot

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type fakeCopilotScenario struct {
	conversationID       string
	messageID            string
	appendTexts          []string
	imageURL             string
	expectSend           bool
	startDelay           time.Duration
	holdChatOpen         bool
	idleCloseAfter       time.Duration
	closeBeforeSendCount int
}

type fakeCopilotUpstream struct {
	t        *testing.T
	scenario fakeCopilotScenario
	server   *httptest.Server

	mu             sync.Mutex
	startRequests  []startRequestBody
	outboundEvents []string
	lastSend       sendMessage
	sendObserved   chan struct{}
	sendOnce       sync.Once
	chatCount      int
	handlerErrors  chan error
}

func newFakeCopilotUpstream(t *testing.T, scenario fakeCopilotScenario) *fakeCopilotUpstream {
	t.Helper()

	if scenario.conversationID == "" {
		scenario.conversationID = "conv-test"
	}
	if scenario.messageID == "" {
		scenario.messageID = "msg-test"
	}

	upstream := &fakeCopilotUpstream{
		t:             t,
		scenario:      scenario,
		sendObserved:  make(chan struct{}),
		handlerErrors: make(chan error, 16),
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/c/api/start":
			upstream.handleStart(w, r)
		case "/c/api/chat":
			upstream.handleChat(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	upstream.server = server
	t.Cleanup(func() {
		server.Close()
		upstream.assertNoHandlerError(t)
	})

	return upstream
}

func (f *fakeCopilotUpstream) newClient(t *testing.T) *Client {
	t.Helper()

	return f.newClientWithConfig(t, ClientConfig{
		MaxSessions:    8,
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

func (f *fakeCopilotUpstream) newClientWithConfig(t *testing.T, cfg ClientConfig) *Client {
	t.Helper()

	return newClientForTestServer(t, f.server, cfg)
}

func (f *fakeCopilotUpstream) lastStartRequest() startRequestBody {
	f.t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.startRequests) == 0 {
		f.t.Fatal("no start requests recorded")
	}

	return f.startRequests[len(f.startRequests)-1]
}

func (f *fakeCopilotUpstream) recordedOutboundEvents() []string {
	f.t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()

	events := make([]string, len(f.outboundEvents))
	copy(events, f.outboundEvents)
	return events
}

func (f *fakeCopilotUpstream) lastSendMessage() sendMessage {
	f.t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()

	return f.lastSend
}

func (f *fakeCopilotUpstream) waitForSendObserved(t *testing.T) {
	t.Helper()

	select {
	case <-f.sendObserved:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for fake upstream send event")
	}
}

func (f *fakeCopilotUpstream) recordHandlerError(format string, args ...any) {
	select {
	case f.handlerErrors <- fmt.Errorf(format, args...):
	default:
	}
}

func (f *fakeCopilotUpstream) assertNoHandlerError(t *testing.T) {
	t.Helper()

	select {
	case err := <-f.handlerErrors:
		t.Fatalf("fake upstream handler error = %v", err)
	default:
	}
}

func (f *fakeCopilotUpstream) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "only POST is supported", http.StatusMethodNotAllowed)
		return
	}

	if f.scenario.startDelay > 0 {
		timer := time.NewTimer(f.scenario.startDelay)
		defer timer.Stop()

		select {
		case <-timer.C:
		case <-r.Context().Done():
			return
		}
	}

	defer r.Body.Close()

	var body startRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	f.startRequests = append(f.startRequests, body)
	f.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     CookieAnon,
		Value:    "anon-cookie",
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
	})

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(startResponse{
		CurrentConversationID: f.scenario.conversationID,
		IsBlocked:             false,
	}); err != nil {
		f.recordHandlerError("encode start response: %w", err)
	}
}

func (f *fakeCopilotUpstream) handleChat(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		f.recordHandlerError("upgrade chat websocket: %w", err)
		return
	}
	defer conn.Close()

	if err := conn.WriteJSON(serverEnvelope{Event: "connected"}); err != nil {
		f.recordHandlerError("write connected: %w", err)
		return
	}

	if !f.readOutboundEvent(conn) {
		return
	}

	f.mu.Lock()
	f.chatCount++
	chatCount := f.chatCount
	idleCloseAfter := f.scenario.idleCloseAfter
	closeBeforeSendCount := f.scenario.closeBeforeSendCount
	f.mu.Unlock()

	if closeBeforeSendCount > 0 && chatCount <= closeBeforeSendCount {
		if idleCloseAfter > 0 {
			time.Sleep(idleCloseAfter)
		}
		if err := conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second),
		); err != nil {
			f.recordHandlerError("write close-before-send control: %w", err)
		}
		return
	}

	if !f.scenario.expectSend {
		return
	}

	if !f.readSendMessage(conn) {
		return
	}
	if f.scenario.holdChatOpen {
		<-r.Context().Done()
		return
	}

	if err := conn.WriteJSON(serverEnvelope{
		Event:          "startMessage",
		ConversationID: f.scenario.conversationID,
		MessageID:      f.scenario.messageID,
	}); err != nil {
		f.recordHandlerError("write startMessage: %w", err)
		return
	}

	if f.scenario.imageURL != "" {
		if err := conn.WriteJSON(serverEnvelope{
			Event:     "generatingImage",
			MessageID: f.scenario.messageID,
			PartID:    "part-image",
		}); err != nil {
			f.recordHandlerError("write generatingImage: %w", err)
			return
		}
		if err := conn.WriteJSON(serverEnvelope{
			Event:     "partialImageGenerated",
			MessageID: f.scenario.messageID,
			PartID:    "part-image",
		}); err != nil {
			f.recordHandlerError("write partialImageGenerated: %w", err)
			return
		}
		if err := conn.WriteJSON(serverEnvelope{
			Event:     "imageGenerated",
			MessageID: f.scenario.messageID,
			PartID:    "part-image",
			URL:       f.scenario.imageURL,
		}); err != nil {
			f.recordHandlerError("write imageGenerated: %w", err)
			return
		}
	}

	for _, text := range f.scenario.appendTexts {
		if err := conn.WriteJSON(serverEnvelope{Event: "appendText", Text: text}); err != nil {
			f.recordHandlerError("write appendText: %w", err)
			return
		}
	}

	if err := conn.WriteJSON(serverEnvelope{Event: "done", MessageID: f.scenario.messageID}); err != nil {
		f.recordHandlerError("write done: %w", err)
		return
	}
	_ = conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(time.Second),
	)
}

func (f *fakeCopilotUpstream) readOutboundEvent(conn *websocket.Conn) bool {
	f.t.Helper()

	var env struct {
		Event string `json:"event"`
	}
	if err := conn.ReadJSON(&env); err != nil {
		f.recordHandlerError("read outbound event: %w", err)
		return false
	}

	f.mu.Lock()
	f.outboundEvents = append(f.outboundEvents, env.Event)
	f.mu.Unlock()
	return true
}

func (f *fakeCopilotUpstream) readSendMessage(conn *websocket.Conn) bool {
	f.t.Helper()

	var msg sendMessage
	if err := conn.ReadJSON(&msg); err != nil {
		if f.scenario.closeBeforeSendCount > 0 {
			return false
		}
		f.recordHandlerError("read send message: %w", err)
		return false
	}

	f.mu.Lock()
	f.outboundEvents = append(f.outboundEvents, msg.Event)
	f.lastSend = msg
	f.mu.Unlock()
	f.sendOnce.Do(func() {
		close(f.sendObserved)
	})
	return true
}
