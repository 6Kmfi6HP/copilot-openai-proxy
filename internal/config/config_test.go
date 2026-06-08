package config

import "testing"

func Test_envOr_returnsEnvironmentValue_whenSet(t *testing.T) {
	t.Setenv("API_KEY", "sk-test")

	got := envOr("API_KEY", "fallback")

	if got != "sk-test" {
		t.Fatalf("envOr returned %q, want %q", got, "sk-test")
	}
}

func Test_envOrInt_returnsParsedValue_whenSet(t *testing.T) {
	t.Setenv("TIMEOUT", "90")

	got := envOrInt("TIMEOUT", 120)

	if got != 90 {
		t.Fatalf("envOrInt returned %d, want %d", got, 90)
	}
}

func Test_envOrInt_returnsFallback_whenValueIsInvalid(t *testing.T) {
	t.Setenv("TIMEOUT", "invalid")

	got := envOrInt("TIMEOUT", 120)

	if got != 120 {
		t.Fatalf("envOrInt returned %d, want %d", got, 120)
	}
}

func Test_envOrBool_returnsParsedValue_whenSet(t *testing.T) {
	t.Setenv("DEBUG", "true")

	got := envOrBool("DEBUG", false)

	if !got {
		t.Fatal("envOrBool returned false, want true")
	}
}

func Test_envOrBool_returnsFallback_whenValueIsInvalid(t *testing.T) {
	t.Setenv("DEBUG", "invalid")

	got := envOrBool("DEBUG", false)

	if got {
		t.Fatal("envOrBool returned true, want false")
	}
}
