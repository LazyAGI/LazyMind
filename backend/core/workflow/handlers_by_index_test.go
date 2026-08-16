package workflow

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
)

func TestPatchSlotItemByIndexHonorsBaseRevision(t *testing.T) {
	db := newHandlerTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowHumanArtifact{}); err != nil {
		t.Fatalf("migrate human artifacts: %v", err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSession{
		ID: "session-1", ConversationID: "conversation-1", WorkflowID: "writer-workflow",
		Status: SessionStatusActive, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID: "revision-3", SessionID: "session-1", SlotID: "draft_document",
		Revision: 3, Selected: true, ChangeSource: "ai", Slot: "draft_document",
		StepID: "write_document", Attempt: 1, CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create selected revision: %v", err)
	}

	request := func(baseRevision int) *httptest.ResponseRecorder {
		req := httptest.NewRequest(
			http.MethodPatch,
			"/workflow-sessions/session-1/slots/draft_document/items/idx/-1",
			jsonBody(fmt.Sprintf(`{"value":{"path":"/var/lib/lazymind/uploads/draft.md","filename":"draft.md"},"content_type":"file","mode":"checkpoint","base_revision":%d}`, baseRevision)),
		)
		req = mux.SetURLVars(req, map[string]string{
			"session_id": "session-1", "slot_id": "draft_document", "list_index": "-1",
		})
		rec := httptest.NewRecorder()
		PatchSlotItemByIndex(rec, req)
		return rec
	}

	if rec := request(3); rec.Code != http.StatusOK {
		t.Fatalf("matching revision save: got %d, body=%s", rec.Code, rec.Body.String())
	}
	if rec := request(3); rec.Code != http.StatusConflict {
		t.Fatalf("stale revision save: got %d, body=%s", rec.Code, rec.Body.String())
	}

	var revisions []orm.WorkflowSlotRevision
	if err := db.Where("session_id = ? AND slot_id = ?", "session-1", "draft_document").
		Order("revision ASC").Find(&revisions).Error; err != nil {
		t.Fatalf("list revisions: %v", err)
	}
	if len(revisions) != 2 || revisions[1].Revision != 4 || !revisions[1].Selected {
		t.Fatalf("unexpected revisions: %#v", revisions)
	}
	var artifacts []orm.WorkflowHumanArtifact
	if err := db.Find(&artifacts).Error; err != nil {
		t.Fatalf("list human artifacts: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].ContentType != "file" {
		t.Fatalf("unexpected human artifacts: %#v", artifacts)
	}
}
