package openai

import (
	"context"
	"encoding/json"
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

type clientErrorResponse struct {
	statusCode int
	errType    string
	message    string
	retryAfter string
	swallow    bool
}

// NewHandler creates a new OpenAI API handler backed by the given Copilot client.
func NewHandler(client *copilot.Client) *Handler {
	return &Handler{client: client}
}

type chatClient interface {
	Complete(rctx context.Context, input copilot.CompletionInput) (string, string, error)
	StreamEvents(rctx context.Context, input copilot.CompletionInput) (<-chan copilot.StreamEvent, error)
	ListModels(rctx context.Context) ([]string, error)
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

	payload, err := buildCompletionPayload(req)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	input := copilot.CompletionInput{
		Prompt:      payload.Prompt,
		Mode:        mode,
		Stream:      req.Stream,
		StreamModel: selectedModel,
		Images:      payload.Images,
	}

	if req.Stream {
		h.streamResponse(w, r, input)
	} else {
		h.fullResponse(w, r, input, req)
	}
}

func normalizeModel(model string) string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return "smart"
	}
	normalized := strings.ToLower(trimmed)
	switch normalized {
	case "smart", "creative", "balanced", "precise":
		return normalized
	}
	// Pass through dynamic Copilot mode IDs; reject obvious garbage.
	if !isValidModelID(normalized) {
		return "smart"
	}
	return normalized
}

func isValidModelID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	if strings.ContainsAny(id, " \t\r\n") {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func modeForModel(model string) string {
	return normalizeModel(model)
}

// ListModels handles GET /v1/models using the upstream Copilot mode catalog.
func (h *Handler) ListModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "only GET is supported")
		return
	}
	modes, err := h.client.ListModels(r.Context())
	if err != nil || len(modes) == 0 {
		modes = []string{"smart"}
	}
	WriteModels(w, modes)
}

// fullResponse handles a non-streaming completion request.
func (h *Handler) fullResponse(w http.ResponseWriter, r *http.Request, input copilot.CompletionInput, req ChatCompletionRequest) {
	text, _, err := h.client.Complete(r.Context(), input)
	if err != nil {
		if wrote := writeClientError(w, classifyClientError(err)); wrote {
			return
		}
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
		if wrote := writeClientError(w, classifyClientError(err)); wrote {
			return
		}
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
	for {
		select {
		case <-r.Context().Done():
			writeStreamDone(sse, completionID, created, input.StreamModel)
			return
		case evt, ok := <-events:
			if !ok {
				writeStreamDone(sse, completionID, created, input.StreamModel)
				return
			}

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
			case copilot.EventImageGenerated:
				content := copilot.ImageMarkdown(evt.ImageURL)
				if content == "" {
					continue
				}
				sse.WriteJSON(ChatCompletionChunk{
					ID:      completionID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   input.StreamModel,
					Choices: []ChunkChoice{
						{Index: 0, Delta: ChunkDelta{Content: content}},
					},
				})
			case copilot.EventError:
				if copilot.IsClientCanceled(evt.Err) {
					writeStreamDone(sse, completionID, created, input.StreamModel)
					return
				}
				log.Printf("stream error: %v", evt.Err)
				writeStreamDone(sse, completionID, created, input.StreamModel)
				return
			case copilot.EventDone:
				writeStreamDone(sse, completionID, created, input.StreamModel)
				return
			}
		}
	}
}

func classifyClientError(err error) clientErrorResponse {
	switch {
	case err == nil:
		return clientErrorResponse{}
	case copilot.IsClientCanceled(err):
		return clientErrorResponse{swallow: true}
	case copilot.IsBlocked(err):
		return clientErrorResponse{
			statusCode: http.StatusForbidden,
			errType:    "authentication_error",
			message:    err.Error(),
		}
	case copilot.IsCapacity(err):
		return clientErrorResponse{
			statusCode: http.StatusServiceUnavailable,
			errType:    "server_error",
			message:    err.Error(),
			retryAfter: "1",
		}
	case copilot.IsTimeout(err):
		return clientErrorResponse{
			statusCode: http.StatusGatewayTimeout,
			errType:    "server_error",
			message:    err.Error(),
		}
	default:
		return clientErrorResponse{
			statusCode: http.StatusBadGateway,
			errType:    "server_error",
			message:    err.Error(),
		}
	}
}

func writeClientError(w http.ResponseWriter, resp clientErrorResponse) bool {
	if resp.swallow {
		return false
	}
	if resp.retryAfter != "" {
		w.Header().Set("Retry-After", resp.retryAfter)
	}
	WriteError(w, resp.statusCode, resp.errType, resp.message)
	return true
}

func writeStreamDone(sse *SSEWriter, completionID string, created int64, model string) {
	doneReason := "stop"
	sse.WriteDone(ChatCompletionChunk{
		ID:      completionID,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
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
