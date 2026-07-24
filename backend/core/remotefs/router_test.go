package remotefs

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func newRemoteFSTestDB(t *testing.T) *orm.DB {
	t.Helper()
	db, err := orm.Connect(orm.DriverSQLite, filepath.Join(t.TempDir(), "remotefs.db"))
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(
		&orm.PersonalResource{},
		&orm.PersonalResourceBlob{},
		&orm.PersonalResourceRevision{},
		&orm.PersonalResourceDraft{},
		&orm.PersonalResourceReviewSession{},
		&orm.PersonalResourceReviewActionBatch{},
		&orm.PersonalResourceReviewActionItem{},
		&orm.PluginResource{},
		&orm.PluginBlob{},
		&orm.PluginRevision{},
		&orm.PluginRevisionEntry{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func TestPluginRevisionViewReadsPinnedContent(t *testing.T) {
	db := newRemoteFSTestDB(t)
	now := time.Now()
	hash := "hash-1"
	resource := orm.PluginResource{ID: "p1", PluginRef: "user:u1:demo", PluginID: "demo", OwnerUserID: "u1", OwnerScope: "u_x", RelativeRoot: "plugins/u_x/demo", HeadRevisionID: "r2", Version: 2, Status: "active", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&resource).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.PluginBlob{Hash: hash, Size: 2, Mime: "text/plain", FileType: "yaml", Content: []byte("v1"), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.PluginRevision{ID: "r1", PluginResourceID: "p1", RevisionNo: 1, TreeHash: "tree1", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.PluginRevision{ID: "r2", PluginResourceID: "p1", ParentRevisionID: "r1", RevisionNo: 2, TreeHash: "tree2", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.PluginRevisionEntry{RevisionID: "r1", Path: "plugin.yaml", EntryType: "file", BlobHash: &hash, Size: 2, Mime: "text/plain", FileType: "yaml", Mode: 420}).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/remote-fs/content?path=plugins/u_x/demo/plugin.yaml&user_id=u1&revision_id=r1", nil)
	rec := httptest.NewRecorder()
	NewHandler(db.DB).Content(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "v1" {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
	other := httptest.NewRequest(http.MethodGet, "/remote-fs/content?path=plugins/u_x/demo/plugin.yaml&user_id=u2&revision_id=r1", nil)
	otherRec := httptest.NewRecorder()
	NewHandler(db.DB).Content(otherRec, other)
	if otherRec.Code != http.StatusNotFound {
		t.Fatalf("cross-user read status=%d", otherRec.Code)
	}
}

func TestPersonalResourceTaskModes(t *testing.T) {
	db := newRemoteFSTestDB(t)
	handler := NewHandler(db.DB)

	writeReview := httptest.NewRequest(http.MethodPut, "/remote-fs/content?path=memory/memory.md&user_id=u1&task_id=review_1", strings.NewReader("review draft"))
	writeReviewRec := httptest.NewRecorder()
	handler.Content(writeReviewRec, writeReview)
	if writeReviewRec.Code != http.StatusOK {
		t.Fatalf("expected review write status 200, got %d body=%s", writeReviewRec.Code, writeReviewRec.Body.String())
	}

	readReview := httptest.NewRequest(http.MethodGet, "/remote-fs/content?path=memory/memory.md&user_id=u1&task_id=review_1", nil)
	readReviewRec := httptest.NewRecorder()
	handler.Content(readReviewRec, readReview)
	if readReviewRec.Code != http.StatusOK || readReviewRec.Body.String() != "review draft" {
		t.Fatalf("expected review read draft, got status=%d body=%q", readReviewRec.Code, readReviewRec.Body.String())
	}

	readEditor := httptest.NewRequest(http.MethodGet, "/remote-fs/content?path=memory/memory.md&user_id=u1&task_id=session_1", nil)
	readEditorRec := httptest.NewRecorder()
	handler.Content(readEditorRec, readEditor)
	if readEditorRec.Code != http.StatusOK || readEditorRec.Body.String() != "" {
		t.Fatalf("expected editor read head, got status=%d body=%q", readEditorRec.Code, readEditorRec.Body.String())
	}

	writeEditor := httptest.NewRequest(http.MethodPut, "/remote-fs/content?path=memory/memory.md&user_id=u1&task_id=session_1", strings.NewReader("editor draft"))
	writeEditorRec := httptest.NewRecorder()
	handler.Content(writeEditorRec, writeEditor)
	if writeEditorRec.Code != http.StatusConflict {
		t.Fatalf("expected editor write conflict, got %d body=%s", writeEditorRec.Code, writeEditorRec.Body.String())
	}
}
