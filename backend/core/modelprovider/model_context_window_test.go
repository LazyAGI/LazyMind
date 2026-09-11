package modelprovider

import (
	"testing"
)

func TestLoadContextWindowsAndLookup(t *testing.T) {
	if err := LoadContextWindows("../config/model_context_windows.yaml"); err != nil {
		t.Fatal(err)
	}

	got, ok := lookupMaxInputTokens("deepseek-flash", "llm")
	if !ok || got != "1M" {
		t.Fatalf("deepseek-flash = %q ok=%v, want 1M", got, ok)
	}

	got, ok = lookupMaxInputTokens("gpt-4o", "")
	if !ok || got != "128K" {
		t.Fatalf("gpt-4o = %q ok=%v, want 128K", got, ok)
	}

	got, ok = lookupMaxInputTokens("openai/gpt-4o", "llm")
	if !ok || got != "128K" {
		t.Fatalf("openai/gpt-4o trailing lookup = %q ok=%v, want 128K", got, ok)
	}

	got, ok = lookupMaxInputTokens("claude-sonnet-4.6", "vlm")
	if !ok || got != "1M" {
		t.Fatalf("claude-sonnet-4.6 dotted lookup = %q ok=%v, want 1M", got, ok)
	}

	got, ok = lookupMaxInputTokens("gpt-6", "")
	if !ok || got != "1050000" {
		t.Fatalf("gpt-6 = %q ok=%v, want 1050000", got, ok)
	}

	got, ok = lookupMaxInputTokens("gpt_6", "")
	if !ok || got != "1050000" {
		t.Fatalf("gpt_6 underscore alias = %q ok=%v, want 1050000", got, ok)
	}

	got, ok = lookupMaxInputTokens("GLM-5.3", "llm")
	if !ok || got != "1M" {
		t.Fatalf("GLM-5.3 = %q ok=%v, want 1M", got, ok)
	}

	got, ok = lookupMaxInputTokens("GLM_5.3", "")
	if !ok || got != "1M" {
		t.Fatalf("GLM_5.3 underscore alias = %q ok=%v, want 1M", got, ok)
	}

	got, ok = lookupMaxInputTokens("Qwen3.8-flash-next", "vlm")
	if !ok || got != "256K" {
		t.Fatalf("Qwen3.8-flash-next = %q ok=%v, want 256K", got, ok)
	}

	got, ok = lookupMaxInputTokens("qwen3.8_flash_next", "")
	if !ok || got != "256K" {
		t.Fatalf("qwen3.8_flash_next underscore alias = %q ok=%v, want 256K", got, ok)
	}

	got, ok = lookupMaxInputTokens("gpt-5.4", "vlm")
	if !ok || got != "1050000" {
		t.Fatalf("gpt-5.4 = %q ok=%v, want 1050000", got, ok)
	}

	got, ok = lookupMaxInputTokens("Qwen/Qwen3.5-397B-A17B", "vlm")
	if !ok || got != "262144" {
		t.Fatalf("Qwen3.5-397B = %q ok=%v, want 262144", got, ok)
	}

	got, ok = lookupMaxInputTokens("GLM-4.7", "llm")
	if !ok || got != "200K" {
		t.Fatalf("GLM-4.7 = %q ok=%v, want 200K", got, ok)
	}

	got, ok = lookupMaxInputTokens("MiniMax-M2.5", "llm")
	if !ok || got != "204800" {
		t.Fatalf("MiniMax-M2.5 = %q ok=%v, want 204800", got, ok)
	}

	got, ok = lookupMaxInputTokens("text-embedding-3-large", "embed")
	if !ok || got != "8192" {
		t.Fatalf("text-embedding-3-large = %q ok=%v, want 8192", got, ok)
	}

	if _, ok := lookupMaxInputTokens("deepseek-chat", ""); ok {
		t.Fatal("expected deprecated deepseek-chat to miss")
	}

	if _, ok := lookupMaxInputTokens("claude-fable-5.1", ""); ok {
		t.Fatal("expected provider-specific claude-fable-5.1 to miss")
	}

	if _, ok := lookupMaxInputTokens("definitely-not-a-real-model", ""); ok {
		t.Fatal("expected unknown model to miss")
	}
}

func TestResolveAddModelMaxInputTokensUsesContextWindows(t *testing.T) {
	if err := LoadContextWindows("../config/model_context_windows.yaml"); err != nil {
		t.Fatal(err)
	}

	got, err := resolveAddModelMaxInputTokens("llm", "qwen-plus", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != "1M" {
		t.Fatalf("qwen-plus = %v, want 1M", got)
	}

	override := "8K"
	got, err = resolveAddModelMaxInputTokens("llm", "qwen-plus", &override)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != "8K" {
		t.Fatalf("qwen-plus override = %v, want 8K", got)
	}

	explicitDefault := DefaultLLMMaxInputTokens
	got, err = resolveAddModelMaxInputTokens("llm", "qwen-plus", &explicitDefault)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != DefaultLLMMaxInputTokens {
		t.Fatalf("qwen-plus explicit 128K = %v, want %s", got, DefaultLLMMaxInputTokens)
	}

	got, err = resolveAddModelMaxInputTokens("llm", "custom-unknown-llm", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != DefaultLLMMaxInputTokens {
		t.Fatalf("unknown llm = %v, want %s", got, DefaultLLMMaxInputTokens)
	}

	got, err = resolveAddModelMaxInputTokens("embed", "qwen-plus", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("embed = %v, want nil", got)
	}
}
