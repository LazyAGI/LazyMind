package taskcenter

import (
	"lazymind/core/common/orm"
	"testing"
)

func TestWorkflowChangeCursorIsOwnerScoped(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.WorkflowEvent{})
	for _, owner := range []string{"owner", "other"} {
		if err := db.Create(&orm.WorkflowEvent{OwnerUserID: owner, SessionID: owner, EventType: "workflow.patch", PayloadJSON: []byte(`{}`)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for owner, want := range map[string]int64{"owner": 1, "other": 2, "nobody": 0} {
		got, err := workflowCursor(db.DB, owner)
		if err != nil || got != want {
			t.Fatalf("owner=%s cursor=%d err=%v", owner, got, err)
		}
	}
}
