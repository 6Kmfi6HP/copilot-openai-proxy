package copilot

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

func buildWebSocketURL(baseURL string) (string, string, error) {
	clientSessionID := uuid.NewString()
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", "", fmt.Errorf("parse copilot websocket URL: %w", err)
	}
	query := parsed.Query()
	query.Set("api-version", "2")
	query.Set("clientSessionId", clientSessionID)
	parsed.RawQuery = query.Encode()
	return parsed.String(), clientSessionID, nil
}

func newSendMessage(prompt, conversationID, mode string, imageURLs []string) sendMessage {
	mode, product := protocolModeAndProduct(mode)
	content := make([]sendContent, 0, len(imageURLs)+1)
	for _, imageURL := range imageURLs {
		if imageURL == "" {
			continue
		}
		content = append(content, sendContent{Type: "image", URL: imageURL})
	}
	content = append(content, sendContent{Type: "text", Text: prompt})
	return sendMessage{
		Event:          "send",
		Content:        content,
		ConversationID: conversationID,
		Mode:           mode,
		Product:        product,
	}
}

func protocolModeAndProduct(model string) (string, string) {
	normalized := strings.ToLower(strings.TrimSpace(model))
	switch normalized {
	case "creative":
		return "chat", "creative"
	case "balanced":
		return "chat", "balanced"
	case "precise":
		return "chat", "precise"
	case "":
		return "smart", "smart"
	default:
		// Dynamic Copilot conversation modes use mode=product=<id>.
		return normalized, normalized
	}
}

func newChallengeAnswer(answer string) challengeAnswerMessage {
	return challengeAnswerMessage{
		Event:  "answer",
		Answer: answer,
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
