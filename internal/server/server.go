package server

import (
	"net/http"

	"copilot-openai-proxy/internal/config"
	"copilot-openai-proxy/internal/openai"
)

// New creates an http.Server with routes and middleware.
func New(handler *openai.Handler, cfg *config.Config) *http.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/chat/completions", handler.ChatCompletions)
	mux.HandleFunc("/v1/models", handler.ListModels)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		openai.WriteHealth(w)
	})

	var h http.Handler = mux
	if cfg.APIKey != "" {
		h = authMiddleware(h, cfg.APIKey)
	}

	return &http.Server{
		Addr:    cfg.Addr,
		Handler: h,
	}
}
