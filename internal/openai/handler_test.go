package openai

import "testing"

func Test_modeForModel_usesSmartUpstreamMode_whenPublicModelVaries(t *testing.T) {
	tests := []struct {
		name  string
		model string
	}{
		{name: "smart model", model: "smart"},
		{name: "creative model", model: "creative"},
		{name: "balanced model", model: "balanced"},
		{name: "precise model", model: "precise"},
		{name: "unknown model", model: "gpt-4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := modeForModel(tt.model)

			if got != "smart" {
				t.Fatalf("modeForModel(%q) = %q, want smart", tt.model, got)
			}
		})
	}
}
