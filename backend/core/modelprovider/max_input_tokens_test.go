package modelprovider

import (
	"testing"
)

func TestResolveSeededMaxInputTokens(t *testing.T) {
	if err := LoadContextWindows("../config/model_context_windows.yaml"); err != nil {
		t.Fatal(err)
	}

	got, err := resolveSeededMaxInputTokens("llm", "qwen-plus")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != "1M" {
		t.Fatalf("seeded llm = %v, want 1M", got)
	}

	got, err = resolveSeededMaxInputTokens("llm", "unknown-seeded-llm")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != defaultLLMMaxInputTokens {
		t.Fatalf("missing window llm = %v, want %s", got, defaultLLMMaxInputTokens)
	}

	got, err = resolveSeededMaxInputTokens("embed", "text-embedding-3-large")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != "8192" {
		t.Fatalf("seeded embed = %v, want 8192", got)
	}

	got, err = resolveSeededMaxInputTokens("embed", "unknown-embed")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("missing window embed = %v, want nil", got)
	}

	got, err = resolveSeededMaxInputTokens("tts", "any-tts")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("tts = %v, want nil", got)
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
