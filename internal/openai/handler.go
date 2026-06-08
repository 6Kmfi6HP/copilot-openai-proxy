package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"copilot-openai-proxy/internal/copilot"
	"copilot-openai-proxy/internal/util"
)

// Handler implements the OpenAI-compatible API.
type Handler struct {
	client chatClient
}

// NewHandler creates a new OpenAI API handler backed by the given Copilot client.
func NewHandler(client *copilot.Client) *Handler {
	return &Handler{client: client}
}

type chatClient interface {
	Complete(rctx context.Context, input copilot.CompletionInput) (string, string, error)
	StreamEvents(rctx context.Context, input copilot.CompletionInput) (<-chan copilot.StreamEvent, error)
}

// ChatCompletions handles POST /v1/chat/completions.
func (h *Handler) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "only POST is supported")
		return
	}

	var req ChatCompletionRequest
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request_error", "failed to read request body")
		return
	}
	defer r.Body.Close()

	if err := json.Unmarshal(body, &req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON: "+err.Error())
		return
	}

	selectedModel := normalizeModel(req.Model)
	mode := modeForModel(selectedModel)

	// tools/function calling is not supported.
	if req.Tools != nil || req.ToolChoice != nil || req.FunctionCall != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request_error", "tools/function calling is not supported")
		return
	}

	// Build the prompt from messages.
	prompt := buildPrompt(req)

	input := copilot.CompletionInput{
		Prompt:      prompt,
		Mode:        mode,
		Stream:      req.Stream,
		StreamModel: selectedModel,
	}

	if req.Stream {
		h.streamResponse(w, r, input)
	} else {
		h.fullResponse(w, r, input, req)
	}
}

// buildPrompt assembles a text prompt from the OpenAI chat messages.
// The Copilot WebSocket expects a plain text prompt.
func buildPrompt(req ChatCompletionRequest) string {
	var b strings.Builder
	for _, msg := range req.Messages {
		role := msg.Role
		content := messageContentText(msg.Content)
		switch role {
		case "system":
			b.WriteString("[System] ")
			b.WriteString(content)
			b.WriteString("\n\n")
		case "user":
			b.WriteString(content)
			b.WriteString("\n")
		case "assistant":
			b.WriteString("[Assistant] ")
			b.WriteString(content)
			b.WriteString("\n")
		default:
			b.WriteString(content)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// messageContentText extracts text from a message content field.
// Supports both plain string content and array of content parts (text-only).
func messageContentText(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		var b strings.Builder
		for _, part := range v {
			if m, ok := part.(map[string]interface{}); ok {
				if m["type"] == "text" {
					if text, ok := m["text"].(string); ok {
						if b.Len() > 0 {
							b.WriteString("\n")
						}
						b.WriteString(text)
					}
				}
			}
		}
		return b.String()
	default:
		return fmt.Sprintf("%v", content)
	}
}

func normalizeModel(model string) string {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "", "smart":
		return "smart"
	case "creative":
		return "creative"
	case "balanced":
		return "balanced"
	case "precise":
		return "precise"
	default:
		return "smart"
	}
}

func modeForModel(model string) string {
	return normalizeModel(model)
}

// fullResponse handles a non-streaming completion request.
func (h *Handler) fullResponse(w http.ResponseWriter, r *http.Request, input copilot.CompletionInput, req ChatCompletionRequest) {
	text, _, err := h.client.Complete(r.Context(), input)
	if err != nil {
		if copilot.IsBlocked(err) {
			WriteError(w, http.StatusForbidden, "authentication_error", err.Error())
			return
		}
		WriteError(w, http.StatusBadGateway, "server_error", err.Error())
		return
	}

	resp := ChatCompletionResponse{
		ID:      util.OpenAIChatCompletionID(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   input.StreamModel,
		Choices: []Choice{
			{
				Index: 0,
				Message: AssistantMessage{
					Role:    "assistant",
					Content: text,
				},
				FinishReason: "stop",
			},
		},
		Usage: Usage{},
	}
	writeJSON(w, http.StatusOK, resp)
}

// streamResponse handles SSE streaming of a completion.
func (h *Handler) streamResponse(w http.ResponseWriter, r *http.Request, input copilot.CompletionInput) {
	events, err := h.client.StreamEvents(r.Context(), input)
	if err != nil {
		WriteError(w, http.StatusBadGateway, "server_error", err.Error())
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, http.StatusInternalServerError, "server_error", "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	completionID := util.OpenAIChatCompletionID()
	created := time.Now().Unix()

	sse := NewSSEWriter(w, flusher)

	// Initial chunk with role.
	sse.WriteJSON(ChatCompletionChunk{
		ID:      completionID,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   input.StreamModel,
		Choices: []ChunkChoice{
			{Index: 0, Delta: ChunkDelta{Role: "assistant"}},
		},
	})

	// Stream tokens.
	for evt := range events {
		switch evt.Type {
		case copilot.EventAppendText:
			sse.WriteJSON(ChatCompletionChunk{
				ID:      completionID,
				Object:  "chat.completion.chunk",
				Created: created,
				Model:   input.StreamModel,
				Choices: []ChunkChoice{
					{Index: 0, Delta: ChunkDelta{Content: evt.Text}},
				},
			})
		case copilot.EventError:
			log.Printf("stream error: %v", evt.Err)
			// End stream on error.
			doneReason := "stop"
			sse.WriteDone(ChatCompletionChunk{
				ID:      completionID,
				Object:  "chat.completion.chunk",
				Created: created,
				Model:   input.StreamModel,
				Choices: []ChunkChoice{
					{Index: 0, Delta: ChunkDelta{}, FinishReason: &doneReason},
				},
			})
			return
		case copilot.EventDone:
			doneReason := "stop"
			sse.WriteDone(ChatCompletionChunk{
				ID:      completionID,
				Object:  "chat.completion.chunk",
				Created: created,
				Model:   input.StreamModel,
				Choices: []ChunkChoice{
					{Index: 0, Delta: ChunkDelta{}, FinishReason: &doneReason},
				},
			})
			return
		}
	}

	// If we get here without a done event, close gracefully.
	doneReason := "stop"
	sse.WriteDone(ChatCompletionChunk{
		ID:      completionID,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   input.StreamModel,
		Choices: []ChunkChoice{
			{Index: 0, Delta: ChunkDelta{}, FinishReason: &doneReason},
		},
	})
}

// --- Types ---

type ChatCompletionRequest struct {
	Model        string        `json:"model"`
	Messages     []ChatMessage `json:"messages"`
	Stream       bool          `json:"stream"`
	Tools        any           `json:"tools,omitempty"`
	ToolChoice   any           `json:"tool_choice,omitempty"`
	FunctionCall any           `json:"function_call,omitempty"`
}

type ChatMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string or []ContentPart
}

// TextContentPart represents a text content part in a multimodal message.
type TextContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ChatCompletionResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

type Choice struct {
	Index        int              `json:"index"`
	Message      AssistantMessage `json:"message"`
	FinishReason string           `json:"finish_reason"`
}

type AssistantMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatCompletionChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []ChunkChoice `json:"choices"`
}

type ChunkChoice struct {
	Index        int        `json:"index"`
	Delta        ChunkDelta `json:"delta"`
	FinishReason *string    `json:"finish_reason"`
}

type ChunkDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ErrorResponse struct {
	Error ErrorObject `json:"error"`
}

type ErrorObject struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    *string `json:"code"`
}
