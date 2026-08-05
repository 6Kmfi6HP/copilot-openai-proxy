package copilot

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestClientIntegration_Complete_returnsFullText_when_fakeUpstreamStreamsAppendText(t *testing.T) {
	t.Parallel()

	upstream := newFakeCopilotUpstream(t, fakeCopilotScenario{
		conversationID: "conv-complete",
		messageID:      "msg-complete",
		appendTexts:    []string{"hello", " world"},
		expectSend:     true,
	})

	client := upstream.newClient(t)

	text, messageID, err := client.Complete(context.Background(), CompletionInput{
		Prompt: "say hello",
		Mode:   "smart",
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if text != "hello world" {
		t.Fatalf("Complete() text = %q, want %q", text, "hello world")
	}
	if messageID != "msg-complete" {
		t.Fatalf("Complete() messageID = %q, want %q", messageID, "msg-complete")
	}

	startReq := upstream.lastStartRequest()
	if startReq.TimeZone != "UTC" {
		t.Fatalf("start request timezone = %q, want %q", startReq.TimeZone, "UTC")
	}
	if !startReq.StartNewConversation {
		t.Fatal("start request StartNewConversation = false, want true")
	}

	outboundEvents := upstream.recordedOutboundEvents()
	if !reflect.DeepEqual(outboundEvents, []string{"setOptions", "send"}) {
		t.Fatalf("outbound events = %v, want %v", outboundEvents, []string{"setOptions", "send"})
	}

	send := upstream.lastSendMessage()
	if send.ConversationID != "conv-complete" {
		t.Fatalf("send conversationID = %q, want %q", send.ConversationID, "conv-complete")
	}
	if got := send.Content[0].Text; got != "say hello" {
		t.Fatalf("send prompt = %q, want %q", got, "say hello")
	}
}

func TestClientIntegration_Complete_embedsImageMarkdown_whenUpstreamGeneratesImage(t *testing.T) {
	t.Parallel()

	imageURL := "https://copilot.microsoft.com/th/id/BCO.test-image.png"
	upstream := newFakeCopilotUpstream(t, fakeCopilotScenario{
		conversationID: "conv-image",
		messageID:      "msg-image",
		imageURL:       imageURL,
		appendTexts:    []string{"Here is your image."},
		expectSend:     true,
	})

	client := upstream.newClient(t)

	text, messageID, err := client.Complete(context.Background(), CompletionInput{
		Prompt: "generate an apple",
		Mode:   "creative",
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if messageID != "msg-image" {
		t.Fatalf("messageID = %q, want %q", messageID, "msg-image")
	}

	want := ImageMarkdown(imageURL) + "Here is your image."
	if text != want {
		t.Fatalf("Complete() text = %q, want %q", text, want)
	}
}

func TestClientIntegration_StreamEvents_emitsCurrentEventSequence_when_fakeUpstreamResponds(t *testing.T) {
	t.Parallel()

	upstream := newFakeCopilotUpstream(t, fakeCopilotScenario{
		conversationID: "conv-stream",
		messageID:      "msg-stream",
		appendTexts:    []string{"stream", "ed"},
		expectSend:     true,
	})

	client := upstream.newClient(t)

	events, err := client.StreamEvents(context.Background(), CompletionInput{
		Prompt: "stream this",
		Mode:   "precise",
	})
	if err != nil {
		t.Fatalf("StreamEvents() error = %v", err)
	}

	gotEvents := collectStreamEvents(t, events)
	gotTypes := make([]StreamEventType, 0, len(gotEvents))
	for _, evt := range gotEvents {
		gotTypes = append(gotTypes, evt.Type)
	}

	wantTypes := []StreamEventType{
		EventStartMessage,
		EventAppendText,
		EventAppendText,
		EventDone,
	}
	if !reflect.DeepEqual(gotTypes, wantTypes) {
		t.Fatalf("event types = %v, want %v", gotTypes, wantTypes)
	}

	if gotEvents[0].ConversationID != "conv-stream" {
		t.Fatalf("start event conversationID = %q, want %q", gotEvents[0].ConversationID, "conv-stream")
	}
	if gotEvents[0].MessageID != "msg-stream" {
		t.Fatalf("start event messageID = %q, want %q", gotEvents[0].MessageID, "msg-stream")
	}
	if gotEvents[1].Text != "stream" || gotEvents[2].Text != "ed" {
		t.Fatalf("append texts = %q/%q, want %q/%q", gotEvents[1].Text, gotEvents[2].Text, "stream", "ed")
	}
}

func TestClientIntegration_StartDelayHonorsContextDeadline(t *testing.T) {
	t.Parallel()

	upstream := newFakeCopilotUpstream(t, fakeCopilotScenario{
		conversationID: "conv-delayed",
		expectSend:     true,
		startDelay:     200 * time.Millisecond,
	})

	client := upstream.newClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, _, err := client.Complete(ctx, CompletionInput{
		Prompt: "will timeout",
		Mode:   "smart",
	})
	if err == nil {
		t.Fatal("Complete() error = nil, want context deadline exceeded")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Complete() error = %v, want %v", err, context.DeadlineExceeded)
	}
	if got := len(upstream.recordedOutboundEvents()); got != 0 {
		t.Fatalf("outbound event count = %d, want 0", got)
	}
}

func TestComplete_HonorsRequestContextCancellation(t *testing.T) {
	t.Parallel()

	upstream := newFakeCopilotUpstream(t, fakeCopilotScenario{
		conversationID: "conv-cancel",
		messageID:      "msg-cancel",
		expectSend:     true,
		holdChatOpen:   true,
	})

	client := upstream.newClient(t)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	go func() {
		_, _, err := client.Complete(ctx, CompletionInput{
			Prompt: "block until canceled",
			Mode:   "smart",
		})
		errCh <- err
	}()

	upstream.waitForSendObserved(t)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Complete() error = %v, want %v", err, context.Canceled)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Complete() cancellation")
	}
}

func TestStreamEvents_RecoversFromClosedWarmSessionBeforeSend(t *testing.T) {
	t.Parallel()

	upstream := newFakeCopilotUpstream(t, fakeCopilotScenario{
		conversationID:       "conv-recovered",
		messageID:            "msg-recovered",
		appendTexts:          []string{"fresh"},
		expectSend:           true,
		idleCloseAfter:       20 * time.Millisecond,
		closeBeforeSendCount: 1,
	})

	client := upstream.newClientWithConfig(t, ClientConfig{
		MaxSessions:    2,
		WarmSessions:   1,
		SessionTTL:     time.Minute,
		CleanupInt:     time.Minute,
		ConnTimeout:    time.Second,
		Timeout:        time.Second,
		WSReadTimeout:  time.Minute,
		WSWriteTimeout: time.Second,
		WSPingInterval: 25 * time.Second,
		Debug:          false,
		TimeZone:       "UTC",
	})

	waitForSessionSnapshot(t, client.sessionMgr, sessionSnapshot{total: 1, idle: 1})
	time.Sleep(50 * time.Millisecond)

	events, err := client.StreamEvents(context.Background(), CompletionInput{
		Prompt: "recover warm session",
		Mode:   "smart",
	})
	if err != nil {
		t.Fatalf("StreamEvents() error = %v", err)
	}

	gotEvents := collectStreamEvents(t, events)
	if len(gotEvents) == 0 {
		t.Fatal("event count = 0, want streamed events after retrying a fresh session")
	}
	if gotEvents[0].Type != EventStartMessage {
		t.Fatalf("first event type = %v, want %v", gotEvents[0].Type, EventStartMessage)
	}
	if gotEvents[1].Type != EventAppendText || gotEvents[1].Text != "fresh" {
		t.Fatalf("append event = %+v, want fresh appendText", gotEvents[1])
	}
}

func collectStreamEvents(t *testing.T, events <-chan StreamEvent) []StreamEvent {
	t.Helper()

	var collected []StreamEvent
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()

	for {
		select {
		case evt, ok := <-events:
			if !ok {
				return collected
			}
			collected = append(collected, evt)
		case <-timeout.C:
			t.Fatal("timed out waiting for stream events")
		}
	}
}
