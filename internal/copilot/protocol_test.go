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
