package learning

import "testing"

func TestExtractLLMResultAcceptsPlainTextForSingleMissingField(t *testing.T) {
	def := Capability{Fields: []Field{{Key: "pinyin"}, {Key: "meaning_in_context", Required: true}}}
	value, err := extractLLMResult(def, map[string]any{}, "在这里指按照规定道路行驶。")
	if err != nil {
		t.Fatal(err)
	}
	if value["meaning_in_context"] != "在这里指按照规定道路行驶。" {
		t.Fatalf("unexpected fallback value: %#v", value)
	}
}

func TestExtractLLMResultStripsPlainTextFence(t *testing.T) {
	def := Capability{Fields: []Field{{Key: "definition", Required: true}}}
	value, err := extractLLMResult(def, map[string]any{}, "```\n道路交通中的安全距离。\n```")
	if err != nil {
		t.Fatal(err)
	}
	if value["definition"] != "道路交通中的安全距离。" {
		t.Fatalf("unexpected fallback value: %#v", value)
	}
}

func TestExtractLLMResultRejectsAmbiguousPlainText(t *testing.T) {
	def := Capability{Fields: []Field{{Key: "evidence", Required: true}, {Key: "effects", Required: true}}}
	if _, err := extractLLMResult(def, map[string]any{}, "普通文本"); err == nil {
		t.Fatal("expected plain text with multiple missing fields to remain invalid")
	}
}
