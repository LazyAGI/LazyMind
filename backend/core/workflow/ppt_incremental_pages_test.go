package workflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func TestPPTInsertionCarriesForwardOnlyVisibleUnselectedPages(t *testing.T) {
	db := newHandlerTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowHumanArtifact{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	session := orm.WorkflowSession{ID: "ppt-insert", WorkflowID: "ppt-workflow", Status: SessionStatusWaiting, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	step := orm.WorkflowSessionStep{ID: "new-step", SessionID: session.ID, StepID: "generate_ppt", Attempt: 2, TaskID: "new-task", Status: StepStatusRunning, Validity: "effective", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&step).Error; err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"stale-visible", "selected-edit", "deleted-page"} {
		artID := "artifact-" + id
		value := json.RawMessage(`{"text":"unchanged ` + id + `"}`)
		if err := db.Create(&orm.WorkflowHumanArtifact{ID: artID, SessionID: session.ID, Slot: "preview_html", ContentType: "text", Value: value, DraftVersion: 1, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		rev := orm.WorkflowSlotRevision{ID: id, SessionID: session.ID, SlotID: "preview_html", Slot: "preview_html", ListIndex: &i, Revision: 1, Selected: true, HumanArtifactID: &artID, StepID: "generate_ppt", Attempt: 1, Validity: "effective", CreatedAt: now}
		if err := db.Create(&rev).Error; err != nil {
			t.Fatal(err)
		}
		if i != 1 {
			if err := db.Model(&rev).Updates(map[string]any{"selected": false, "validity": "stale"}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := db.Create(&orm.WorkflowSlotOrder{SessionID: session.ID, SlotID: "preview_html", OrderList: json.RawMessage(`[0,1]`), UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", jsonBody(`{"value":{"text":"new page"},"content_type":"text","insert_before":2}`))
	req = mux.SetURLVars(req, map[string]string{"session_id": session.ID, "slot_id": "preview_html"})
	rec := httptest.NewRecorder()
	CreateSlotItem(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("insert: %d %s", rec.Code, rec.Body.String())
	}
	var revisions []orm.WorkflowSlotRevision
	if err := db.Where("session_id = ? AND slot_id = ? AND selected = ?", session.ID, "preview_html", true).Order("list_index").Find(&revisions).Error; err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 3 {
		t.Fatalf("selected count=%d, want 3", len(revisions))
	}
	if revisions[0].Revision != 2 || revisions[0].Attempt != 2 || revisions[0].Validity != "effective" {
		t.Fatalf("carried page: %+v", revisions[0])
	}
	if revisions[1].ID != "selected-edit" {
		t.Fatal("selected user edit was replaced")
	}
	value, err := LoadSlotRevisionValue(context.Background(), db.DB, revisions[0])
	if err != nil {
		t.Fatal(err)
	}
	var content map[string]any
	if err := json.Unmarshal(value, &content); err != nil {
		t.Fatal(err)
	}
	if content["text"] != "unchanged stale-visible" {
		t.Fatalf("content changed: %s", value)
	}
	// This is the lookup used by AI preview: the old page is now addressable.
	if _, _, _, err := loadSelectedArtifactValue(context.Background(), db.DB, session.ID, "preview_html", revisions[0].ListIndex); err != nil {
		t.Fatal(err)
	}
	order, err := GetSlotOrder(context.Background(), db.DB, session.ID, "preview_html")
	if err != nil {
		t.Fatal(err)
	}
	var indices []int
	_ = json.Unmarshal(order.OrderList, &indices)
	if len(indices) != 3 || indices[0] != 0 || indices[1] != 3 || indices[2] != 1 {
		t.Fatalf("wrong insertion order: %v", indices)
	}
}

func TestPPTCarryForwardScope(t *testing.T) {
	for _, tc := range []struct {
		workflow, slot string
		want           bool
	}{
		{"ppt-workflow", "preview_html", true}, {"ppt-workflow", "preview_notes", true},
		{"ppt-workflow", "deck_outline", false}, {"ai-writer", "preview_html", false},
	} {
		if got := isPPTPreviewSlot(&orm.WorkflowSession{WorkflowID: tc.workflow}, tc.slot); got != tc.want {
			t.Fatalf("scope %+v = %v", tc, got)
		}
	}
}

func TestPPTCarryForwardRollsBackWhenHistoricalPageIsMissing(t *testing.T) {
	db := newHandlerTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowHumanArtifact{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	session := orm.WorkflowSession{ID: "rollback-ppt", WorkflowID: "ppt-workflow", Status: SessionStatusWaiting, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		rev := orm.WorkflowSlotRevision{ID: []string{"good", "missing"}[i], SessionID: session.ID, SlotID: "preview_html", Slot: "preview_html", ListIndex: &i, Revision: 1, Validity: "stale", Selected: true, StepID: "generate_ppt", Attempt: 1, CreatedAt: now}
		if i == 0 {
			rev.ContentSnapshot = json.RawMessage(`{"text":"keep"}`)
		}
		if err := db.Create(&rev).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&rev).Update("selected", false).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&orm.WorkflowSlotOrder{SessionID: session.ID, SlotID: "preview_html", OrderList: json.RawMessage(`[0,1]`), UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		return carryForwardPPTPages(context.Background(), tx, &session, "preview_html", "generate_ppt", 2)
	})
	if err == nil {
		t.Fatal("expected missing historical page error")
	}
	var count int64
	if err := db.Model(&orm.WorkflowSlotRevision{}).Where("session_id = ? AND revision > 1", session.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("partially committed %d revisions", count)
	}
}
