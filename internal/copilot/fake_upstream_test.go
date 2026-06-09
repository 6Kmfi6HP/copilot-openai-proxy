package copilot

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type fakeCopilotScenario struct {
	conversationID string
	messageID      string
	appendTexts    []string
	expectSend     bool
	startDelay     time.Duration
	holdChatOpen   bool
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
		t:            t,
		scenario:     scenario,
		sendObserved: make(chan struct{}),
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
	t.Cleanup(server.Close)

	return upstream
}

func (f *fakeCopilotUpstream) newClient(t *testing.T) *Client {
	t.Helper()

	client, err := NewClient(ClientConfig{
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
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	baseURL, err := url.Parse(f.server.URL)
	if err != nil {
		t.Fatalf("url.Parse(%q) error = %v", f.server.URL, err)
	}

	client.http.Transport = rewriteRoundTripper{
		target: baseURL,
		base:   f.server.Client().Transport,
	}
	client.wsDialer.Proxy = nil
	client.wsDialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	client.wsDialer.NetDialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, network, f.server.Listener.Addr().String())
	}

	return client
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
		f.t.Fatalf("json.NewEncoder().Encode() error = %v", err)
	}
}

func (f *fakeCopilotUpstream) handleChat(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		f.t.Fatalf("Upgrade() error = %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(serverEnvelope{Event: "connected"}); err != nil {
		f.t.Fatalf("WriteJSON(connected) error = %v", err)
	}

	f.readOutboundEvent(conn)
	if !f.scenario.expectSend {
		return
	}

	f.readSendMessage(conn)
	if f.scenario.holdChatOpen {
		<-r.Context().Done()
		return
	}

	if err := conn.WriteJSON(serverEnvelope{
		Event:          "startMessage",
		ConversationID: f.scenario.conversationID,
		MessageID:      f.scenario.messageID,
	}); err != nil {
		f.t.Fatalf("WriteJSON(startMessage) error = %v", err)
	}

	for _, text := range f.scenario.appendTexts {
		if err := conn.WriteJSON(serverEnvelope{Event: "appendText", Text: text}); err != nil {
			f.t.Fatalf("WriteJSON(appendText) error = %v", err)
		}
	}

	if err := conn.WriteJSON(serverEnvelope{Event: "done", MessageID: f.scenario.messageID}); err != nil {
		f.t.Fatalf("WriteJSON(done) error = %v", err)
	}
	if err := conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(time.Second),
	); err != nil {
		f.t.Fatalf("WriteControl(close) error = %v", err)
	}
}

func (f *fakeCopilotUpstream) readOutboundEvent(conn *websocket.Conn) {
	f.t.Helper()

	var env struct {
		Event string `json:"event"`
	}
	if err := conn.ReadJSON(&env); err != nil {
		f.t.Fatalf("ReadJSON(outbound event) error = %v", err)
	}

	f.mu.Lock()
	f.outboundEvents = append(f.outboundEvents, env.Event)
	f.mu.Unlock()
}

func (f *fakeCopilotUpstream) readSendMessage(conn *websocket.Conn) {
	f.t.Helper()

	var msg sendMessage
	if err := conn.ReadJSON(&msg); err != nil {
		f.t.Fatalf("ReadJSON(send message) error = %v", err)
	}

	f.mu.Lock()
	f.outboundEvents = append(f.outboundEvents, msg.Event)
	f.lastSend = msg
	f.mu.Unlock()
	f.sendOnce.Do(func() {
		close(f.sendObserved)
	})
}

type rewriteRoundTripper struct {
	target *url.URL
	base   http.RoundTripper
}

func (r rewriteRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	rewritten := *clone.URL
	rewritten.Scheme = r.target.Scheme
	rewritten.Host = r.target.Host
	clone.URL = &rewritten
	return r.base.RoundTrip(clone)
}
