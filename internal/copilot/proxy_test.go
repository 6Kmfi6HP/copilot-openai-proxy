package copilot

import (
	"net/http"
	"testing"
	"time"
)

func Test_newProxyFunc_returnsError_whenProxyURLIsInvalid(t *testing.T) {
	_, err := newProxyFunc("://invalid")

	if err == nil {
		t.Fatal("newProxyFunc returned nil error, want invalid proxy error")
	}
}

func Test_newProxyFunc_usesConfiguredProxy_whenURLIsProvided(t *testing.T) {
	proxyFunc, err := newProxyFunc("http://proxy.internal:8080")
	if err != nil {
		t.Fatalf("newProxyFunc returned error: %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, "https://copilot.microsoft.com/c/api/start", nil)
	if err != nil {
		t.Fatalf("http.NewRequest returned error: %v", err)
	}

	proxyURL, err := proxyFunc(req)
	if err != nil {
		t.Fatalf("proxyFunc returned error: %v", err)
	}
	if proxyURL == nil {
		t.Fatal("proxyURL is nil, want configured proxy")
	}
	if got := proxyURL.String(); got != "http://proxy.internal:8080" {
		t.Fatalf("proxyURL = %q, want %q", got, "http://proxy.internal:8080")
	}
}

func Test_NewClient_usesConfiguredProxyForHTTPAndWebSocket(t *testing.T) {
	client, err := NewClient(ClientConfig{
		MaxSessions:    16,
		WarmSessions:   0,
		SessionTTL:     time.Second,
		CleanupInt:     time.Second,
		ConnTimeout:    time.Second,
		Timeout:        time.Second,
		WSReadTimeout:  time.Minute,
		WSWriteTimeout: time.Second,
		WSPingInterval: 25 * time.Second,
		Debug:          false,
		TimeZone:       "UTC",
		ProxyURL:       "http://proxy.internal:8080",
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	defer closeTestClient(t, client)

	req, err := http.NewRequest(http.MethodGet, "https://copilot.microsoft.com/c/api/start", nil)
	if err != nil {
		t.Fatalf("http.NewRequest returned error: %v", err)
	}

	httpTransport, ok := client.http.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("client http transport type = %T, want *http.Transport", client.http.Transport)
	}
	httpProxyURL, err := httpTransport.Proxy(req)
	if err != nil {
		t.Fatalf("http proxy returned error: %v", err)
	}
	if httpProxyURL == nil || httpProxyURL.String() != "http://proxy.internal:8080" {
		t.Fatalf("http proxy = %v, want %q", httpProxyURL, "http://proxy.internal:8080")
	}

	wsProxyURL, err := client.wsDialer.Proxy(req)
	if err != nil {
		t.Fatalf("websocket proxy returned error: %v", err)
	}
	if wsProxyURL == nil || wsProxyURL.String() != "http://proxy.internal:8080" {
		t.Fatalf("websocket proxy = %v, want %q", wsProxyURL, "http://proxy.internal:8080")
	}
}
