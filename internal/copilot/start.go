package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type startRequestBody struct {
	TimeZone                      string `json:"timeZone"`
	StartNewConversation          bool   `json:"startNewConversation"`
	TeenSupportEnabled            bool   `json:"teenSupportEnabled"`
	PerformUserMerge              *bool  `json:"performUserMerge"`
	CorrectPersonalizationSetting bool   `json:"correctPersonalizationSetting"`
	DeferredDataUseCapable        bool   `json:"deferredDataUseCapable"`
}

type startResponse struct {
	CurrentConversationID string `json:"currentConversationId"`
	IsBlocked             bool   `json:"isBlocked"`
}

func (c *Client) startAnonOnce(ctx context.Context) (*SessionState, error) {
	cookies, convID, err := c.acquireAnonCookies(ctx)
	if err != nil {
		return nil, fmt.Errorf("copilot start: %w", err)
	}
	if convID == "" {
		convID = uuid.NewString()
	}

	sessionKey, err := c.acquireTemporarySessionKey(ctx, cookies)
	if err != nil {
		return nil, fmt.Errorf("copilot temporary session: %w", err)
	}

	wsURL, clientSessionID, err := buildWebSocketURL(c.wsURL, sessionKey)
	if err != nil {
		return nil, err
	}

	header := http.Header{}
	setCommonHeaders(header)
	header.Set("Accept", "*/*")
	header.Set("Origin", copilotOrigin)
	header.Set("Cookie", collectCookies(cookies))
	if sessionKey != "" {
		header.Set(temporarySessionKeyHeader, sessionKey)
	}

	conn, resp, err := c.wsDialer.DialContext(ctx, wsURL, header)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		closeResponseBody(resp)
		return nil, wrapDialError(status, sessionKey, err)
	}

	session := &SessionState{
		Conn:                conn,
		ConversationID:      convID,
		ClientSessionID:     clientSessionID,
		TemporarySessionKey: sessionKey,
		Cookies:             cookies,
		CreatedAt:           time.Now(),
		LastUsedAt:          time.Now(),
	}
	session.setConnected(true)

	if err := c.waitForConnected(ctx, conn); err != nil {
		closeSessionConn(conn)
		return nil, newRetryableSessionStartError("copilot wait connected", err)
	}
	if err := sendEvent(conn, defaultSetOptions()); err != nil {
		closeSessionConn(conn)
		return nil, newRetryableSessionStartError("copilot setOptions", err)
	}

	return session, nil
}

// wrapDialError turns a WebSocket handshake failure into an actionable error.
// HTTP 460/401 mean the anonymous temporary session key was missing/rejected or
// anonymous access is blocked in this region, so retrying is pointless; other
// statuses stay retryable to preserve transient-failure recovery.
func wrapDialError(status int, sessionKey string, err error) error {
	switch status {
	case 460:
		detail := "copilot rejected the websocket handshake (HTTP 460): anonymous chat requires a temporary session key"
		if sessionKey == "" {
			detail += " but none was obtained"
		} else {
			detail += " and the provided key was not accepted — anonymous access may be blocked in this IP/region " +
				"(Microsoft 'anonymous-block-page' flight); route outbound traffic through PROXY_URL to a supported region"
		}
		return NewBlockedError(fmt.Sprintf("%s: %v", detail, err))
	case http.StatusUnauthorized:
		return NewBlockedError(fmt.Sprintf("copilot rejected the websocket handshake (HTTP 401): temporary session key rejected or expired: %v", err))
	}
	if status != 0 && isRetryableStartStatus(status) {
		return newRetryableSessionStartMessage(fmt.Sprintf("copilot websocket dial returned %d: %v", status, err))
	}
	return newRetryableSessionStartError("copilot websocket dial", err)
}

func (c *Client) acquireAnonCookies(ctx context.Context) ([]*http.Cookie, string, error) {
	body := startRequestBody{
		TimeZone:                      c.timeZone,
		StartNewConversation:          true,
		TeenSupportEnabled:            true,
		PerformUserMerge:              nil,
		CorrectPersonalizationSetting: true,
		DeferredDataUseCapable:        true,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, "", fmt.Errorf("marshal start body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.startURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, "", fmt.Errorf("create copilot start request: %w", err)
	}
	setCommonHeaders(req.Header)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", acceptLanguage)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", newRetryableSessionStartError("request", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read copilot start response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		message := fmt.Sprintf("copilot start returned %d: %s", resp.StatusCode, string(respBody))
		if isRetryableStartStatus(resp.StatusCode) {
			return nil, "", newRetryableSessionStartMessage(message)
		}
		return nil, "", fmt.Errorf("copilot start returned %d: %s", resp.StatusCode, string(respBody))
	}

	var startResp startResponse
	if err := json.Unmarshal(respBody, &startResp); err != nil {
		return nil, "", newRetryableSessionStartError("parse copilot start response", err)
	}
	if startResp.IsBlocked {
		return nil, "", fmt.Errorf("copilot start reports anonymous user is blocked; websocket may not produce completions")
	}

	cookies := cookiesWithJarFallback(c.http.Jar, c.startURL, resp.Cookies())
	anon := findCookie(cookies, CookieAnon)
	if anon == nil {
		return nil, "", fmt.Errorf("copilot start did not return __Host-copilot-anon cookie")
	}

	expires := ""
	if !anon.Expires.IsZero() {
		expires = anon.Expires.Format(time.RFC3339)
	}
	log.Printf("acquired copilot anon cookie session=temporary cookie_names=%s expires=%s current_conversation_id=%s",
		cookieNames(cookies), expires, startResp.CurrentConversationID)

	return cookies, startResp.CurrentConversationID, nil
}
