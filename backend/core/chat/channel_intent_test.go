package chat

import (
	"encoding/json"
	"strings"
	"testing"

	"lazymind/core/algo"
)

// --- validChannelIntentText ---

// TestValidChannelIntentText_Valid accepts non-empty provider and message within limits.
func TestValidChannelIntentText_Valid(t *testing.T) {
	if !validChannelIntentText("slack", "hello world") {
		t.Fatal("expected valid provider+message")
	}
}

// TestValidChannelIntentText_EmptyProvider is rejected.
func TestValidChannelIntentText_EmptyProvider(t *testing.T) {
	if validChannelIntentText("", "hello") {
		t.Fatal("expected invalid for empty provider")
	}
	if validChannelIntentText("  ", "hello") {
		t.Fatal("expected invalid for whitespace provider")
	}
}

// TestValidChannelIntentText_EmptyMessage is rejected.
func TestValidChannelIntentText_EmptyMessage(t *testing.T) {
	if validChannelIntentText("slack", "") {
		t.Fatal("expected invalid for empty message")
	}
	if validChannelIntentText("slack", "  ") {
		t.Fatal("expected invalid for whitespace message")
	}
}

// TestValidChannelIntentText_ProviderTooLong is rejected (>32 runes).
func TestValidChannelIntentText_ProviderTooLong(t *testing.T) {
	long := make([]byte, 33)
	for i := range long {
		long[i] = 'a'
	}
	if validChannelIntentText(string(long), "hello") {
		t.Fatal("expected invalid for provider > 32 runes")
	}
}

// TestValidChannelIntentText_MessageTooLong is rejected (>4000 runes).
func TestValidChannelIntentText_MessageTooLong(t *testing.T) {
	long := make([]byte, 4001)
	for i := range long {
		long[i] = 'x'
	}
	if validChannelIntentText("slack", string(long)) {
		t.Fatal("expected invalid for message > 4000 runes")
	}
}

// TestValidChannelIntentText_AtLimits accepts provider/message exactly at max length.
func TestValidChannelIntentText_AtLimits(t *testing.T) {
	provider := make([]byte, 32)
	for i := range provider {
		provider[i] = 'p'
	}
	msg := make([]byte, 4000)
	for i := range msg {
		msg[i] = 'm'
	}
	if !validChannelIntentText(string(provider), string(msg)) {
		t.Fatal("expected valid at exact limits")
	}
}

// --- validChannelCommandName ---

// TestValidChannelCommandName accepts alphanumeric, dash, underscore, dot.
func TestValidChannelCommandName(t *testing.T) {
	tests := []struct {
		name string
		ok   bool
	}{
		{"create_task", true},
		{"delete-task", true},
		{"get.status", true},
		{"MixedCase123", true},
		{"", false},
		{"has space", false},
		{"special!", false},
		{"中文", false},
		{string(make([]byte, 129)), false}, // >128 chars
	}
	for _, tt := range tests {
		got := validChannelCommandName(tt.name)
		if got != tt.ok {
			t.Fatalf("validChannelCommandName(%q) = %v, want %v", tt.name, got, tt.ok)
		}
	}
}

// TestValidChannelCommandName_AtMaxLength accepts exactly 128 chars.
func TestValidChannelCommandName_AtMaxLength(t *testing.T) {
	name := make([]byte, 128)
	for i := range name {
		name[i] = 'a'
	}
	if !validChannelCommandName(string(name)) {
		t.Fatal("expected valid for 128-char name")
	}
}

// --- validJSONObject ---

// TestValidJSONObject accepts a valid non-empty JSON object within size limit.
func TestValidJSONObject_Valid(t *testing.T) {
	if !validJSONObject(json.RawMessage(`{"key":"value"}`), 1024) {
		t.Fatal("expected valid JSON object")
	}
}

// TestValidJSONObject_Empty is rejected.
func TestValidJSONObject_Empty(t *testing.T) {
	if validJSONObject(json.RawMessage(``), 1024) {
		t.Fatal("expected invalid for empty bytes")
	}
}

// TestValidJSONObject_ExceedsMaxBytes is rejected.
func TestValidJSONObject_ExceedsMaxBytes(t *testing.T) {
	if validJSONObject(json.RawMessage(`{"x":"y"}`), 5) {
		t.Fatal("expected invalid when exceeding max bytes")
	}
}

// TestValidJSONObject_Array is rejected (must be JSON object, not array).
func TestValidJSONObject_Array(t *testing.T) {
	if validJSONObject(json.RawMessage(`[1,2,3]`), 1024) {
		t.Fatal("expected invalid for JSON array (not object)")
	}
}

// TestValidJSONObject_String is rejected (must be JSON object).
func TestValidJSONObject_String(t *testing.T) {
	if validJSONObject(json.RawMessage(`"hello"`), 1024) {
		t.Fatal("expected invalid for JSON string primitive")
	}
}

// TestValidJSONObject_Null is rejected (null is not a valid object).
func TestValidJSONObject_Null(t *testing.T) {
	if validJSONObject(json.RawMessage(`null`), 1024) {
		t.Fatal("expected invalid for JSON null")
	}
}

// TestValidJSONObject_TrailingGarbage is rejected (single value enforced).
func TestValidJSONObject_TrailingGarbage(t *testing.T) {
	if validJSONObject(json.RawMessage(`{"a":1}{"b":2}`), 1024) {
		t.Fatal("expected invalid for multiple JSON values")
	}
}

// --- ensureChannelIntentEOF ---

// TestEnsureChannelIntentEOF_SingleValue returns nil after the first value is consumed.
func TestEnsureChannelIntentEOF_SingleValue(t *testing.T) {
	decoder := json.NewDecoder(strings.NewReader(`{"a":1}`))
	// Consume the first (and only) JSON value, simulating the handler's initial decode.
	var first map[string]any
	if err := decoder.Decode(&first); err != nil {
		t.Fatalf("initial decode: %v", err)
	}
	if err := ensureChannelIntentEOF(decoder); err != nil {
		t.Fatalf("expected nil after single value, got %v", err)
	}
}

// TestEnsureChannelIntentEOF_MultipleValues returns error.
func TestEnsureChannelIntentEOF_MultipleValues(t *testing.T) {
	decoder := json.NewDecoder(strings.NewReader(`{"a":1} {"b":2}`))
	if err := ensureChannelIntentEOF(decoder); err == nil {
		t.Fatal("expected error for multiple values")
	}
}

// TestEnsureChannelIntentEOF_EmptyInput returns nil (EOF after empty is treated as success).
func TestEnsureChannelIntentEOF_EmptyInput(t *testing.T) {
	decoder := json.NewDecoder(strings.NewReader(``))
	if err := ensureChannelIntentEOF(decoder); err != nil {
		t.Fatalf("expected nil for empty input (EOF is success), got %v", err)
	}
}

// --- validChannelCommandEnvelope ---

// TestValidChannelCommandEnvelope_Valid returns true when envelope command matches registry.
func TestValidChannelCommandEnvelope_Valid(t *testing.T) {
	registry := algo.ChannelCommandRegistry{
		SchemaVersion: "1.0",
		Commands: []algo.ChannelCommandDescription{
			{Name: "search", Description: "search docs"},
			{Name: "summarize", Description: "summarize text"},
		},
		SelectionRules: []string{"rule1"},
		OutputSchema:   json.RawMessage(`{}`),
	}
	envelope := algo.ChannelCommandEnvelope{
		SchemaVersion: "1.0",
		Command:       "summarize",
		Parameters:    json.RawMessage(`{}`),
	}
	if !validChannelCommandEnvelope(envelope, registry) {
		t.Fatal("expected valid envelope")
	}
}

// TestValidChannelCommandEnvelope_SchemaMismatch returns false.
func TestValidChannelCommandEnvelope_SchemaMismatch(t *testing.T) {
	registry := algo.ChannelCommandRegistry{
		SchemaVersion: "1.0",
		Commands:      []algo.ChannelCommandDescription{{Name: "search", Description: "desc"}},
	}
	envelope := algo.ChannelCommandEnvelope{
		SchemaVersion: "2.0",
		Command:       "search",
		Parameters:    json.RawMessage(`{}`),
	}
	if validChannelCommandEnvelope(envelope, registry) {
		t.Fatal("expected invalid when schema versions differ")
	}
}

// TestValidChannelCommandEnvelope_UnknownCommand returns false.
func TestValidChannelCommandEnvelope_UnknownCommand(t *testing.T) {
	registry := algo.ChannelCommandRegistry{
		SchemaVersion: "1.0",
		Commands:      []algo.ChannelCommandDescription{{Name: "search", Description: "desc"}},
	}
	envelope := algo.ChannelCommandEnvelope{
		SchemaVersion: "1.0",
		Command:       "unknown",
		Parameters:    json.RawMessage(`{}`),
	}
	if validChannelCommandEnvelope(envelope, registry) {
		t.Fatal("expected invalid for unknown command")
	}
}

// TestValidChannelCommandEnvelope_InvalidParameters returns false.
func TestValidChannelCommandEnvelope_InvalidParameters(t *testing.T) {
	registry := algo.ChannelCommandRegistry{
		SchemaVersion: "1.0",
		Commands:      []algo.ChannelCommandDescription{{Name: "search", Description: "desc"}},
	}
	longParams := make([]byte, maxChannelParametersBytes+1)
	for i := range longParams {
		longParams[i] = 'x'
	}
	envelope := algo.ChannelCommandEnvelope{
		SchemaVersion: "1.0",
		Command:       "search",
		Parameters:    json.RawMessage(`"` + string(longParams) + `"`),
	}
	if validChannelCommandEnvelope(envelope, registry) {
		t.Fatal("expected invalid for oversized parameters")
	}
}

// --- validChannelCommandRegistry ---

// TestValidChannelCommandRegistry_Valid accepts a minimal valid registry.
func TestValidChannelCommandRegistry_Valid(t *testing.T) {
	registry := algo.ChannelCommandRegistry{
		SchemaVersion: "1.0",
		Commands: []algo.ChannelCommandDescription{
			{Name: "search", Description: "search documents"},
		},
		SelectionRules: []string{"rule1"},
		OutputSchema:   json.RawMessage(`{}`),
	}
	if !validChannelCommandRegistry(registry) {
		t.Fatal("expected valid minimal registry")
	}
}

// TestValidChannelCommandRegistry_EmptySchemaVersion is rejected.
func TestValidChannelCommandRegistry_EmptySchemaVersion(t *testing.T) {
	registry := algo.ChannelCommandRegistry{
		Commands:       []algo.ChannelCommandDescription{{Name: "x", Description: "d"}},
		SelectionRules: []string{"r"},
	}
	if validChannelCommandRegistry(registry) {
		t.Fatal("expected invalid for empty schema version")
	}
}

// TestValidChannelCommandRegistry_NoCommands is rejected.
func TestValidChannelCommandRegistry_NoCommands(t *testing.T) {
	registry := algo.ChannelCommandRegistry{
		SchemaVersion:  "1.0",
		SelectionRules: []string{"r"},
	}
	if validChannelCommandRegistry(registry) {
		t.Fatal("expected invalid for zero commands")
	}
}

// TestValidChannelCommandRegistry_NoSelectionRules is rejected.
func TestValidChannelCommandRegistry_NoSelectionRules(t *testing.T) {
	registry := algo.ChannelCommandRegistry{
		SchemaVersion: "1.0",
		Commands:      []algo.ChannelCommandDescription{{Name: "x", Description: "d"}},
	}
	if validChannelCommandRegistry(registry) {
		t.Fatal("expected invalid for zero selection rules")
	}
}

// TestValidChannelCommandRegistry_DuplicateCommands is rejected.
func TestValidChannelCommandRegistry_DuplicateCommands(t *testing.T) {
	registry := algo.ChannelCommandRegistry{
		SchemaVersion: "1.0",
		Commands: []algo.ChannelCommandDescription{
			{Name: "dup", Description: "first"},
			{Name: "dup", Description: "second"},
		},
		SelectionRules: []string{"r"},
	}
	if validChannelCommandRegistry(registry) {
		t.Fatal("expected invalid for duplicate command names")
	}
}

// TestValidChannelCommandRegistry_EmptyDescription is rejected.
func TestValidChannelCommandRegistry_EmptyDescription(t *testing.T) {
	registry := algo.ChannelCommandRegistry{
		SchemaVersion:  "1.0",
		Commands:       []algo.ChannelCommandDescription{{Name: "x", Description: ""}},
		SelectionRules: []string{"r"},
	}
	if validChannelCommandRegistry(registry) {
		t.Fatal("expected invalid for empty command description")
	}
}

// TestValidChannelCommandRegistry_DescriptionTooLong is rejected.
func TestValidChannelCommandRegistry_DescriptionTooLong(t *testing.T) {
	long := make([]byte, 2001)
	for i := range long {
		long[i] = 'd'
	}
	registry := algo.ChannelCommandRegistry{
		SchemaVersion:  "1.0",
		Commands:       []algo.ChannelCommandDescription{{Name: "x", Description: string(long)}},
		SelectionRules: []string{"r"},
	}
	if validChannelCommandRegistry(registry) {
		t.Fatal("expected invalid for description > 2000 runes")
	}
}

// TestValidChannelCommandRegistry_EmptySelectionRule is rejected.
func TestValidChannelCommandRegistry_EmptySelectionRule(t *testing.T) {
	registry := algo.ChannelCommandRegistry{
		SchemaVersion:  "1.0",
		Commands:       []algo.ChannelCommandDescription{{Name: "x", Description: "d"}},
		SelectionRules: []string{""},
	}
	if validChannelCommandRegistry(registry) {
		t.Fatal("expected invalid for empty selection rule")
	}
}
