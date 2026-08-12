package subagent

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func TestResolveDurableWorkflowArtifact(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&orm.WorkflowSessionStep{}, &orm.WorkflowSlotRevision{}, &orm.WorkflowHumanArtifact{},
	); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	humanID := "human-1"
	seq := 1
	rows := []any{
		&orm.WorkflowSessionStep{ID: "attempt-1", SessionID: "session-1", StepID: "compose",
			Attempt: 1, TaskID: "task-1", Status: "succeeded", Validity: "effective",
			CreatedAt: now, UpdatedAt: now},
		&orm.WorkflowHumanArtifact{ID: humanID, SessionID: "session-1", Slot: "document",
			ContentType: "file", Value: json.RawMessage(`{"path":"/var/lib/lazymind/uploads/workflow-artifacts/document.docx","filename":"document.docx"}`), CreatedAt: now},
		&orm.WorkflowSlotRevision{ID: "revision-1", SessionID: "session-1", SlotID: "document",
			Slot: "document", StepID: "compose", Attempt: 1, Revision: 1, Selected: true,
			ArtifactSeq: &seq, HumanArtifactID: &humanID, ProducerAttemptID: "attempt-1",
			Validity: "effective", CreatedAt: now},
	}
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	contentType, value, _, durable := ResolveDurableWorkflowArtifact(
		t.Context(), db, "task-1", "document", 1, "file",
		json.RawMessage(`{"path":"/tmp/transient.docx"}`), nil,
	)
	if !durable || contentType != "file" || string(value) == `{"path":"/tmp/transient.docx"}` {
		t.Fatalf("durable artifact was not resolved: durable=%v type=%q value=%s", durable, contentType, value)
	}
}
