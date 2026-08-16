package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientIntegration_Complete_attachesTemporarySessionKeyToHandshake(t *testing.T) {
	t.Parallel()

	upstream := newFakeCopilotUpstream(t, fakeCopilotScenario{
		conversationID: "conv-key",
		messageID:      "msg-key",
		appendTexts:    []string{"ok"},
		expectSend:     true,
	})
	client := upstream.newClient(t)

	if _, _, err := client.Complete(context.Background(), CompletionInput{Prompt: "hi", Mode: "smart"}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	header, query, count := upstream.chatSessionKey()
	if count < 1 {
		t.Fatalf("temporary session endpoint called %d times, want >= 1", count)
	}
	if header != "temp-session-test" {
		t.Fatalf("chat %s header = %q, want %q", temporarySessionKeyHeader, header, "temp-session-test")
	}
	if query != "temp-session-test" {
		t.Fatalf("chat temporarySessionKey query = %q, want %q", query, "temp-session-test")
	}
}

func Test_sessionsURLFromStart_derivesTemporarySessionEndpoint(t *testing.T) {
	tests := []struct {
		name  string
		start string
		want  string
	}{
		{
			name:  "default start url",
			start: defaultCopilotStartURL,
			want:  "https://copilot.microsoft.com/c/api/user/sessions/temporary",
		},
		{
			name:  "custom host",
			start: "https://example.test/c/api/start",
			want:  "https://example.test/c/api/user/sessions/temporary",
		},
		{
			name:  "non-standard start path falls back to default",
			start: "https://example.test/weird",
			want:  defaultCopilotSessionsURL,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sessionsURLFromStart(tt.start); got != tt.want {
				t.Fatalf("sessionsURLFromStart(%q) = %q, want %q", tt.start, got, tt.want)
			}
		})
	}
}

func Test_normalized_derivesSessionsURL_whenUnset(t *testing.T) {
	cfg := ClientConfig{StartURL: "https://example.test/c/api/start"}.normalized()
	if cfg.SessionsURL != "https://example.test/c/api/user/sessions/temporary" {
		t.Fatalf("normalized SessionsURL = %q, want derived temporary endpoint", cfg.SessionsURL)
	}

	explicit := ClientConfig{
		StartURL:    "https://example.test/c/api/start",
		SessionsURL: "https://override.test/sessions",
	}.normalized()
	if explicit.SessionsURL != "https://override.test/sessions" {
		t.Fatalf("normalized SessionsURL = %q, want explicit override preserved", explicit.SessionsURL)
	}
}

func Test_wrapDialError_classifiesHandshakeStatuses(t *testing.T) {
	cause := errors.New("websocket: bad handshake")

	blocked460 := wrapDialError(460, "some-key", cause)
	if !IsBlocked(blocked460) {
		t.Fatalf("460 error not classified as blocked: %v", blocked460)
	}
	if errors.Is(blocked460, errRetryableSessionStart) {
		t.Fatalf("460 error should not be retryable: %v", blocked460)
	}
	if !strings.Contains(blocked460.Error(), "460") || !strings.Contains(blocked460.Error(), "PROXY_URL") {
		t.Fatalf("460 error missing actionable detail: %v", blocked460)
	}

	blocked401 := wrapDialError(http.StatusUnauthorized, "some-key", cause)
	if !IsBlocked(blocked401) {
		t.Fatalf("401 error not classified as blocked: %v", blocked401)
	}

	retryable500 := wrapDialError(http.StatusServiceUnavailable, "", cause)
	if !errors.Is(retryable500, errRetryableSessionStart) {
		t.Fatalf("503 error should be retryable: %v", retryable500)
	}

	retryableNoStatus := wrapDialError(0, "", cause)
	if !errors.Is(retryableNoStatus, errRetryableSessionStart) {
		t.Fatalf("statusless dial error should be retryable: %v", retryableNoStatus)
	}
}

func Test_acquireTemporarySessionKey_returnsKey_whenUpstreamOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/c/api/user/sessions/temporary" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(temporarySessionResponse{SessionKey: "abc123"})
	}))
	defer server.Close()

	c := &Client{http: server.Client(), sessionsURL: server.URL + "/c/api/user/sessions/temporary"}
	key, err := c.acquireTemporarySessionKey(context.Background(), nil)
	if err != nil {
		t.Fatalf("acquireTemporarySessionKey() error = %v", err)
	}
	if key != "abc123" {
		t.Fatalf("session key = %q, want %q", key, "abc123")
	}
}

func Test_acquireTemporarySessionKey_returnsBlocked_whenUpstream451(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(451)
		_, _ = w.Write([]byte(`{"status":451}`))
	}))
	defer server.Close()

	c := &Client{http: server.Client(), sessionsURL: server.URL + "/c/api/user/sessions/temporary"}
	_, err := c.acquireTemporarySessionKey(context.Background(), nil)
	if err == nil {
		t.Fatal("acquireTemporarySessionKey() error = nil, want blocked error")
	}
	if !IsBlocked(err) {
		t.Fatalf("451 error not classified as blocked: %v", err)
	}
	if errors.Is(err, errRetryableSessionStart) {
		t.Fatalf("451 error should not be retryable: %v", err)
	}
}

func Test_acquireTemporarySessionKey_errors_whenSessionKeyMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	c := &Client{http: server.Client(), sessionsURL: server.URL + "/c/api/user/sessions/temporary"}
	if _, err := c.acquireTemporarySessionKey(context.Background(), nil); err == nil {
		t.Fatal("acquireTemporarySessionKey() error = nil, want missing-sessionKey error")
	}
}

func Test_acquireTemporarySessionKey_skips_whenSessionsURLEmpty(t *testing.T) {
	c := &Client{sessionsURL: ""}
	key, err := c.acquireTemporarySessionKey(context.Background(), nil)
	if err != nil {
		t.Fatalf("acquireTemporarySessionKey() error = %v", err)
	}
	if key != "" {
		t.Fatalf("session key = %q, want empty when sessions URL unset", key)
	}
}
