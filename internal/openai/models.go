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
// Model IDs are Copilot conversation modes discovered from the upstream catalog.
func WriteModels(w http.ResponseWriter, models []string) {
	if len(models) == 0 {
		models = []string{"smart"}
	}
	data := make([]ModelInfo, 0, len(models))
	for _, id := range models {
		if id == "" {
			continue
		}
		data = append(data, ModelInfo{
			ID:      id,
			Object:  "model",
			Created: 0,
			OwnedBy: "copilot",
		})
	}
	if len(data) == 0 {
		data = []ModelInfo{{
			ID:      "smart",
			Object:  "model",
			Created: 0,
			OwnedBy: "copilot",
		}}
	}
	writeJSON(w, http.StatusOK, ModelsResponse{
		Object: "list",
		Data:   data,
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
