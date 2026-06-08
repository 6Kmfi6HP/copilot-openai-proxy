package openai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// SSEWriter writes Server-Sent Events to an http.ResponseWriter.
type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// NewSSEWriter creates an SSE writer.
func NewSSEWriter(w http.ResponseWriter, flusher http.Flusher) *SSEWriter {
	return &SSEWriter{w: w, flusher: flusher}
}

// WriteJSON writes a single SSE event with JSON-encoded data.
func (s *SSEWriter) WriteJSON(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	fmt.Fprintf(s.w, "data: %s\n\n", data)
	s.flusher.Flush()
}

// WriteDone sends the final "data: [DONE]" SSE event.
func (s *SSEWriter) WriteDone(final ...ChatCompletionChunk) {
	if len(final) > 0 {
		s.WriteJSON(final[0])
	}
	fmt.Fprint(s.w, "data: [DONE]\n\n")
	s.flusher.Flush()
}

// WriteError writes an OpenAI-style error response.
func WriteError(w http.ResponseWriter, statusCode int, errType, message string) {
	writeJSON(w, statusCode, ErrorResponse{
		Error: ErrorObject{
			Message: message,
			Type:    errType,
			Param:   nil,
			Code:    nil,
		},
	})
}

func writeJSON(w http.ResponseWriter, statusCode int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(v)
}

// Ensure strings is imported for WriteError.
var _ = strings.TrimSpace