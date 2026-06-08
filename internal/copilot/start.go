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

func (c *Client) startAnon(ctx context.Context) (*SessionState, error) {
	cookies, convID, err := c.acquireAnonCookies(ctx)
	if err != nil {
		return nil, fmt.Errorf("copilot start: %w", err)
	}
	if convID == "" {
		convID = uuid.NewString()
	}

	wsURL, clientSessionID, err := buildWebSocketURL(c.wsURL)
	if err != nil {
		return nil, err
	}

	header := http.Header{}
	setCommonHeaders(header)
	header.Set("Accept", "*/*")
	header.Set("Origin", copilotOrigin)
	header.Set("Cookie", collectCookies(cookies))

	conn, _, err := c.wsDialer.DialContext(ctx, wsURL, header)
	if err != nil {
		return nil, fmt.Errorf("copilot websocket dial: %w", err)
	}

	session := &SessionState{
		Conn:            conn,
		ConversationID:  convID,
		ClientSessionID: clientSessionID,
		Cookies:         cookies,
		Connected:       true,
		CreatedAt:       time.Now(),
		LastUsedAt:      time.Now(),
	}

	if err := c.waitForConnected(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("copilot wait connected: %w", err)
	}
	if err := sendEvent(conn, defaultSetOptions()); err != nil {
		conn.Close()
		return nil, fmt.Errorf("copilot setOptions: %w", err)
	}

	return session, nil
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
		return nil, "", fmt.Errorf("copilot start: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read copilot start response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("copilot start returned %d: %s", resp.StatusCode, string(respBody))
	}

	var startResp startResponse
	if err := json.Unmarshal(respBody, &startResp); err != nil {
		return nil, "", fmt.Errorf("parse copilot start response: %w", err)
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
