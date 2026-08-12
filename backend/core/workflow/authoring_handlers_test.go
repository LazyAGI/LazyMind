package workflow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
)

func seedAuthoringSkill(t *testing.T, db *orm.DB) {
	t.Helper()
	if err := db.AutoMigrate(
		&orm.SkillV2Skill{}, &orm.SkillV2Revision{},
		&orm.SkillV2RevisionEntry{}, &orm.SkillV2Blob{},
	); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	revisionID := "revision-1"
	blobHash := "blob-1"
	rows := []any{
		&orm.SkillV2Skill{ID: "skill-1", OwnerUserID: "user-1", CreateUserID: "user-1",
			Category: "test", SkillName: "Pinned Skill", RelativeRoot: "test/pinned-skill",
			HeadRevisionID: &revisionID, CreatedAt: now, UpdatedAt: now},
		&orm.SkillV2Revision{ID: revisionID, SkillID: "skill-1", RevisionNo: 1,
			TreeHash: "tree-fixed", CreatedAt: now},
		&orm.SkillV2Blob{Hash: blobHash, Size: 14, Mime: "text/markdown", FileType: "markdown",
			StorageBackend: "database", Content: []byte("# Pinned Skill"), CreatedAt: now},
		&orm.SkillV2RevisionEntry{RevisionID: revisionID, Path: "SKILL.md", EntryType: "file",
			BlobHash: &blobHash, Size: 14, Mime: "text/markdown", FileType: "markdown"},
	}
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestAuthoringFixtureAndLazyMindDraftShareDeterministicDiagnostics(t *testing.T) {
	db := newHandlerTestDB(t)
	seedAuthoringSkill(t, db)
	fixtureReq := httptest.NewRequest(http.MethodGet, "/workflow-authoring/v1/fixture?tree_hash=tree-fixed", nil)
	fixtureRec := httptest.NewRecorder()
	GenerateAuthoringFixture(fixtureRec, fixtureReq)
	var fixtureEnvelope struct {
		Data struct {
			Files map[string]string `json:"files"`
		} `json:"data"`
	}
	if err := json.Unmarshal(fixtureRec.Body.Bytes(), &fixtureEnvelope); err != nil {
		t.Fatal(err)
	}
	filesJSON, _ := json.Marshal(fixtureEnvelope.Data.Files)
	createBody := `{"name":"Fixture","skill_id":"skill-1","revision_id":"revision-1","tree_hash":"tree-fixed","files":` + string(filesJSON) + `}`
	createReq := httptest.NewRequest(http.MethodPost, "/workflow-authoring/v1/drafts", strings.NewReader(createBody))
	createReq.Header.Set("X-User-Id", "user-1")
	createRec := httptest.NewRecorder()
	CreateAuthoringWorkflowDraft(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create=%d %s", createRec.Code, createRec.Body.String())
	}
	var draft orm.WorkflowDraft
	if err := db.Where("created_by=?", "user-1").First(&draft).Error; err != nil {
		t.Fatal(err)
	}
	first := authoringDiagnosticsForDraft(db.DB, draft)
	second := authoringDiagnosticsForDraft(db.DB, draft)
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) || !first.Valid {
		t.Fatalf("diagnostics not deterministic/valid: %s vs %s", a, b)
	}
	if draft.SourceSkillRevisionID != "revision-1" || draft.SourceSkillTreeHash != "tree-fixed" {
		t.Fatalf("snapshot not fixed: %#v", draft)
	}
}

func TestAuthoringFileUpdateUsesOptimisticVersion(t *testing.T) {
	db := newHandlerTestDB(t)
	now := time.Now().UTC()
	draft := orm.WorkflowDraft{ID: "draft-1", Name: "Draft", CreatedBy: "user-1", Version: 1, ScriptsContent: "{}", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&draft).Error; err != nil {
		t.Fatal(err)
	}
	call := func(version int) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/workflow-authoring/v1/drafts/draft-1/files", strings.NewReader(`{"path":"workflow.yaml","content":"id: fixed","expected_version":`+strconv.Itoa(version)+`}`))
		req.Header.Set("X-User-Id", "user-1")
		req = mux.SetURLVars(req, map[string]string{"draft_id": "draft-1"})
		rec := httptest.NewRecorder()
		UpdateAuthoringWorkflowDraftFile(rec, req)
		return rec
	}
	if got := call(1); got.Code != http.StatusOK {
		t.Fatalf("update=%d %s", got.Code, got.Body.String())
	}
	if got := call(1); got.Code != http.StatusConflict {
		t.Fatalf("stale update=%d %s", got.Code, got.Body.String())
	}
}

func TestApplyAuthoringFilesMergesIndividualScriptUpdates(t *testing.T) {
	draft := orm.WorkflowDraft{ScriptsContent: `{"scripts/first.py":"def first(): return 1"}`}

	applyAuthoringFiles(&draft, map[string]string{
		"scripts/second.py": "def second(): return 2",
	})

	var scripts map[string]string
	if err := json.Unmarshal([]byte(draft.ScriptsContent), &scripts); err != nil {
		t.Fatal(err)
	}
	if len(scripts) != 2 || scripts["scripts/first.py"] == "" || scripts["scripts/second.py"] == "" {
		t.Fatalf("script update replaced package contents: %#v", scripts)
	}
}

func TestAuthoringSourceContainsNoModelInvocation(t *testing.T) {
	data, err := os.ReadFile("authoring_handlers.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{"core/algo", "modelconfig", "http://chat", "GenerateWorkflowStaged"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("implicit model dependency %q", forbidden)
		}
	}
}
