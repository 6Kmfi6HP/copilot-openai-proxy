package util

import (
	"fmt"

	"github.com/google/uuid"
)

// OpenAIChatCompletionID generates a completion ID in the format
// "chatcmpl-" + UUID, matching OpenAI's convention.
func OpenAIChatCompletionID() string {
	return fmt.Sprintf("chatcmpl-%s", uuid.New().String())
}