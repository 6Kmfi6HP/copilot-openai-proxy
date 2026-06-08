package server

import (
	"fmt"
	"net/http"
	"strings"
)

// authMiddleware validates the Authorization header against the configured API key.
// When api-key is set, requests must include "Authorization: Bearer <key>".
func authMiddleware(next http.Handler, apiKey string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for health check.
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			WriteErrorJSON(w, http.StatusUnauthorized, "authentication_error", "invalid API key")
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") || parts[1] != apiKey {
			WriteErrorJSON(w, http.StatusUnauthorized, "authentication_error", "invalid API key")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func WriteErrorJSON(w http.ResponseWriter, statusCode int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	fmt.Fprintf(w, `{"error":{"message":%q,"type":%q,"param":null,"code":null}}`,
		message, errType)
}