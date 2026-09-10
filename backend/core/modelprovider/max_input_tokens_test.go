package modelprovider

import (
	"testing"
)

func TestApplyCatalogMaxInputTokens(t *testing.T) {
	t.Parallel()

	catalog := "200k"
	got, err := applyCatalogMaxInputTokens("llm", &catalog)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != "200K" {
		t.Fatalf("catalog llm = %v, want 200K", got)
	}

	got, err = applyCatalogMaxInputTokens("llm", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != defaultLLMMaxInputTokens {
		t.Fatalf("missing catalog llm = %v, want %s", got, defaultLLMMaxInputTokens)
	}

	got, err = applyCatalogMaxInputTokens("embed", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("missing catalog embed = %v, want nil", got)
	}

	invalidType := "128K"
	if _, err := applyCatalogMaxInputTokens("tts", &invalidType); err == nil {
		t.Fatal("expected tts catalog max_input_tokens to fail")
	}
}

func TestResolveUserMaxInputTokens(t *testing.T) {
	t.Parallel()

	got, err := resolveUserMaxInputTokens("llm", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != defaultLLMMaxInputTokens {
		t.Fatalf("default llm = %v, want %s", got, defaultLLMMaxInputTokens)
	}

	raw := "1m"
	got, err = resolveUserMaxInputTokens("llm", &raw)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != "1M" {
		t.Fatalf("explicit llm = %v, want 1M", got)
	}

	if _, err := resolveUserMaxInputTokens("embed", &raw); err == nil {
		t.Fatal("expected embed user max_input_tokens to fail")
	}

	got, err = resolveUserMaxInputTokens("embed", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("omitted embed = %v, want nil", got)
	}

	tooLong := "999999999999999999999999K"
	if _, err := parseMaxInputTokens(tooLong); err == nil {
		t.Fatal("expected over-length max_input_tokens to fail")
	}
}

func TestResolveRequiredUserMaxInputTokens(t *testing.T) {
	t.Parallel()

	if _, err := resolveRequiredUserMaxInputTokens("llm", nil); err == nil {
		t.Fatal("expected missing max_input_tokens to fail")
	}
	empty := "  "
	if _, err := resolveRequiredUserMaxInputTokens("llm", &empty); err == nil {
		t.Fatal("expected blank max_input_tokens to fail")
	}
	raw := "1m"
	got, err := resolveRequiredUserMaxInputTokens("llm", &raw)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != "1M" {
		t.Fatalf("required llm = %v, want 1M", got)
	}
}
