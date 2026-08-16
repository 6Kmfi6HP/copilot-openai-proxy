package copilot

import (
	"strings"
	"time"
)

const (
	defaultCopilotStartURL       = "https://copilot.microsoft.com/c/api/start"
	defaultCopilotWSURL          = "wss://copilot.microsoft.com/c/api/chat"
	defaultCopilotAttachmentsURL = "https://copilot.microsoft.com/c/api/attachments"
	defaultCopilotSessionsURL    = "https://copilot.microsoft.com/c/api/user/sessions/temporary"
	defaultTimeZone              = "Asia/Shanghai"
)

type ClientConfig struct {
	MaxSessions    int
	WarmSessions   int
	SessionTTL     time.Duration
	CleanupInt     time.Duration
	ConnTimeout    time.Duration
	Timeout        time.Duration
	WSReadTimeout  time.Duration
	WSWriteTimeout time.Duration
	WSPingInterval time.Duration
	Debug          bool
	TimeZone       string
	ProxyURL       string
	StartURL       string
	WSURL          string
	AttachmentsURL string
	SessionsURL    string
}

func (cfg ClientConfig) normalized() ClientConfig {
	if cfg.MaxSessions <= 0 {
		cfg.MaxSessions = 1
	}
	if cfg.WarmSessions < 0 {
		cfg.WarmSessions = 0
	}
	if cfg.WarmSessions > cfg.MaxSessions {
		cfg.WarmSessions = cfg.MaxSessions
	}
	if cfg.TimeZone == "" {
		cfg.TimeZone = defaultTimeZone
	}
	if cfg.StartURL == "" {
		cfg.StartURL = defaultCopilotStartURL
	}
	if cfg.WSURL == "" {
		cfg.WSURL = defaultCopilotWSURL
	}
	if cfg.AttachmentsURL == "" {
		cfg.AttachmentsURL = attachmentsURLFromStart(cfg.StartURL)
	}
	if cfg.SessionsURL == "" {
		cfg.SessionsURL = sessionsURLFromStart(cfg.StartURL)
	}
	return cfg
}

func attachmentsURLFromStart(startURL string) string {
	const suffix = "/c/api/start"
	if strings.HasSuffix(startURL, suffix) {
		return strings.TrimSuffix(startURL, "/start") + "/attachments"
	}
	return defaultCopilotAttachmentsURL
}

// sessionsURLFromStart derives the anonymous temporary-conversation session
// endpoint from the start URL. Copilot's chat WebSocket handshake now requires a
// temporary session key minted by POST /c/api/user/sessions/temporary.
func sessionsURLFromStart(startURL string) string {
	const suffix = "/c/api/start"
	if strings.HasSuffix(startURL, suffix) {
		return strings.TrimSuffix(startURL, "/start") + "/user/sessions/temporary"
	}
	return defaultCopilotSessionsURL
}
