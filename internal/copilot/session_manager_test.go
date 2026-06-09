package copilot

import (
	"context"
	"testing"
)

func TestGetOrCreateSession_returnsConnectedSession_whenStartSucceeds(t *testing.T) {
	t.Parallel()

	upstream := newFakeCopilotUpstream(t, fakeCopilotScenario{
		conversationID: "conv-baseline",
		messageID:      "msg-baseline",
		appendTexts:    []string{"ok"},
		expectSend:     false,
	})

	client := upstream.newClient(t)

	session, err := client.getOrCreateSession(context.Background())
	if err != nil {
		t.Fatalf("getOrCreateSession() error = %v", err)
	}

	if session.ConversationID != "conv-baseline" {
		t.Fatalf("conversationID = %q, want %q", session.ConversationID, "conv-baseline")
	}
	if !session.IsConnected() {
		t.Fatal("session.IsConnected() = false, want true")
	}
	if session.Conn == nil {
		t.Fatal("session.Conn = nil, want websocket connection")
	}

	client.InvalidateSession(session.ConversationID)
}
