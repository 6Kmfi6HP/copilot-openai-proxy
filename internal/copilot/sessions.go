package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
)

// temporarySessionKeyHeader carries the anonymous temporary-conversation session
// key on the chat WebSocket handshake. The official web client sends the same key
// both as this header and as the temporarySessionKey query parameter; without it
// the chat endpoint rejects the handshake with HTTP 460.
const temporarySessionKeyHeader = "X-Copilot-TemporarySessionKey"

type temporarySessionResponse struct {
	SessionKey string `json:"sessionKey"`
}

// acquireTemporarySessionKey mints an anonymous temporary-conversation session key
// via POST /c/api/user/sessions/temporary. Copilot now gates the chat WebSocket on
// this key: a dial without it returns HTTP 460. A blank sessionsURL disables the
// step (the dial then proceeds without a key, e.g. against a stub upstream).
func (c *Client) acquireTemporarySessionKey(ctx context.Context, cookies []*http.Cookie) (string, error) {
	if c.sessionsURL == "" {
		return "", nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.sessionsURL, bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", fmt.Errorf("create temporary session request: %w", err)
	}
	setCommonHeaders(req.Header)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", acceptLanguage)
	req.Header.Set("Origin", copilotOrigin)
	for _, ck := range cookies {
		if ck != nil && ck.Name != "" && ck.Value != "" {
			req.AddCookie(ck)
		}
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", newRetryableSessionStartError("temporary session request", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read temporary session response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("copilot temporary session returned %d: %s", resp.StatusCode, truncateForLog(string(body)))
		// 451/403 are hard region/IP blocks (Cloudflare bot management + the
		// "anonymous-block-page" flight); retrying cannot help.
		if resp.StatusCode == 451 || resp.StatusCode == http.StatusForbidden {
			return "", NewBlockedError(msg + " — anonymous Copilot access appears blocked for this IP/region; route outbound traffic through PROXY_URL to a supported region")
		}
		if isRetryableStartStatus(resp.StatusCode) {
			return "", newRetryableSessionStartMessage(msg)
		}
		return "", fmt.Errorf("%s", msg)
	}

	var parsed temporarySessionResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", newRetryableSessionStartError("parse temporary session response", err)
	}
	if parsed.SessionKey == "" {
		return "", fmt.Errorf("copilot temporary session response missing sessionKey")
	}

	log.Printf("acquired copilot temporary session key len=%d", len(parsed.SessionKey))
	return parsed.SessionKey, nil
}
