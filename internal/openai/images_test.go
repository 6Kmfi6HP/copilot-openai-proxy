package openai

import (
	"strings"
	"testing"
)

func Test_parseDataImageURL_acceptsPNGAndJPEG(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		wantMIME string
	}{
		{
			name:     "png",
			url:      "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==",
			wantMIME: "image/png",
		},
		{
			name:     "jpeg alias jpg",
			url:      "data:image/jpg;base64,/9j/4AAQ",
			wantMIME: "image/jpeg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDataImageURL(tt.url)
			if err != nil {
				t.Fatalf("parseDataImageURL() error = %v", err)
			}
			if got.MIME != tt.wantMIME {
				t.Fatalf("MIME = %q, want %q", got.MIME, tt.wantMIME)
			}
			if len(got.Data) == 0 {
				t.Fatal("Data is empty")
			}
		})
	}
}

func Test_parseDataImageURL_rejectsUnsupported(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "https", url: "https://example.com/a.png", want: "data:image"},
		{name: "gif", url: "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7", want: "unsupported image mime"},
		{name: "missing base64", url: "data:image/png,AAAA", want: "base64"},
		{name: "empty", url: "", want: "required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseDataImageURL(tt.url)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.want)
			}
		})
	}
}

func Test_buildCompletionPayload_limitsImageCount(t *testing.T) {
	png := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	parts := make([]interface{}, 0, maxImagesPerRequest+2)
	parts = append(parts, map[string]interface{}{"type": "text", "text": "look"})
	for i := 0; i < maxImagesPerRequest+1; i++ {
		parts = append(parts, map[string]interface{}{
			"type":      "image_url",
			"image_url": map[string]interface{}{"url": png},
		})
	}
	_, err := buildCompletionPayload(ChatCompletionRequest{
		Messages: []ChatMessage{{Role: "user", Content: parts}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "at most") {
		t.Fatalf("error = %q", err.Error())
	}
}
