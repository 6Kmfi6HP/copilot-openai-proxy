package copilot

import (
	"fmt"
	"strings"
)

// UpstreamError represents an error returned by the Copilot upstream.
type UpstreamError struct {
	Message string
}

func (e *UpstreamError) Error() string {
	return e.Message
}

func upstreamErrorf(format string, args ...any) *UpstreamError {
	return &UpstreamError{Message: fmt.Sprintf(format, args...)}
}

// authenticationError is returned when the user session is blocked.
func authenticationError(msg string) *UpstreamError {
	return &UpstreamError{Message: msg}
}

// IsBlocked checks if the error indicates a blocked user.
func IsBlocked(err error) bool {
	if ue, ok := err.(*UpstreamError); ok {
		return strings.Contains(ue.Message, "blocked")
	}
	return false
}