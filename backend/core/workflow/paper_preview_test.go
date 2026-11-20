package workflow

import (
	"encoding/json"
	"testing"

	"lazymind/core/common/orm"
	workflowstore "lazymind/core/workflow/store"
)

func TestPaperPreviewProjectionRestoresOnlyCurrentExternalAttempt(t *testing.T) {
	db, _ := setupBatchTransitionSession(t)
	if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Update("plugin_id", "paper-writing").Error; err != nil {
		t.Fatal(err)
	}
	row := orm.WorkflowSessionStep{ID: "preview-attempt", SessionID: "batch-session", StepID: "branch_b", Attempt: 1,
		ExecutorHost: "external-agent", FencingGeneration: 2, Status: "claimed", Validity: "effective"}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	preview := workflowstore.ArtifactPreview{AttemptID: row.ID, StepID: row.StepID, Attempt: 1,
		Slot: "branch_b_document", StreamID: "preview-attempt:2", Seq: 1, Content: "# Live section", State: "streaming"}
	payload, _ := json.Marshal(preview)
	if err := db.Create(&orm.WorkflowEvent{SessionID: row.SessionID, EntityID: row.ID, OwnerUserID: "batch-user",
		EventType: "artifact.preview", PayloadJSON: payload}).Error; err != nil {
		t.Fatal(err)
	}
	session, err := GetSession(t.Context(), db.DB, row.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := projectSession(t.Context(), db.DB, session)
	if err != nil {
		t.Fatal(err)
	}
	if projection.ArtifactPreviews[row.ID].Content != preview.Content {
		t.Fatal("reopening lost the current preview")
	}
	if projection.AttemptHistory[row.StepID][0].ExecutorHost != "external-agent" || projection.AttemptHistory[row.StepID][0].FencingGeneration != 2 {
		t.Fatal("snapshot lost host/fencing identity")
	}
	for _, update := range []map[string]any{{"fencing_generation": 3}, {"fencing_generation": 2, "status": "failed"},
		{"status": "claimed", "validity": "stale"}, {"validity": "effective", "executor_host": "lazymind"}} {
		if err := db.Model(&row).Updates(update).Error; err != nil {
			t.Fatal(err)
		}
		projection, err := projectSession(t.Context(), db.DB, session)
		if err != nil {
			t.Fatal(err)
		}
		if len(projection.ArtifactPreviews) != 0 {
			t.Fatal("snapshot replayed a retired or native attempt preview")
		}
	}
}
