package workflow

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestPPTPageCheckpointAndRollback(t *testing.T) {
	db := newHandlerTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowHumanArtifact{}); err != nil {
		t.Fatal(err)
	}
	const sid = "ppt-page-edit"
	const step = "generate_ppt"
	now := time.Now().UTC()
	for _, v := range []any{
		&orm.WorkflowSession{ID: sid, ConversationID: "ppt-chat", WorkflowID: "ppt-workflow", CreatedAt: now, UpdatedAt: now},
		&orm.WorkflowSessionStep{ID: "ppt-page-step", SessionID: sid, StepID: step, Attempt: 1, Status: StepStatusSucceeded},
	} {
		if err := db.Create(v).Error; err != nil {
			t.Fatal(err)
		}
	}
	htmlIndex, notesIndex := 2, 5
	for slot, index := range map[string]int{"preview_html": htmlIndex, "preview_notes": notesIndex} {
		order, _ := json.Marshal([]int{index})
		if err := db.Create(&orm.WorkflowSlotOrder{SessionID: sid, SlotID: slot, OrderList: order}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.WorkflowSlotRevision{ID: slot, SessionID: sid, SlotID: slot, Slot: slot, StepID: step, Attempt: 1, ListIndex: &index, Revision: 1, Selected: true, ChangeSource: "ai", ContentSnapshot: json.RawMessage(`{"text":"original"}`), CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	html, notes := "<html><body><h1>新标题</h1></body></html>", "新讲稿"
	edit := pptPageEdit{Slide: pptPageField{Revision: 1, Text: &html}, Notes: &pptPageField{ListIndex: &notesIndex, Revision: 1, Text: &notes}}
	save := func() (map[string]int, error) { return savePPTPage(t.Context(), db.DB, sid, htmlIndex, edit) }
	assertSelected := func(slot string, index, revision, count int) {
		t.Helper()
		versions, err := LoadSlotVersions(t.Context(), db.DB, sid, slot, &index)
		if err != nil || len(versions) != count {
			t.Fatalf("%s versions: %d, %v", slot, len(versions), err)
		}
		for _, v := range versions {
			if v.Selected != (v.Revision == revision) {
				t.Fatalf("%s unexpected selection %d", slot, v.Revision)
			}
		}
	}
	r, err := save()
	if err != nil || r["revision"] != 2 || r["notes_revision"] != 2 {
		t.Fatalf("save: %v %v", r, err)
	}
	assertSelected("preview_html", htmlIndex, 2, 2)
	assertSelected("preview_notes", notesIndex, 2, 2)
	if _, err = save(); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale save: %v", err)
	}
	one := int64(1)
	edit.Slide.Revision, edit.Notes.Revision = 2, 2
	edit.Slide.DraftVersion, edit.Notes.DraftVersion = &one, &one
	if r, err = save(); err != nil || r["revision"] != 2 {
		t.Fatalf("unchanged save: %v %v", r, err)
	}
	if _, err = RollbackSlotRevision(t.Context(), db.DB, sid, "preview_html", &htmlIndex, 1, "preview_html"); err != nil {
		t.Fatal(err)
	}
	assertSelected("preview_html", htmlIndex, 1, 2)
	assertSelected("preview_notes", notesIndex, 1, 2)
	if _, err = RollbackSlotRevision(t.Context(), db.DB, sid, "preview_html", &htmlIndex, 2, "preview_html"); err != nil {
		t.Fatal(err)
	}
	assertSelected("preview_notes", notesIndex, 2, 2)
	if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "ppt-page-step").Update("status", StepStatusRunning).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = save(); !errors.Is(err, ErrConflict) {
		t.Fatalf("save during execution: %v", err)
	}
}
