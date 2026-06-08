package copilot

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/gorilla/websocket"
)

// StreamEventType represents the type of a Copilot streaming event.
type StreamEventType int

const (
	EventIgnore StreamEventType = iota
	EventAppendText
	EventStartMessage
	EventError
	EventDone
	EventChallenge
)

// StreamEvent is a single event emitted from the Copilot WebSocket read loop.
type StreamEvent struct {
	Type           StreamEventType
	Text           string // EventAppendText
	MessageID      string // EventStartMessage
	ConversationID string // EventStartMessage
	Err            error  // EventError
	ChallengeParam string // EventChallenge: hashcash parameter
}

// serverEnvelope wraps messages received from the Copilot WebSocket.
// The real Copilot protocol uses "event" field (not "type").
type serverEnvelope struct {
	Event          string `json:"event"` // connected, received, startMessage, appendText, partCompleted, done, error, challenge
	ConversationID string `json:"conversationId,omitempty"`
	MessageID      string `json:"messageId,omitempty"`
	PartID         string `json:"partId,omitempty"`
	Text           string `json:"text,omitempty"`      // appendText
	Body           string `json:"body,omitempty"`      // error body
	ErrorCode      string `json:"errorCode,omitempty"` // error code
	Method         string `json:"method,omitempty"`    // challenge method (e.g. "hashcash")
	Parameter      string `json:"parameter,omitempty"` // challenge parameter
	RequestID      string `json:"requestId,omitempty"` // connected
	CreatedAt      string `json:"createdAt,omitempty"`
	ID             string `json:"id,omitempty"` // sequential event ID
}

// --- Outbound protocol messages ---

// setOptionsMessage is sent right after connecting to configure the session.
type setOptionsMessage struct {
	Event                 string            `json:"event"`
	SupportedCards        []string          `json:"supportedCards"`
	Ads                   setOptionsAds     `json:"ads"`
	SupportedActions      []string          `json:"supportedActions"`
	SupportedFeatures     []string          `json:"supportedFeatures"`
	SupportedUIComponents map[string]string `json:"supportedUIComponents"`
}

type setOptionsAds struct {
	SupportedTypes          []string       `json:"supportedTypes"`
	OptOutOfPersonalization bool           `json:"optOutOfPersonalization"`
	Product                 productOptions `json:"product"`
}

type productOptions struct {
	TagsSupported bool `json:"tagsSupported"`
}

// sendContent represents a content item in a Copilot WebSocket send message.
// This supports richer prompts with multiple content parts.
type sendContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// sendMessage is the full message sent to the Copilot WebSocket
// when sending a prompt with a specific conversation action.
type sendMessage struct {
	Event          string        `json:"event"`
	Content        []sendContent `json:"content"`
	ConversationID string        `json:"conversationId"`
	Mode           string        `json:"mode"`
	Product        string        `json:"product"`
}

func defaultSetOptions() setOptionsMessage {
	return setOptionsMessage{
		Event:          "setOptions",
		SupportedCards: defaultSupportedCards(),
		Ads: setOptionsAds{
			SupportedTypes:          defaultSupportedTypes(),
			OptOutOfPersonalization: true,
			Product:                 productOptions{TagsSupported: false},
		},
		SupportedActions:  []string{},
		SupportedFeatures: defaultSupportedFeatures(),
		SupportedUIComponents: map[string]string{
			"Text":         "1.2",
			"Row":          "1.2",
			"Col":          "1.2",
			"Box":          "1.2",
			"Title":        "1.2",
			"Caption":      "1.2",
			"Divider":      "1.2",
			"Spacer":       "1.2",
			"Icon":         "1.2",
			"Badge":        "1.2",
			"Image":        "1.2",
			"Card":         "1.2",
			"Markdown":     "1.2",
			"ListViewItem": "1.2",
			"ListView":     "1.2",
			"Button":       "1.2",
			"Map":          "1.3",
			"Table":        "1.3",
			"Table.Row":    "1.3",
			"Table.Cell":   "1.3",
		},
	}
}

// parseServerEvent parses an incoming WebSocket message into a StreamEvent.
// Copilot protocol events:
//   - connected: session established
//   - received: server acknowledged our message
//   - startMessage: assistant starts generating
//   - appendText: partial text delta
//   - partCompleted: one part of the response is done
//   - done: full response complete
//   - error: something went wrong
func parseServerEvent(raw []byte) (StreamEvent, error) {
	var env serverEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return StreamEvent{}, fmt.Errorf("parse copilot event failed: %w", err)
	}

	switch env.Event {
	case "appendText":
		return StreamEvent{Type: EventAppendText, Text: env.Text}, nil
	case "startMessage":
		return StreamEvent{
			Type:           EventStartMessage,
			MessageID:      env.MessageID,
			ConversationID: env.ConversationID,
		}, nil
	case "challenge":
		// Hashcash challenge: the client needs to solve this and respond.
		// For now we pass it through; the readLoop will handle it.
		return StreamEvent{
			Type:           EventChallenge,
			ChallengeParam: env.Parameter,
		}, nil
	case "error":
		body := firstNonEmpty(env.Body, env.Text, env.ErrorCode)
		if strings.Contains(body, "blocked") {
			return StreamEvent{
				Type: EventError,
				Err:  fmt.Errorf("copilot start reports session user is blocked; websocket may not produce completions"),
			}, nil
		}
		return StreamEvent{
			Type: EventError,
			Err:  upstreamErrorf("upstream_error: %s", body),
		}, nil
	case "done":
		return StreamEvent{Type: EventDone, MessageID: env.MessageID}, nil
	case "connected", "ready":
		return StreamEvent{Type: EventIgnore}, nil
	case "received", "partCompleted":
		return StreamEvent{Type: EventIgnore}, nil
	default:
		return StreamEvent{Type: EventIgnore}, nil
	}
}

// sendEvent serialises an outbound message and writes it to the WebSocket.
func sendEvent(ws *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	// Log what we're sending.
	var m map[string]any
	if json.Unmarshal(data, &m) == nil {
		evt, _ := m["event"].(string)
		if evt == "" {
			evt, _ = m["type"].(string)
		}
		if evt != "" {
			log.Printf("copilot send event=%s", evt)
		}
	}
	return ws.WriteMessage(websocket.TextMessage, data)
}
