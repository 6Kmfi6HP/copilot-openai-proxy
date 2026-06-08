package config

import (
	"flag"
	"io"
	"os"
	"testing"
)

func TestLoad_readsWarmAndWebSocketTimeoutConfig(t *testing.T) {
	t.Setenv("MAX_SESSIONS", "6")
	t.Setenv("WARM_SESSIONS", "9")
	t.Setenv("WS_READ_TIMEOUT", "90")
	t.Setenv("WS_WRITE_TIMEOUT", "11")
	t.Setenv("WS_PING_INTERVAL", "27")
	t.Setenv("CONN_TIMEOUT", "13")
	t.Setenv("TIMEOUT", "44")

	cfg := loadForTest(t)

	if cfg.MaxSessions != 6 {
		t.Fatalf("MaxSessions = %d, want %d", cfg.MaxSessions, 6)
	}
	if cfg.WarmSessions != 6 {
		t.Fatalf("WarmSessions = %d, want capped %d", cfg.WarmSessions, 6)
	}
	if cfg.WSReadTimeout != 90 {
		t.Fatalf("WSReadTimeout = %d, want %d", cfg.WSReadTimeout, 90)
	}
	if cfg.WSWriteTimeout != 11 {
		t.Fatalf("WSWriteTimeout = %d, want %d", cfg.WSWriteTimeout, 11)
	}
	if cfg.WSPingInterval != 27 {
		t.Fatalf("WSPingInterval = %d, want %d", cfg.WSPingInterval, 27)
	}
	if cfg.ConnTimeout != 13 {
		t.Fatalf("ConnTimeout = %d, want %d", cfg.ConnTimeout, 13)
	}
	if cfg.Timeout != 44 {
		t.Fatalf("Timeout = %d, want %d", cfg.Timeout, 44)
	}
}

func TestLoad_usesDefaultsForWarmAndDeadlineConfig(t *testing.T) {
	cfg := loadForTest(t)

	if cfg.WarmSessions != 4 {
		t.Fatalf("WarmSessions = %d, want %d", cfg.WarmSessions, 4)
	}
	if cfg.WSReadTimeout != 60 {
		t.Fatalf("WSReadTimeout = %d, want %d", cfg.WSReadTimeout, 60)
	}
	if cfg.WSWriteTimeout != 10 {
		t.Fatalf("WSWriteTimeout = %d, want %d", cfg.WSWriteTimeout, 10)
	}
	if cfg.WSPingInterval != 25 {
		t.Fatalf("WSPingInterval = %d, want %d", cfg.WSPingInterval, 25)
	}
	if cfg.ConnTimeout != 20 {
		t.Fatalf("ConnTimeout = %d, want %d", cfg.ConnTimeout, 20)
	}
	if cfg.Timeout != 120 {
		t.Fatalf("Timeout = %d, want %d", cfg.Timeout, 120)
	}
}

func TestLoad_readsAdvancedUpstreamOverrides(t *testing.T) {
	t.Setenv("COPILOT_START_URL", "http://127.0.0.1:8081/start")
	t.Setenv("COPILOT_WS_URL", "ws://127.0.0.1:8081/chat")

	cfg := loadForTest(t)

	if cfg.CopilotStartURL != "http://127.0.0.1:8081/start" {
		t.Fatalf("CopilotStartURL = %q, want %q", cfg.CopilotStartURL, "http://127.0.0.1:8081/start")
	}
	if cfg.CopilotWSURL != "ws://127.0.0.1:8081/chat" {
		t.Fatalf("CopilotWSURL = %q, want %q", cfg.CopilotWSURL, "ws://127.0.0.1:8081/chat")
	}
}

func loadForTest(t *testing.T) *Config {
	t.Helper()

	originalArgs := os.Args
	originalFlagSet := flag.CommandLine

	flag.CommandLine = flag.NewFlagSet("config-test", flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)
	os.Args = []string{"config-test"}

	t.Cleanup(func() {
		flag.CommandLine = originalFlagSet
		os.Args = originalArgs
	})

	return Load()
}
