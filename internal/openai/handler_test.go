package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"copilot-openai-proxy/internal/copilot"
)

type fakeChatClient struct {
	input        copilot.CompletionInput
	completeText string
	completeErr  error
	streamEvents <-chan copilot.StreamEvent
	streamErr    error
}

func (f *fakeChatClient) Complete(_ context.Context, input copilot.CompletionInput) (string, string, error) {
	f.input = input
	return f.completeText, "", f.completeErr
}

func (f *fakeChatClient) StreamEvents(_ context.Context, input copilot.CompletionInput) (<-chan copilot.StreamEvent, error) {
	f.input = input
	if f.streamEvents == nil {
		events := make(chan copilot.StreamEvent)
		close(events)
		return events, f.streamErr
	}
	return f.streamEvents, f.streamErr
}

func Test_modeForModel_usesSmartUpstreamMode_whenPublicModelVaries(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  string
	}{
		{name: "default empty model", model: "", want: "smart"},
		{name: "smart model", model: "smart", want: "smart"},
		{name: "creative model", model: "creative", want: "creative"},
		{name: "balanced model", model: "balanced", want: "balanced"},
		{name: "precise model", model: "precise", want: "precise"},
		{name: "model is normalized", model: " Creative ", want: "creative"},
		{name: "unknown model", model: "gpt-4", want: "smart"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := modeForModel(tt.model)

			if got != tt.want {
				t.Fatalf("modeForModel(%q) = %q, want %q", tt.model, got, tt.want)
			}
		})
	}
}

func TestChatCompletions_usesSelectedModeAndResponseModel(t *testing.T) {
	tests := []struct {
		name          string
		requestModel  string
		wantMode      string
		wantRespModel string
	}{
		{name: "creative model", requestModel: "creative", wantMode: "creative", wantRespModel: "creative"},
		{name: "balanced model", requestModel: "balanced", wantMode: "balanced", wantRespModel: "balanced"},
		{name: "precise model", requestModel: "precise", wantMode: "precise", wantRespModel: "precise"},
		{name: "normalized model", requestModel: " Creative ", wantMode: "creative", wantRespModel: "creative"},
		{name: "unknown model falls back", requestModel: "gpt-4", wantMode: "smart", wantRespModel: "smart"},
		{name: "missing model defaults", requestModel: "", wantMode: "smart", wantRespModel: "smart"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeChatClient{completeText: "ok"}
			handler := &Handler{client: fake}

			body := `{"model":"` + tt.requestModel + `","messages":[{"role":"user","content":"hello"}]}`
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.ChatCompletions(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
			}
			if fake.input.Mode != tt.wantMode {
				t.Fatalf("upstream mode = %q, want %q", fake.input.Mode, tt.wantMode)
			}
			if fake.input.StreamModel != tt.wantRespModel {
				t.Fatalf("response model input = %q, want %q", fake.input.StreamModel, tt.wantRespModel)
			}

			var resp ChatCompletionResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal response: %v", err)
			}
			if resp.Model != tt.wantRespModel {
				t.Fatalf("response model = %q, want %q", resp.Model, tt.wantRespModel)
			}
		})
	}
}

func TestChatCompletions_StreamResponseUsesSelectedModel(t *testing.T) {
	events := make(chan copilot.StreamEvent, 2)
	events <- copilot.StreamEvent{Type: copilot.EventAppendText, Text: "hello"}
	events <- copilot.StreamEvent{Type: copilot.EventDone}
	close(events)

	fake := &fakeChatClient{streamEvents: events}
	handler := &Handler{client: fake}

	body := `{"model":"precise","messages":[{"role":"user","content":"hello"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if fake.input.Mode != "precise" {
		t.Fatalf("upstream mode = %q, want %q", fake.input.Mode, "precise")
	}
	if !strings.Contains(rec.Body.String(), `"model":"precise"`) {
		t.Fatalf("stream response did not echo precise model: %s", rec.Body.String())
	}
}
