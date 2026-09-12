package main

import (
	"reflect"
	"testing"
)

func TestConversationStatusSchemaKeepsActivityAndOptionalTerminal(t *testing.T) {
	schema := generatedOpenAPISchemas(t)["ConversationRunningStatusItem"].(map[string]any)
	props := schema["properties"].(map[string]any)
	for field, want := range map[string][]any{
		"status":          {"running", "idle", "unknown"},
		"terminal_status": {"completed", "failed", "canceled"},
	} {
		if got := props[field].(map[string]any)["enum"]; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s values = %v, want %v", field, got, want)
		}
	}
	if props["terminal_version"].(map[string]any)["type"] != "string" {
		t.Fatal("terminal_version must be a string")
	}
	if got := schema["required"]; !reflect.DeepEqual(got, []any{"conversation_id", "status"}) {
		t.Fatalf("terminal status must be optional: required=%v", got)
	}
}
