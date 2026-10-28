package modelsettings

import (
	"strings"
	"testing"
)

func boolPtr(v bool) *bool { return &v }
func intPtr(v int) *int    { return &v }

func TestSettingsKeepUnknownSeparateFromDisabled(t *testing.T) {
	raw, err := Normalize("llm", &Settings{FeatureEnabled: &FeatureFlags{ToolCalling: boolPtr(false)}})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if settings.FeatureSupport != nil {
		t.Fatal("disabled was converted into unsupported")
	}
	features := Effective(settings, false, "llm")
	if features.ToolCalling == nil || *features.ToolCalling || features.Reasoning != nil {
		t.Fatalf("effective flags=%+v", features)
	}
	if legacy := Effective(Settings{}, true, "llm"); legacy.Vision == nil || !*legacy.Vision || legacy.ToolCalling != nil {
		t.Fatalf("legacy flags=%+v", legacy)
	}
}

func TestSettingsRejectImpossibleEnabledFeatures(t *testing.T) {
	_, err := Normalize("llm", &Settings{FeatureSupport: &FeatureFlags{Reasoning: boolPtr(false)}, FeatureEnabled: &FeatureFlags{Reasoning: boolPtr(true)}})
	if err == nil {
		t.Fatal("enabled unsupported reasoning")
	}
	_, err = Normalize("embed", &Settings{FeatureSupport: &FeatureFlags{ToolCalling: boolPtr(true)}})
	if err == nil {
		t.Fatal("embedding accepted chat features")
	}
	_, err = Normalize("llm", &Settings{Embedding: &EmbeddingOptions{QueryPrefix: "query: "}})
	if err == nil {
		t.Fatal("chat accepted embedding settings")
	}
}

func TestEmbeddingSettingsPreserveExactPrefixesAndValidateBatch(t *testing.T) {
	raw, err := Normalize("embed", &Settings{Embedding: &EmbeddingOptions{QueryPrefix: "query: ", PassagePrefix: "\npassage:  ", BatchSize: intPtr(2)}})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Embedding.QueryPrefix != "query: " || settings.Embedding.PassagePrefix != "\npassage:  " || *settings.Embedding.BatchSize != 2 {
		t.Fatalf("prefix changed: %s", raw)
	}
	for _, size := range []int{0, -1, 2049} {
		if _, err := Normalize("embed", &Settings{Embedding: &EmbeddingOptions{BatchSize: intPtr(size)}}); err == nil {
			t.Fatalf("accepted batch=%d", size)
		}
	}
	if _, err := Normalize("embed", &Settings{Embedding: &EmbeddingOptions{QueryPrefix: strings.Repeat("x", 2049)}}); err == nil {
		t.Fatal("accepted oversized prefix")
	}
}

func TestEmbeddingIdentityOnlyChangesWithSemanticPrefixes(t *testing.T) {
	if !EmbeddingIdentityChanged(`{"embedding":{"query_prefix":"q: "}}`, `{}`) {
		t.Fatal("prefix identity unchanged")
	}
	if EmbeddingIdentityChanged(`{"embedding":{"batch_size":2}}`, `{"embedding":{"batch_size":8}}`) {
		t.Fatal("batch treated as different embedding space")
	}
}

func TestDecodeRejectsUnknownSettingsFields(t *testing.T) {
	if _, err := Decode(`{"feature_support":{"tool_typo":true}}`); err == nil {
		t.Fatal("unknown feature field accepted")
	}
}
