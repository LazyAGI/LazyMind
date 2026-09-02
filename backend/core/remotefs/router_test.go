package remotefs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	skilltestutil "lazymind/core/skillv2/testutil"
)

func newRemoteFSTestDB(t *testing.T) *orm.DB {
	t.Helper()
	return orm.MigrateTestDB(t,
		&orm.MemoryCurrentEntry{},
		&orm.ResourceUpdateTask{},
		&orm.WorkflowResource{},
		&orm.WorkflowBlob{},
		&orm.WorkflowRevision{},
		&orm.WorkflowRevisionEntry{},
	)
}

func TestRequireInternalServiceToken(t *testing.T) {
	t.Run("disabled when not configured", func(t *testing.T) {
		t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "")
		req := httptest.NewRequest(http.MethodGet, "/remote-fs/list", nil)
		rec := httptest.NewRecorder()

		if !requireInternalServiceToken(rec, req) {
			t.Fatal("token check should be disabled when no token is configured")
		}
	})

	t.Run("rejects missing or incorrect token", func(t *testing.T) {
		t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal-secret")
		for _, provided := range []string{"", "wrong-secret"} {
			req := httptest.NewRequest(http.MethodGet, "/remote-fs/list", nil)
			req.Header.Set("X-LazyMind-Internal-Token", provided)
			rec := httptest.NewRecorder()

			if requireInternalServiceToken(rec, req) {
				t.Fatalf("token check unexpectedly accepted %q", provided)
			}
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
		}
	})

	t.Run("accepts configured token", func(t *testing.T) {
		t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal-secret")
		req := httptest.NewRequest(http.MethodGet, "/remote-fs/list", nil)
		req.Header.Set("X-LazyMind-Internal-Token", "internal-secret")
		rec := httptest.NewRecorder()

		if !requireInternalServiceToken(rec, req) {
			t.Fatalf("configured token was rejected: status=%d body=%s", rec.Code, rec.Body.String())
		}
	})
}

func TestWorkflowRevisionViewReadsPinnedContent(t *testing.T) {
	db := newRemoteFSTestDB(t)
	now := time.Now()
	hash := "hash-1"
	resource := orm.WorkflowResource{ID: "p1", WorkflowRef: "user:u1:demo", WorkflowID: "demo", OwnerUserID: "u1", OwnerScope: "u_x", RelativeRoot: "workflows/u_x/demo", HeadRevisionID: "r2", Version: 2, Status: "active", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&resource).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowBlob{Hash: hash, Size: 2, Mime: "text/plain", FileType: "yaml", Content: []byte("v1"), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowRevision{ID: "r1", WorkflowResourceID: "p1", RevisionNo: 1, TreeHash: "tree1", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowRevision{ID: "r2", WorkflowResourceID: "p1", ParentRevisionID: "r1", RevisionNo: 2, TreeHash: "tree2", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowRevisionEntry{RevisionID: "r1", Path: "workflow.yaml", EntryType: "file", BlobHash: &hash, Size: 2, Mime: "text/plain", FileType: "yaml", Mode: 420}).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/remote-fs/content?path=workflows/u_x/demo/workflow.yaml&user_id=u1&revision_id=r1", nil)
	rec := httptest.NewRecorder()
	NewHandler(db.DB).Content(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "v1" {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
	other := httptest.NewRequest(http.MethodGet, "/remote-fs/content?path=workflows/u_x/demo/workflow.yaml&user_id=u2&revision_id=r1", nil)
	otherRec := httptest.NewRecorder()
	NewHandler(db.DB).Content(otherRec, other)
	if otherRec.Code != http.StatusNotFound {
		t.Fatalf("cross-user read status=%d", otherRec.Code)
	}
}

func TestMemoryCurrentStateDoesNotUseTaskDraftModes(t *testing.T) {
	db := newRemoteFSTestDB(t)
	handler := NewHandler(db.DB)
	memoryPath := "memory/users/references/direct.md"
	reviewContent := validMemoryReferenceYAML
	editorContent := strings.Replace(
		validMemoryReferenceYAML,
		"Explain motivations and tradeoffs.",
		"Use concise answers.",
		1,
	)

	writeReview := httptest.NewRequest(
		http.MethodPut,
		"/remote-fs/content?path="+memoryPath+"&user_id=u1&task_id=memory_review_1",
		strings.NewReader(reviewContent),
	)
	writeReviewRec := httptest.NewRecorder()
	handler.Content(writeReviewRec, writeReview)
	if writeReviewRec.Code != http.StatusOK {
		t.Fatalf("expected review write status 200, got %d body=%s", writeReviewRec.Code, writeReviewRec.Body.String())
	}

	readReview := httptest.NewRequest(http.MethodGet, "/remote-fs/content?path="+memoryPath+"&user_id=u1&task_id=memory_review_1", nil)
	readReviewRec := httptest.NewRecorder()
	handler.Content(readReviewRec, readReview)
	if readReviewRec.Code != http.StatusOK || readReviewRec.Body.String() != reviewContent {
		t.Fatalf("expected review read draft, got status=%d body=%q", readReviewRec.Code, readReviewRec.Body.String())
	}

	readEditor := httptest.NewRequest(http.MethodGet, "/remote-fs/content?path="+memoryPath+"&user_id=u1&task_id=session_1", nil)
	readEditorRec := httptest.NewRecorder()
	handler.Content(readEditorRec, readEditor)
	if readEditorRec.Code != http.StatusOK || readEditorRec.Body.String() != reviewContent {
		t.Fatalf("expected current-state content, got status=%d body=%q", readEditorRec.Code, readEditorRec.Body.String())
	}

	writeEditor := httptest.NewRequest(
		http.MethodPut,
		"/remote-fs/content?path="+memoryPath+"&user_id=u1&task_id=session_1",
		strings.NewReader(editorContent),
	)
	writeEditorRec := httptest.NewRecorder()
	handler.Content(writeEditorRec, writeEditor)
	if writeEditorRec.Code != http.StatusOK {
		t.Fatalf("expected direct current-state write, got %d body=%s", writeEditorRec.Code, writeEditorRec.Body.String())
	}

	readLatest := httptest.NewRequest(http.MethodGet, "/remote-fs/content?path="+memoryPath+"&user_id=u1", nil)
	readLatestRec := httptest.NewRecorder()
	handler.Content(readLatestRec, readLatest)
	if readLatestRec.Code != http.StatusOK || readLatestRec.Body.String() != editorContent {
		t.Fatalf("expected latest current-state content, got status=%d body=%q", readLatestRec.Code, readLatestRec.Body.String())
	}
}

func TestRouterKeepsSkillWritesInTaskOwnedDrafts(t *testing.T) {
	db := skilltestutil.NewTestDB(t)
	t.Setenv("LAZYMIND_SKILL_OBJECT_ROOT", t.TempDir())
	handler := NewHandler(db.DB)

	createReq := httptest.NewRequest(
		http.MethodPost,
		"/remote-fs/dir?user_id=user_001&task_id=skill-task",
		strings.NewReader(`{"path":"skills/research/router-draft","recursive":true}`),
	)
	createRec := httptest.NewRecorder()
	handler.Dir(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create skill draft status=%d body=%s", createRec.Code, createRec.Body.String())
	}

	var skill skilltestutil.SkillRow
	if err := db.Where(
		"owner_user_id = ? AND relative_root = ?",
		"user_001",
		"research/router-draft",
	).Take(&skill).Error; err != nil {
		t.Fatalf("query skill draft package: %v", err)
	}
	if skill.HeadRevisionID != nil {
		t.Fatalf("new skill unexpectedly committed revision %q", *skill.HeadRevisionID)
	}
	var draft skilltestutil.SkillDraftRow
	if err := db.Where("skill_id = ?", skill.ID).Take(&draft).Error; err != nil {
		t.Fatalf("query routed skill draft: %v", err)
	}
	if draft.TaskID != "skill-task" || draft.DraftStatus != "pending_confirm" {
		t.Fatalf(
			"routed skill draft task/status = %q/%q",
			draft.TaskID,
			draft.DraftStatus,
		)
	}
	if got := skilltestutil.CountRows(
		t,
		db,
		"skill_revisions",
		"skill_id = ?",
		skill.ID,
	); got != 0 {
		t.Fatalf("routed skill revision count=%d, want 0 before approval", got)
	}
}
