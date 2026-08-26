package chat

import (
	"reflect"
	"testing"
)

func TestDatasetIDsForChatTurnDeduplicatesFiltersAndBindings(t *testing.T) {
	reqBody := map[string]any{
		"filters": map[string]any{
			"kb_id": []any{"ds-1", "ds-2", "ds-1"},
		},
		"explicit_resource_bindings": map[string]any{
			"knowledge_base_ids": []any{"ds-2", "ds-3", ""},
		},
	}
	got := datasetIDsForChatTurn(reqBody)
	want := []string{"ds-1", "ds-2", "ds-3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("datasetIDsForChatTurn = %#v, want %#v", got, want)
	}
}

func TestDatasetIDsFromValueSupportsStringAndStringSlice(t *testing.T) {
	if got := datasetIDsFromValue("ds-1"); !reflect.DeepEqual(got, []string{"ds-1"}) {
		t.Fatalf("string value = %#v", got)
	}
	if got := datasetIDsFromValue([]string{"ds-1", "ds-2"}); !reflect.DeepEqual(got, []string{"ds-1", "ds-2"}) {
		t.Fatalf("[]string value = %#v", got)
	}
}

func TestSyntheticSourceFromChatReq(t *testing.T) {
	if got := syntheticSourceFromChatReq(map[string]any{
		"workflow_context": map[string]any{"synthetic_source": "auto_continue"},
	}); got != "auto_continue" {
		t.Fatalf("synthetic source = %q", got)
	}
	if got := syntheticSourceFromChatReq(map[string]any{}); got != "" {
		t.Fatalf("missing synthetic source = %q", got)
	}
}
