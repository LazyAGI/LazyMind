package chat

import "testing"

func TestParseMaxInputTokens(t *testing.T) {
	tests := map[string]int64{
		"128K": 128000,
		"200k": 200000,
		"1M":   1000000,
		"32":   32,
	}
	for input, expected := range tests {
		got := parseMaxInputTokens(input)
		if got == nil || *got != expected {
			t.Fatalf("parseMaxInputTokens(%q) = %v, want %d", input, got, expected)
		}
	}
	for _, input := range []string{"", "nope", "0"} {
		if got := parseMaxInputTokens(input); got != nil {
			t.Fatalf("parseMaxInputTokens(%q) = %d, want nil", input, *got)
		}
	}
}

func TestPreviewQueryReadsTextInput(t *testing.T) {
	raw := map[string]any{
		"input": []any{
			map[string]any{"input_type": "text", "text": "  hello  "},
			map[string]any{"input_type": "file", "uri": "/tmp/a.txt"},
		},
	}
	if got := previewQuery(raw); got != "hello" {
		t.Fatalf("previewQuery() = %q, want hello", got)
	}
}

func TestMentionedBuiltinPluginReplacesDefaultCatalog(t *testing.T) {
	catalog := []map[string]any{{"plugin_ref": "plugin:default", "plugin_id": "default"}}
	selected, builtins, err := mergeMentionedPlugins(
		t.Context(), nil, "user-1", []string{"builtin:image-plugin"}, catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 0 {
		t.Fatalf("selected catalog = %#v, want no default plugins", selected)
	}
	if len(builtins) != 1 || builtins[0] != "image-plugin" {
		t.Fatalf("builtins = %#v, want image-plugin", builtins)
	}
}
