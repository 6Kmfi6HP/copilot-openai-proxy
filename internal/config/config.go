package config

import (
	"flag"
	"os"
)

// Config holds the application configuration.
type Config struct {
	Addr            string // listen address, e.g. "127.0.0.1:8080"
	APIKey          string // optional bearer token for client auth
	MaxSessions     int    // max concurrent Copilot WebSocket sessions
	SessionTTL      int    // session idle TTL in seconds
	CleanupInterval int   // session cleanup interval in seconds
	ConnTimeout     int   // WebSocket connection timeout in seconds
	Timeout         int    // request timeout in seconds
	Debug           bool   // print raw protocol logs
	TimeZone        string // timezone sent to Copilot start endpoint
}

// Load reads configuration from flags / environment.
func Load() *Config {
	cfg := &Config{}
	flag.StringVar(&cfg.Addr, "host", envOr("HOST", "127.0.0.1"), "listen host")
	flag.StringVar(&cfg.APIKey, "api-key", envOr("API_KEY", ""), "API key; when set, requests require Authorization header")
	flag.IntVar(&cfg.MaxSessions, "max-sessions", 1000, "max in-memory sessions")
	flag.IntVar(&cfg.SessionTTL, "session-ttl", 1800, "session expiry in seconds")
	flag.IntVar(&cfg.CleanupInterval, "cleanup-interval", 300, "session cleanup interval in seconds")
	flag.IntVar(&cfg.ConnTimeout, "conn-timeout", 20, "WebSocket connection timeout in seconds")
	flag.IntVar(&cfg.Timeout, "timeout", 120, "request timeout in seconds")
	flag.BoolVar(&cfg.Debug, "debug", false, "print raw protocol logs")
	flag.StringVar(&cfg.TimeZone, "timezone", envOr("TIMEZONE", "Asia/Shanghai"), "timezone sent to Copilot start endpoint")

	// Parse -port separately so it merges with -host.
	port := flag.String("port", envOr("PORT", "8080"), "listen port")
	flag.Parse()

	cfg.Addr = cfg.Addr + ":" + *port
	return cfg
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}