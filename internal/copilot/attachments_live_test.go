package copilot

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Live probe: anonymous cookie can upload to /c/api/attachments and vision
// answers over the WebSocket send path. Skipped unless COPILOT_VISION_LIVE=1.
func TestLiveAnonVision_UploadAndDescribe(t *testing.T) {
	if os.Getenv("COPILOT_VISION_LIVE") != "1" {
		t.Skip("set COPILOT_VISION_LIVE=1 to run live anonymous vision probe")
	}

	client, err := NewClient(ClientConfig{
		MaxSessions:    1,
		WarmSessions:   0,
		ConnTimeout:    20 * time.Second,
		Timeout:        60 * time.Second,
		WSReadTimeout:  60 * time.Second,
		WSWriteTimeout: 10 * time.Second,
		WSPingInterval: 25 * time.Second,
		TimeZone:       "UTC",
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })

	// 1x1 red PNG
	png := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde, 0x00, 0x00, 0x00,
		0x0c, 0x49, 0x44, 0x41, 0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x50,
		0x0f, 0x00, 0x04, 0x85, 0x01, 0x80, 0xa4, 0xa9, 0x8c, 0x21, 0x00, 0x00,
		0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
	}

	text, _, err := client.Complete(context.Background(), CompletionInput{
		Prompt: "What color is this image? Reply in one short sentence.",
		Mode:   "smart",
		Images: []ImageInput{{MIME: "image/png", Data: png}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "red") {
		t.Fatalf("vision reply = %q, want mention of red", text)
	}
}
