package config

import (
	"flag"
	"os"
	"strconv"
)

// Config holds the application configuration.
type Config struct {
	Addr            string // listen address, e.g. "127.0.0.1:8080"
	APIKey          string // optional bearer token for client auth
	MaxSessions     int    // max concurrent Copilot WebSocket sessions
	WarmSessions    int
	SessionTTL      int // session idle TTL in seconds
	CleanupInterval int // session cleanup interval in seconds
	ConnTimeout     int
	Timeout         int
	WSReadTimeout   int
	WSWriteTimeout  int
	WSPingInterval  int
	Debug           bool   // print raw protocol logs
	TimeZone        string // timezone sent to Copilot start endpoint
	ProxyURL        string // optional outbound proxy URL for Copilot traffic
	CopilotStartURL string
	CopilotWSURL    string
}

// Load reads configuration from flags / environment.
func Load() *Config {
	cfg := &Config{}
	flag.StringVar(&cfg.Addr, "host", envOr("HOST", "127.0.0.1"), "listen host")
	flag.StringVar(&cfg.APIKey, "api-key", envOr("API_KEY", ""), "API key; when set, requests require Authorization header")
	flag.IntVar(&cfg.MaxSessions, "max-sessions", envOrInt("MAX_SESSIONS", 1000), "max in-memory sessions")
	flag.IntVar(&cfg.WarmSessions, "warm-sessions", envOrInt("WARM_SESSIONS", 4), "target warm idle sessions")
	flag.IntVar(&cfg.SessionTTL, "session-ttl", envOrInt("SESSION_TTL", 1800), "session expiry in seconds")
	flag.IntVar(&cfg.CleanupInterval, "cleanup-interval", envOrInt("CLEANUP_INTERVAL", 300), "session cleanup interval in seconds")
	flag.IntVar(&cfg.ConnTimeout, "conn-timeout", envOrInt("CONN_TIMEOUT", 20), "WebSocket handshake timeout in seconds")
	flag.IntVar(&cfg.Timeout, "timeout", envOrInt("TIMEOUT", 120), "default acquire/start timeout in seconds")
	flag.IntVar(&cfg.WSReadTimeout, "ws-read-timeout", envOrInt("WS_READ_TIMEOUT", 60), "WebSocket read timeout in seconds")
	flag.IntVar(&cfg.WSWriteTimeout, "ws-write-timeout", envOrInt("WS_WRITE_TIMEOUT", 10), "WebSocket write timeout in seconds")
	flag.IntVar(&cfg.WSPingInterval, "ws-ping-interval", envOrInt("WS_PING_INTERVAL", 25), "WebSocket ping interval in seconds")
	flag.BoolVar(&cfg.Debug, "debug", envOrBool("DEBUG", false), "print raw protocol logs")
	flag.StringVar(&cfg.TimeZone, "timezone", envOr("TIMEZONE", "Asia/Shanghai"), "timezone sent to Copilot start endpoint")
	flag.StringVar(&cfg.ProxyURL, "proxy-url", envOr("PROXY_URL", ""), "outbound proxy URL for Copilot HTTP/WebSocket traffic")
	cfg.CopilotStartURL = envOr("COPILOT_START_URL", "")
	cfg.CopilotWSURL = envOr("COPILOT_WS_URL", "")

	// Parse -port separately so it merges with -host.
	port := flag.String("port", envOr("PORT", "8080"), "listen port")
	flag.Parse()

	if cfg.WarmSessions > cfg.MaxSessions {
		cfg.WarmSessions = cfg.MaxSessions
	}
	if cfg.WarmSessions < 0 {
		cfg.WarmSessions = 0
	}
	cfg.Addr = cfg.Addr + ":" + *port
	return cfg
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envOrInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return parsed
}

func envOrBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}

	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return parsed
}
