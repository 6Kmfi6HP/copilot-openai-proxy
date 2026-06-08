package copilot

import (
	"fmt"
	"net/url"

	"github.com/google/uuid"
)

func buildWebSocketURL() (string, string, error) {
	clientSessionID := uuid.NewString()
	parsed, err := url.Parse(copilotWSURL)
	if err != nil {
		return "", "", fmt.Errorf("parse copilot websocket URL: %w", err)
	}
	query := parsed.Query()
	query.Set("api-version", "2")
	query.Set("clientSessionId", clientSessionID)
	parsed.RawQuery = query.Encode()
	return parsed.String(), clientSessionID, nil
}

func newSendMessage(prompt, conversationID, mode string) sendMessage {
	if mode == "" {
		mode = "smart"
	}
	return sendMessage{
		Event: "send",
		Content: []sendContent{
			{Type: "text", Text: prompt},
		},
		ConversationID: conversationID,
		Mode:           mode,
		Product:        "smart",
	}
}

func defaultSupportedTypes() []string {
	return []string{"text", "multimedia", "product"}
}

func defaultSupportedCards() []string {
	return []string{
		"ads",
		"createCalendarEvent",
		"chart",
		"consentV2",
		"finance",
		"flashcard",
		"image",
		"local",
	}
}

func defaultSupportedActions() []string {
	return []string{}
}

func defaultSupportedFeatures() []string {
	return []string{
		"composer-prefill-conversation-action",
		"composer-send-conversation-action-v2",
		"short-conversation-action",
		"session-duration-nudge",
	}
}
