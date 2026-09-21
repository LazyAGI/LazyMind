package workflow

import (
	"encoding/json"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlpolicy"
	"lazymind/core/workflow/controlstore"
)

func TestNativeDraftSaveDoesNotOptIntoExternalControl(t *testing.T) {
	db := seedSelectedHumanDraft(t)
	// Even after the extension tables are installed, ordinary saves retain
	// upstream draft versions and do not pause the native workflow.
	if err := db.AutoMigrate(&orm.WorkflowReviewCheckpoint{}, &orm.WorkflowHostAction{}); err != nil {
		t.Fatal(err)
	}
	revision, draftVersion := 1, int64(1)
	_, version, inPlace, err := UpdateSelectedHumanArtifactValue(t.Context(), db.DB,
		"session-draft", "draft_document", nil, "text", json.RawMessage(`{"text":"edited"}`), nil, &revision, &draftVersion)
	if err != nil || !inPlace || version != 2 {
		t.Fatalf("save: version=%d inPlace=%v err=%v", version, inPlace, err)
	}
	var session orm.WorkflowSession
	if err := db.First(&session, "id = ?", "session-draft").Error; err != nil {
		t.Fatal(err)
	}
	if session.Status != SessionStatusActive || session.ControlProtocol != "" || controlstore.EditPaused(session) || session.LastStoppedAt != nil {
		t.Fatalf("external controls changed native save: %+v", session)
	}
	var count int64
	db.Model(&orm.WorkflowReviewCheckpoint{}).Count(&count)
	if count != 0 {
		t.Fatal("native save created a review")
	}
}

func TestExternalCommandsCannotTakeOverNativeSessions(t *testing.T) {
	for _, protocol := range []string{"", controlpolicy.Protocol} {
		t.Run("protocol="+protocol, func(t *testing.T) {
			db := seedSelectedHumanDraft(t)
			if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "session-draft").Updates(map[string]any{
				"controller_host": "lazymind", "control_protocol": protocol, "create_user_id": "owner",
			}).Error; err != nil {
				t.Fatal(err)
			}
			for _, kind := range []string{"continue", "retry", "rewind", "stop"} {
				_, err := (WorkflowControlService{DB: db.DB}).Execute(t.Context(), "owner", "session-draft", WorkflowControlCommand{CommandID: "external-" + kind, Kind: kind})
				expectControlCode(t, err, "CONTROL_PROTOCOL_REQUIRED")
			}
			var session orm.WorkflowSession
			db.First(&session, "id = ?", "session-draft")
			if session.Status != SessionStatusActive || controlstore.Controlled(session) {
				t.Fatalf("native session was taken over: %+v", session)
			}
		})
	}
}
