package copilot

import "testing"

func Test_parseServerEvent_ignoresAcknowledgementEvents_whenCompletionStillStreaming(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "received acknowledgement",
			raw:  `{"event":"received","conversationId":"c1","messageId":"m1","id":"1"}`,
		},
		{
			name: "part completed acknowledgement",
			raw:  `{"event":"partCompleted","messageId":"m1","partId":"p1","id":"6"}`,
		},
		{
			name: "unknown non terminal event",
			raw:  `{"event":"telemetry","id":"7"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseServerEvent([]byte(tt.raw))

			if err != nil {
				t.Fatalf("parseServerEvent returned error: %v", err)
			}
			if got.Type != EventIgnore {
				t.Fatalf("event type = %v, want EventIgnore", got.Type)
			}
		})
	}
}

func Test_parseServerEvent_usesErrorCode_whenErrorBodyIsEmpty(t *testing.T) {
	raw := `{"event":"error","errorCode":"invalid-event","id":"0.0002"}`

	got, err := parseServerEvent([]byte(raw))

	if err != nil {
		t.Fatalf("parseServerEvent returned error: %v", err)
	}
	if got.Type != EventError {
		t.Fatalf("event type = %v, want EventError", got.Type)
	}
	if got.Err == nil || got.Err.Error() != "upstream_error: invalid-event" {
		t.Fatalf("error = %v, want upstream_error: invalid-event", got.Err)
	}
}

func Test_parseServerEvent_imageGenerated(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantType StreamEventType
		wantURL  string
	}{
		{
			name: "final image url",
			raw: `{
				"event":"imageGenerated",
				"messageId":"AyrcyqSg3Rc8HqpzbL1u3",
				"partId":"DEGVe5ihRQZsTpQnMNztR",
				"url":"https://copilot.microsoft.com/th/id/BCO.42a11350-302d-46b2-909f-43b45f2b15d5.png",
				"thumbnailUrl":"https://copilot.microsoft.com/th/id/BCO.42a11350-302d-46b2-909f-43b45f2b15d5.png/?w=270&qlt=90",
				"id":"14",
				"nextAvailableAt":null
			}`,
			wantType: EventImageGenerated,
			wantURL:  "https://copilot.microsoft.com/th/id/BCO.42a11350-302d-46b2-909f-43b45f2b15d5.png",
		},
		{
			name:     "empty url ignored",
			raw:      `{"event":"imageGenerated","messageId":"m1","partId":"p1","url":"","id":"14"}`,
			wantType: EventIgnore,
		},
		{
			name:     "generatingImage ignored",
			raw:      `{"event":"generatingImage","messageId":"m1","partId":"p1","prompt":"Red apple on table","progressionText":"Bringing a crisp apple to life...","id":"11"}`,
			wantType: EventIgnore,
		},
		{
			name:     "partialImageGenerated ignored",
			raw:      `{"event":"partialImageGenerated","messageId":"m1","partId":"p1","content":"/9j/4AAQ","id":"12"}`,
			wantType: EventIgnore,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseServerEvent([]byte(tt.raw))
			if err != nil {
				t.Fatalf("parseServerEvent returned error: %v", err)
			}
			if got.Type != tt.wantType {
				t.Fatalf("event type = %v, want %v", got.Type, tt.wantType)
			}
			if got.ImageURL != tt.wantURL {
				t.Fatalf("ImageURL = %q, want %q", got.ImageURL, tt.wantURL)
			}
		})
	}
}

func TestImageMarkdown(t *testing.T) {
	if got := ImageMarkdown(""); got != "" {
		t.Fatalf("ImageMarkdown(empty) = %q, want empty", got)
	}
	want := "\n![image](https://example.com/a.png)\n"
	if got := ImageMarkdown("https://example.com/a.png"); got != want {
		t.Fatalf("ImageMarkdown = %q, want %q", got, want)
	}
}
