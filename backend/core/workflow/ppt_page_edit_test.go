package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"
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
		&orm.WorkflowSession{ID: sid, CreateUserID: "ppt-owner", ConversationID: "ppt-chat", WorkflowID: "ppt-workflow", CreatedAt: now, UpdatedAt: now},
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
	save := func() (pptPageResult, error) {
		return savePPTPage(t.Context(), db.DB, sid, "ppt-owner", htmlIndex, edit)
	}
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
	var notifications int
	previousHook := OnAcademicRuntimeChanged
	OnAcademicRuntimeChanged = func(_ context.Context, _ *gorm.DB, sessionID string) {
		if sessionID == sid {
			notifications++
		}
	}
	t.Cleanup(func() { OnAcademicRuntimeChanged = previousHook })
	r, err := save()
	if err != nil || r.Revision != 2 || r.NotesRevision != 2 || notifications != 2 || len(r.Changes) != 2 || r.StateVersion == 0 {
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
	if r, err = save(); err != nil || r.Revision != 2 || notifications != 2 || len(r.Changes) != 0 {
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
	// Simulate a notes checkpoint whose content lives in object storage.
	var selected orm.WorkflowSlotRevision
	if err := db.Where("session_id = ? AND slot_id = ? AND selected = ?", sid, "preview_notes", true).First(&selected).Error; err != nil {
		t.Fatal(err)
	}
	originalFile := `{"type":"text","path":"files/original.md","size":10,"url":"stale"}`
	if err := db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", *selected.HumanArtifactID).Update("value", json.RawMessage(originalFile)).Error; err != nil {
		t.Fatal(err)
	}
	edit.Notes.Text = nil
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	dir := filepath.Join(root, "tmp", "users", "ppt-owner", "files", "upload-id")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(dir, "updated.md")
	if err := os.WriteFile(newPath, []byte("new notes"), 0600); err != nil {
		t.Fatal(err)
	}
	edit.Notes.File = &pptNotesFile{Path: newPath, Size: 9}
	if _, err := savePPTPage(t.Context(), db.DB, sid, "different-owner", htmlIndex, edit); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign notes upload: %v", err)
	}
	r, err = save()
	if err != nil || r.Revision != 3 || r.NotesRevision != 3 {
		t.Fatalf("file checkpoint: %+v %v", r, err)
	}
	var fileRevision orm.WorkflowSlotRevision
	if err := db.Where("session_id = ? AND slot_id = ? AND selected = ?", sid, "preview_notes", true).First(&fileRevision).Error; err != nil {
		t.Fatal(err)
	}
	value, err := LoadSlotRevisionValue(t.Context(), db.DB, fileRevision)
	var file map[string]any
	if err != nil || json.Unmarshal(value, &file) != nil || file["path"] != edit.Notes.File.Path || file["type"] != "text" || file["size"] != float64(9) || file["url"] != nil || file["text"] != nil {
		t.Fatalf("stored notes: %s, %v", value, err)
	}
	if _, err = RollbackSlotRevision(t.Context(), db.DB, sid, "preview_html", &htmlIndex, 2, "preview_html"); err != nil {
		t.Fatal(err)
	}
	assertSelected("preview_notes", notesIndex, 2, 3)
	if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "ppt-page-step").Update("status", StepStatusRunning).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = save(); !errors.Is(err, ErrConflict) {
		t.Fatalf("save during execution: %v", err)
	}
}
