package openai

import (
	"net/http"
)

// ModelsResponse mirrors the OpenAI models list response.
type ModelsResponse struct {
	Object string      `json:"object"`
	Data   []ModelInfo `json:"data"`
}

// ModelInfo describes a single model.
type ModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// WriteModels returns the list of available models.
// The proxy exposes Copilot's conversation modes as OpenAI-compatible model names.
func WriteModels(w http.ResponseWriter) {
	models := []ModelInfo{
		{
			ID:      "smart",
			Object:  "model",
			Created: 0,
			OwnedBy: "copilot",
		},
		{
			ID:      "creative",
			Object:  "model",
			Created: 0,
			OwnedBy: "copilot",
		},
		{
			ID:      "balanced",
			Object:  "model",
			Created: 0,
			OwnedBy: "copilot",
		},
		{
			ID:      "precise",
			Object:  "model",
			Created: 0,
			OwnedBy: "copilot",
		},
	}
	writeJSON(w, http.StatusOK, ModelsResponse{
		Object: "list",
		Data:   models,
	})
}

// HealthResponse is returned by the health check endpoint.
type HealthResponse struct {
	Status string `json:"status"`
}

// WriteHealth returns a simple health check response.
func WriteHealth(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, HealthResponse{Status: "ok"})
}