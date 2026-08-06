package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"copilot-openai-proxy/internal/copilot"
)

type fakeChatClient struct {
	input         copilot.CompletionInput
	completeText  string
	completeErr   error
	completeCalls int
	completeFunc  func(context.Context, copilot.CompletionInput) (string, string, error)
	streamEvents  <-chan copilot.StreamEvent
	streamErr     error
	streamCalls   int
	streamFunc    func(context.Context, copilot.CompletionInput) (<-chan copilot.StreamEvent, error)
	models        []string
	modelsErr     error
	modelsCalls   int
}

func (f *fakeChatClient) ListModels(ctx context.Context) ([]string, error) {
	f.modelsCalls++
	if f.modelsErr != nil {
		return nil, f.modelsErr
	}
	if f.models != nil {
		return append([]string(nil), f.models...), nil
	}
	return []string{"smart"}, nil
}

func (f *fakeChatClient) Complete(ctx context.Context, input copilot.CompletionInput) (string, string, error) {
	f.completeCalls++
	f.input = input
	if f.completeFunc != nil {
		return f.completeFunc(ctx, input)
	}
	return f.completeText, "", f.completeErr
}

func (f *fakeChatClient) StreamEvents(ctx context.Context, input copilot.CompletionInput) (<-chan copilot.StreamEvent, error) {
	f.streamCalls++
	f.input = input
	if f.streamFunc != nil {
		return f.streamFunc(ctx, input)
	}
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
		{name: "reasoning model passes through", model: "reasoning", want: "reasoning"},
		{name: "coco model passes through", model: "coco", want: "coco"},
		{name: "unknown plausible model passes through", model: "gpt-4", want: "gpt-4"},
		{name: "invalid model falls back", model: "bad model!", want: "smart"},
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

func TestChatCompletions_buildsPromptFromMixedMessageContent_whenRequestIsNonStreaming(t *testing.T) {
	fake := &fakeChatClient{completeText: "completion"}
	handler := &Handler{client: fake}

	// 1x1 PNG
	dataURI := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	body := `{"model":"creative","messages":[{"role":"system","content":"Guardrails"},{"role":"user","content":[{"type":"text","text":"First line"},{"type":"image_url","image_url":{"url":"` + dataURI + `"}},{"type":"text","text":"Second line"}]},{"role":"assistant","content":"Earlier answer"},{"role":"user","content":"Final question"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if fake.completeCalls != 1 {
		t.Fatalf("complete calls = %d, want %d", fake.completeCalls, 1)
	}
	if fake.streamCalls != 0 {
		t.Fatalf("stream calls = %d, want %d", fake.streamCalls, 0)
	}

	wantPrompt := "[System] Guardrails\n\nFirst line\nSecond line\n[Assistant] Earlier answer\nFinal question\n"
	if fake.input.Prompt != wantPrompt {
		t.Fatalf("prompt = %q, want %q", fake.input.Prompt, wantPrompt)
	}
	if fake.input.Mode != "creative" {
		t.Fatalf("mode = %q, want %q", fake.input.Mode, "creative")
	}
	if len(fake.input.Images) != 1 {
		t.Fatalf("images = %d, want 1", len(fake.input.Images))
	}
	if fake.input.Images[0].MIME != "image/png" {
		t.Fatalf("image mime = %q, want image/png", fake.input.Images[0].MIME)
	}
	if len(fake.input.Images[0].Data) == 0 {
		t.Fatal("image data is empty")
	}

	var resp ChatCompletionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got := resp.Choices[0].Message.Content; got != "completion" {
		t.Fatalf("response content = %q, want %q", got, "completion")
	}
}

func TestChatCompletions_rejectsExternalImageURL(t *testing.T) {
	fake := &fakeChatClient{completeText: "completion"}
	handler := &Handler{client: fake}

	body := `{"model":"smart","messages":[{"role":"user","content":[{"type":"text","text":"What is this?"},{"type":"image_url","image_url":{"url":"https://example.com/image.png"}}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.ChatCompletions(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if fake.completeCalls != 0 {
		t.Fatalf("complete calls = %d, want 0", fake.completeCalls)
	}
	if !strings.Contains(rec.Body.String(), "data:image") {
		t.Fatalf("error body = %s, want mention of data:image", rec.Body.String())
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
		{name: "reasoning model", requestModel: "reasoning", wantMode: "reasoning", wantRespModel: "reasoning"},
		{name: "unknown plausible model passes through", requestModel: "gpt-4", wantMode: "gpt-4", wantRespModel: "gpt-4"},
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

func TestListModels_returnsCatalogFromClient(t *testing.T) {
	fake := &fakeChatClient{models: []string{"smart", "reasoning", "coco", "search"}}
	handler := &Handler{client: fake}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ListModels(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if fake.modelsCalls != 1 {
		t.Fatalf("ListModels calls = %d, want 1", fake.modelsCalls)
	}
	var resp ModelsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Object != "list" {
		t.Fatalf("object = %q, want list", resp.Object)
	}
	if len(resp.Data) != 4 {
		t.Fatalf("data len = %d, want 4: %#v", len(resp.Data), resp.Data)
	}
	if resp.Data[0].ID != "smart" || resp.Data[1].ID != "reasoning" || resp.Data[2].ID != "coco" {
		t.Fatalf("unexpected models: %#v", resp.Data)
	}
}

func TestListModels_fallsBackToSmartOnCatalogError(t *testing.T) {
	fake := &fakeChatClient{modelsErr: context.DeadlineExceeded}
	handler := &Handler{client: fake}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ListModels(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp ModelsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].ID != "smart" {
		t.Fatalf("fallback models = %#v, want [smart]", resp.Data)
	}
}

func TestChatCompletions_rejectsToolCallingFields_whenPresent(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "tools field",
			body: `{"messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function"}]}`,
		},
		{
			name: "tool choice field",
			body: `{"messages":[{"role":"user","content":"hello"}],"tool_choice":"auto"}`,
		},
		{
			name: "function call field",
			body: `{"messages":[{"role":"user","content":"hello"}],"function_call":{"name":"lookup"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeChatClient{completeText: "unused"}
			handler := &Handler{client: fake}

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.ChatCompletions(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if fake.completeCalls != 0 {
				t.Fatalf("complete calls = %d, want %d", fake.completeCalls, 0)
			}
			if fake.streamCalls != 0 {
				t.Fatalf("stream calls = %d, want %d", fake.streamCalls, 0)
			}

			var resp ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal error response: %v", err)
			}
			if got := resp.Error.Type; got != "invalid_request_error" {
				t.Fatalf("error type = %q, want %q", got, "invalid_request_error")
			}
			if got := resp.Error.Message; got != "tools/function calling is not supported" {
				t.Fatalf("error message = %q, want %q", got, "tools/function calling is not supported")
			}
		})
	}
}

func TestChatCompletions_Returns403WhenUpstreamIdentityBlocked(t *testing.T) {
	fake := &fakeChatClient{completeErr: &copilot.UpstreamError{Message: "blocked upstream identity"}}
	handler := &Handler{client: fake}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ChatCompletions(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "" {
		t.Fatalf("Retry-After = %q, want empty", got)
	}

	var resp ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if got := resp.Error.Type; got != "authentication_error" {
		t.Fatalf("error type = %q, want %q", got, "authentication_error")
	}
	if got := resp.Error.Message; got != "blocked upstream identity" {
		t.Fatalf("error message = %q, want %q", got, "blocked upstream identity")
	}
}

func TestChatCompletions_Returns503WhenSessionCapacityExhausted(t *testing.T) {
	fake := &fakeChatClient{completeErr: copilot.NewCapacityError("session capacity exhausted", context.DeadlineExceeded)}
	handler := &Handler{client: fake}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ChatCompletions(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want %q", got, "1")
	}

	var resp ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if got := resp.Error.Type; got != "server_error" {
		t.Fatalf("error type = %q, want %q", got, "server_error")
	}
}

func TestChatCompletions_Returns503WhenUpstreamReportsTooManyMessages(t *testing.T) {
	fake := &fakeChatClient{completeErr: &copilot.UpstreamError{Message: "upstream_error: too-many-messages"}}
	handler := &Handler{client: fake}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ChatCompletions(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want %q", got, "1")
	}
}

func TestChatCompletions_Returns504OnUpstreamTimeout(t *testing.T) {
	fake := &fakeChatClient{completeErr: copilot.NewTimeoutError("start", context.DeadlineExceeded)}
	handler := &Handler{client: fake}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ChatCompletions(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusGatewayTimeout, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "" {
		t.Fatalf("Retry-After = %q, want empty", got)
	}
}

func TestChatCompletions_StreamStartupReturnsMappedErrorResponses(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		wantStatus     int
		wantType       string
		wantRetryAfter string
	}{
		{
			name:       "blocked identity",
			err:        &copilot.UpstreamError{Message: "blocked upstream identity"},
			wantStatus: http.StatusForbidden,
			wantType:   "authentication_error",
		},
		{
			name:           "capacity exhausted",
			err:            copilot.NewCapacityError("session capacity exhausted", context.DeadlineExceeded),
			wantStatus:     http.StatusServiceUnavailable,
			wantType:       "server_error",
			wantRetryAfter: "1",
		},
		{
			name:           "upstream too many messages",
			err:            &copilot.UpstreamError{Message: "upstream_error: too-many-messages"},
			wantStatus:     http.StatusServiceUnavailable,
			wantType:       "server_error",
			wantRetryAfter: "1",
		},
		{
			name:       "upstream timeout",
			err:        copilot.NewTimeoutError("start", context.DeadlineExceeded),
			wantStatus: http.StatusGatewayTimeout,
			wantType:   "server_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeChatClient{streamErr: tt.err}
			handler := &Handler{client: fake}

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"stream":true,"messages":[{"role":"user","content":"hello"}]}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.ChatCompletions(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("content type = %q, want %q", got, "application/json")
			}
			if got := rec.Header().Get("Retry-After"); got != tt.wantRetryAfter {
				t.Fatalf("Retry-After = %q, want %q", got, tt.wantRetryAfter)
			}
			if strings.Contains(rec.Body.String(), "data: ") {
				t.Fatalf("body unexpectedly contains SSE frames: %s", rec.Body.String())
			}

			var resp ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal error response: %v", err)
			}
			if got := resp.Error.Type; got != tt.wantType {
				t.Fatalf("error type = %q, want %q", got, tt.wantType)
			}
			if got := resp.Error.Message; got != tt.err.Error() {
				t.Fatalf("error message = %q, want %q", got, tt.err.Error())
			}
		})
	}
}

func TestChatCompletions_StreamOverloadSurface(t *testing.T) {
	fake := &fakeChatClient{streamErr: copilot.NewCapacityError("session capacity exhausted", context.DeadlineExceeded)}
	handler := &Handler{client: fake}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"stream":true,"messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ChatCompletions(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want %q", got, "application/json")
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want %q", got, "1")
	}
	if strings.Contains(rec.Body.String(), "data: ") {
		t.Fatalf("body unexpectedly contains SSE frames: %s", rec.Body.String())
	}
}

func TestStreamResponse_StopsCleanlyOnClientCancel(t *testing.T) {
	started := make(chan struct{})
	fake := &fakeChatClient{
		streamFunc: func(ctx context.Context, input copilot.CompletionInput) (<-chan copilot.StreamEvent, error) {
			events := make(chan copilot.StreamEvent)
			go func() {
				defer close(events)
				events <- copilot.StreamEvent{Type: copilot.EventAppendText, Text: "hello"}
				close(started)
				<-ctx.Done()
			}()
			return events, nil
		},
	}
	handler := &Handler{client: fake}

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"stream":true,"messages":[{"role":"user","content":"hello"}]}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler.ChatCompletions(rec, req)
		close(done)
	}()

	<-started
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler to stop after client cancel")
	}

	frames := sseDataFrames(t, rec.Body.String())
	if len(frames) < 3 {
		t.Fatalf("frame count = %d, want at least %d; body=%s", len(frames), 3, rec.Body.String())
	}
	if frames[len(frames)-1] != "[DONE]" {
		t.Fatalf("final frame = %q, want %q", frames[len(frames)-1], "[DONE]")
	}
	if strings.Contains(rec.Body.String(), "context canceled") {
		t.Fatalf("body contains bogus cancellation error: %s", rec.Body.String())
	}
}

func TestChatCompletions_StreamResponseUsesSelectedModel(t *testing.T) {
	events := make(chan copilot.StreamEvent, 3)
	events <- copilot.StreamEvent{Type: copilot.EventAppendText, Text: "hello"}
	events <- copilot.StreamEvent{Type: copilot.EventAppendText, Text: " world"}
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
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content type = %q, want %q", got, "text/event-stream")
	}
	if fake.completeCalls != 0 {
		t.Fatalf("complete calls = %d, want %d", fake.completeCalls, 0)
	}
	if fake.streamCalls != 1 {
		t.Fatalf("stream calls = %d, want %d", fake.streamCalls, 1)
	}
	if fake.input.Mode != "precise" {
		t.Fatalf("upstream mode = %q, want %q", fake.input.Mode, "precise")
	}
	if fake.input.Prompt != "hello\n" {
		t.Fatalf("prompt = %q, want %q", fake.input.Prompt, "hello\n")
	}

	frames := sseDataFrames(t, rec.Body.String())
	if len(frames) != 5 {
		t.Fatalf("frame count = %d, want %d; body=%s", len(frames), 5, rec.Body.String())
	}
	if frames[4] != "[DONE]" {
		t.Fatalf("final frame = %q, want %q", frames[4], "[DONE]")
	}

	var initial ChatCompletionChunk
	if err := json.Unmarshal([]byte(frames[0]), &initial); err != nil {
		t.Fatalf("unmarshal initial chunk: %v", err)
	}
	if initial.Model != "precise" {
		t.Fatalf("initial model = %q, want %q", initial.Model, "precise")
	}
	if initial.Choices[0].Delta.Role != "assistant" {
		t.Fatalf("initial role = %q, want %q", initial.Choices[0].Delta.Role, "assistant")
	}

	var firstToken ChatCompletionChunk
	if err := json.Unmarshal([]byte(frames[1]), &firstToken); err != nil {
		t.Fatalf("unmarshal first token chunk: %v", err)
	}
	if firstToken.Choices[0].Delta.Content != "hello" {
		t.Fatalf("first token = %q, want %q", firstToken.Choices[0].Delta.Content, "hello")
	}
	if firstToken.ID != initial.ID {
		t.Fatalf("first token id = %q, want %q", firstToken.ID, initial.ID)
	}

	var secondToken ChatCompletionChunk
	if err := json.Unmarshal([]byte(frames[2]), &secondToken); err != nil {
		t.Fatalf("unmarshal second token chunk: %v", err)
	}
	if secondToken.Choices[0].Delta.Content != " world" {
		t.Fatalf("second token = %q, want %q", secondToken.Choices[0].Delta.Content, " world")
	}
	if secondToken.ID != initial.ID {
		t.Fatalf("second token id = %q, want %q", secondToken.ID, initial.ID)
	}

	var done ChatCompletionChunk
	if err := json.Unmarshal([]byte(frames[3]), &done); err != nil {
		t.Fatalf("unmarshal done chunk: %v", err)
	}
	if done.ID != initial.ID {
		t.Fatalf("done id = %q, want %q", done.ID, initial.ID)
	}
	if done.Choices[0].FinishReason == nil || *done.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish reason = %v, want %q", done.Choices[0].FinishReason, "stop")
	}
}

func TestChatCompletions_StreamResponseEmbedsImageMarkdown(t *testing.T) {
	imageURL := "https://copilot.microsoft.com/th/id/BCO.test-image.png"
	events := make(chan copilot.StreamEvent, 3)
	events <- copilot.StreamEvent{Type: copilot.EventImageGenerated, ImageURL: imageURL}
	events <- copilot.StreamEvent{Type: copilot.EventAppendText, Text: "Here is your image."}
	events <- copilot.StreamEvent{Type: copilot.EventDone}
	close(events)

	fake := &fakeChatClient{streamEvents: events}
	handler := &Handler{client: fake}

	body := `{"model":"creative","messages":[{"role":"user","content":"draw an apple"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	frames := sseDataFrames(t, rec.Body.String())
	// role + image markdown + text + finish + [DONE]
	if len(frames) != 5 {
		t.Fatalf("frame count = %d, want %d; body=%s", len(frames), 5, rec.Body.String())
	}

	var imageChunk ChatCompletionChunk
	if err := json.Unmarshal([]byte(frames[1]), &imageChunk); err != nil {
		t.Fatalf("unmarshal image chunk: %v", err)
	}
	wantImage := copilot.ImageMarkdown(imageURL)
	if imageChunk.Choices[0].Delta.Content != wantImage {
		t.Fatalf("image content = %q, want %q", imageChunk.Choices[0].Delta.Content, wantImage)
	}
	if imageChunk.Model != "creative" {
		t.Fatalf("model = %q, want %q", imageChunk.Model, "creative")
	}

	var textChunk ChatCompletionChunk
	if err := json.Unmarshal([]byte(frames[2]), &textChunk); err != nil {
		t.Fatalf("unmarshal text chunk: %v", err)
	}
	if textChunk.Choices[0].Delta.Content != "Here is your image." {
		t.Fatalf("text content = %q, want %q", textChunk.Choices[0].Delta.Content, "Here is your image.")
	}
}

func sseDataFrames(t *testing.T, body string) []string {
	t.Helper()

	rawFrames := strings.Split(strings.TrimSpace(body), "\n\n")
	frames := make([]string, 0, len(rawFrames))
	for _, raw := range rawFrames {
		if raw == "" {
			continue
		}
		if !strings.HasPrefix(raw, "data: ") {
			t.Fatalf("unexpected sse frame %q", raw)
		}
		frames = append(frames, strings.TrimPrefix(raw, "data: "))
	}

	return frames
}
