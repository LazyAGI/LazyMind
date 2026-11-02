package chat

import (
	"encoding/json"
	"testing"
)

func TestKnowledgeOperationSnapshotMerge(t *testing.T) {
	ext := json.RawMessage(`{"artifact_ids":["art-1"]}`)
	operation := map[string]any{"call_id": "call-1", "action": "knowledge.create", "items": []any{map[string]any{"name": "论文集"}}}
	ext = mergeKnowledgeOperation(ext, operation)
	ext = mergeKnowledgeOperation(ext, operation)
	var data map[string]any
	if err := json.Unmarshal(ext, &data); err != nil {
		t.Fatal(err)
	}
	if len(data["knowledge_operations"].([]any)) != 1 || len(data["artifact_ids"].([]any)) != 1 {
		t.Fatalf("lost history or duplicated card: %s", ext)
	}
}
