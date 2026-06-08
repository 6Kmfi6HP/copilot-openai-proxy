package copilot

import (
	"encoding/json"
	"net/url"
	"testing"
)

func Test_defaultSetOptions_matchesReferenceCapabilities_whenMarshaled(t *testing.T) {
	data, err := json.Marshal(defaultSetOptions())

	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	var got struct {
		Event                 string            `json:"event"`
		Type                  json.RawMessage   `json:"type"`
		Options               json.RawMessage   `json:"options"`
		SupportedCards        []string          `json:"supportedCards"`
		Ads                   setOptionsAds     `json:"ads"`
		SupportedActions      []string          `json:"supportedActions"`
		SupportedFeatures     []string          `json:"supportedFeatures"`
		SupportedUIComponents map[string]string `json:"supportedUIComponents"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if got.Event != "setOptions" {
		t.Fatalf("event = %q, want setOptions", got.Event)
	}
	if got.Type != nil {
		t.Fatalf("setOptions JSON contains type field: %s", data)
	}
	if got.Options != nil {
		t.Fatalf("setOptions JSON contains options wrapper: %s", data)
	}

	wantTypes := []string{"text", "multimedia", "product"}
	wantCards := []string{"ads", "createCalendarEvent", "chart", "consentV2", "finance", "flashcard", "image", "local"}
	wantActions := []string{
		"composer-prefill-conversation-action",
		"composer-send-conversation-action-v2",
		"short-conversation-action",
		"session-duration-nudge",
	}

	assertStringSliceEqual(t, got.Ads.SupportedTypes, wantTypes)
	assertStringSliceEqual(t, got.SupportedCards, wantCards)
	assertStringSliceEqual(t, got.SupportedActions, []string{})
	assertStringSliceEqual(t, got.SupportedFeatures, wantActions)
	if !got.Ads.OptOutOfPersonalization {
		t.Fatalf("ads.optOutOfPersonalization = false, want true")
	}
	if got.Ads.Product.TagsSupported {
		t.Fatalf("ads.product.tagsSupported = true, want false")
	}
	for component, version := range map[string]string{"Text": "1.2", "Card": "1.2", "Map": "1.3", "Table.Cell": "1.3"} {
		if got.SupportedUIComponents[component] != version {
			t.Fatalf("supportedUIComponents[%q] = %q, want %q", component, got.SupportedUIComponents[component], version)
		}
	}
}

func Test_newSendMessage_usesContentArrayAndMode_whenMarshaled(t *testing.T) {
	data, err := json.Marshal(newSendMessage("hello", "conv-1", "smart"))

	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	want := `{"event":"send","content":[{"type":"text","text":"hello"}],"conversationId":"conv-1","mode":"smart","product":"smart"}`
	if string(data) != want {
		t.Fatalf("send message JSON = %s, want %s", data, want)
	}
}

func Test_newSendMessage_keepsProductSmart_whenModeVaries(t *testing.T) {
	tests := []struct {
		name string
		mode string
		want string
	}{
		{
			name: "creative mode",
			mode: "creative",
			want: `{"event":"send","content":[{"type":"text","text":"hello"}],"conversationId":"conv-1","mode":"creative","product":"smart"}`,
		},
		{
			name: "balanced mode",
			mode: "balanced",
			want: `{"event":"send","content":[{"type":"text","text":"hello"}],"conversationId":"conv-1","mode":"balanced","product":"smart"}`,
		},
		{
			name: "precise mode",
			mode: "precise",
			want: `{"event":"send","content":[{"type":"text","text":"hello"}],"conversationId":"conv-1","mode":"precise","product":"smart"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(newSendMessage("hello", "conv-1", tt.mode))

			if err != nil {
				t.Fatalf("json.Marshal returned error: %v", err)
			}
			if string(data) != tt.want {
				t.Fatalf("send message JSON = %s, want %s", data, tt.want)
			}
		})
	}
}

func Test_buildWebSocketURL_addsReferenceQuery_whenCalled(t *testing.T) {
	rawURL, clientSessionID, err := buildWebSocketURL()

	if err != nil {
		t.Fatalf("buildWebSocketURL returned error: %v", err)
	}
	if clientSessionID == "" {
		t.Fatalf("clientSessionID is empty")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("url.Parse returned error: %v", err)
	}
	if got := parsed.Query().Get("api-version"); got != "2" {
		t.Fatalf("api-version = %q, want 2", got)
	}
	if got := parsed.Query().Get("clientSessionId"); got != clientSessionID {
		t.Fatalf("clientSessionId query = %q, want %q", got, clientSessionID)
	}
}

func assertStringSliceEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("slice length = %d, want %d: got %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("slice[%d] = %q, want %q; got %v", i, got[i], want[i], got)
		}
	}
}
