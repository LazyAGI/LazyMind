package workflow

import (
	"encoding/json"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestSessionDTOOmitsMisleadingRevisionNumbers(t *testing.T) {
	now := time.Now().UTC()
	dto := toSessionDTO(&orm.WorkflowSession{
		ID: "session-1", WorkflowID: "writer-workflow",
		WorkflowRevisionID: "revision-12", WorkflowRevisionNo: 12,
		CreatedAt: now, UpdatedAt: now,
	})
	body, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal session dto: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal session dto: %v", err)
	}
	if payload["pinned_revision_id"] != "revision-12" {
		t.Fatalf("pinned revision identity missing: %s", body)
	}
	for _, key := range []string{"pinned_revision_no", "head_revision_no"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("misleading UI version field %q leaked: %s", key, body)
		}
	}
}
