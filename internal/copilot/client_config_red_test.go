package copilot

import (
	"testing"
	"time"
)

func TestNewClient_UsesConfiguredConcurrencyTimeouts(t *testing.T) {
	t.Parallel()

	client, err := NewClient(ClientConfig{
		MaxSessions:    8,
		WarmSessions:   4,
		SessionTTL:     time.Minute,
		CleanupInt:     time.Minute,
		ConnTimeout:    3 * time.Second,
		Timeout:        17 * time.Second,
		WSReadTimeout:  61 * time.Second,
		WSWriteTimeout: 9 * time.Second,
		WSPingInterval: 23 * time.Second,
		Debug:          false,
		TimeZone:       "UTC",
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if client.http.Timeout != 17*time.Second {
		t.Fatalf("http.Timeout = %v, want %v", client.http.Timeout, 17*time.Second)
	}
	if client.wsDialer.HandshakeTimeout != 3*time.Second {
		t.Fatalf("HandshakeTimeout = %v, want %v", client.wsDialer.HandshakeTimeout, 3*time.Second)
	}
	if client.timeout != 17*time.Second {
		t.Fatalf("timeout = %v, want %v", client.timeout, 17*time.Second)
	}
	if client.warmSessions != 4 {
		t.Fatalf("warmSessions = %d, want %d", client.warmSessions, 4)
	}
	if client.wsReadTimeout != 61*time.Second {
		t.Fatalf("wsReadTimeout = %v, want %v", client.wsReadTimeout, 61*time.Second)
	}
	if client.wsWriteTimeout != 9*time.Second {
		t.Fatalf("wsWriteTimeout = %v, want %v", client.wsWriteTimeout, 9*time.Second)
	}
	if client.wsPingInterval != 23*time.Second {
		t.Fatalf("wsPingInterval = %v, want %v", client.wsPingInterval, 23*time.Second)
	}
	if client.startURL != defaultCopilotStartURL {
		t.Fatalf("startURL = %q, want %q", client.startURL, defaultCopilotStartURL)
	}
	if client.wsURL != defaultCopilotWSURL {
		t.Fatalf("wsURL = %q, want %q", client.wsURL, defaultCopilotWSURL)
	}
	if client.sessionMgr.maxSessions != 8 {
		t.Fatalf("sessionMgr.maxSessions = %d, want %d", client.sessionMgr.maxSessions, 8)
	}
}
