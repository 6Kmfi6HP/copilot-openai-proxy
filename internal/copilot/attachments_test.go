package copilot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func Test_attachmentsURLFromStart_derivesFromStartPath(t *testing.T) {
	got := attachmentsURLFromStart("https://example.test/c/api/start")
	want := "https://example.test/c/api/attachments"
	if got != want {
		t.Fatalf("attachmentsURLFromStart() = %q, want %q", got, want)
	}
	if got := attachmentsURLFromStart("https://other.test/x"); got != defaultCopilotAttachmentsURL {
		t.Fatalf("fallback = %q, want %q", got, defaultCopilotAttachmentsURL)
	}
}

func TestClient_UploadImage_andComplete_withVisionContent(t *testing.T) {
	upstream := newFakeCopilotUpstream(t, fakeCopilotScenario{
		conversationID: "conv-vision",
		messageID:      "msg-vision",
		appendTexts:    []string{"It is red."},
		expectSend:     true,
	})
	client := upstream.newClient(t)

	png := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde, 0x00, 0x00, 0x00,
		0x0c, 0x49, 0x44, 0x41, 0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x50,
		0x0f, 0x00, 0x04, 0x85, 0x01, 0x80, 0xa4, 0xa9, 0x8c, 0x21, 0x00, 0x00,
		0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
	}

	text, messageID, err := client.Complete(context.Background(), CompletionInput{
		Prompt: "What color?",
		Mode:   "smart",
		Images: []ImageInput{{MIME: "image/png", Data: png}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if messageID != "msg-vision" {
		t.Fatalf("messageID = %q, want msg-vision", messageID)
	}
	if text != "It is red." {
		t.Fatalf("text = %q, want %q", text, "It is red.")
	}

	uploads := upstream.recordedAttachments()
	if len(uploads) != 1 {
		t.Fatalf("attachment uploads = %d, want 1", len(uploads))
	}
	if uploads[0].ContentType != "image/png" {
		t.Fatalf("content-type = %q, want image/png", uploads[0].ContentType)
	}
	if string(uploads[0].Body) != string(png) {
		t.Fatalf("uploaded body mismatch")
	}

	upstream.waitForSendObserved(t)
	send := upstream.lastSendMessage()
	if len(send.Content) != 2 {
		t.Fatalf("send content parts = %d, want 2: %+v", len(send.Content), send.Content)
	}
	if send.Content[0].Type != "image" || send.Content[0].URL != "/attachments/att-1.png" {
		t.Fatalf("image part = %+v", send.Content[0])
	}
	if send.Content[1].Type != "text" || send.Content[1].Text != "What color?" {
		t.Fatalf("text part = %+v", send.Content[1])
	}
}

func TestClient_UploadImage_mapsNonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/c/api/attachments" {
			http.Error(w, "nope", http.StatusUnauthorized)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{
		MaxSessions:    1,
		WarmSessions:   0,
		Timeout:        time.Second,
		AttachmentsURL: server.URL + "/c/api/attachments",
		StartURL:       server.URL + "/c/api/start",
		WSURL:          "ws" + strings.TrimPrefix(server.URL, "http") + "/c/api/chat",
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })

	_, err = client.UploadImage(context.Background(), nil, "image/png", []byte{1, 2, 3})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("error = %q, want status 401", err.Error())
	}
}
