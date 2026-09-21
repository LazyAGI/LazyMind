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
	first := authoringDiagnosticsForDraft(t.Context(), db.DB, draft)
	second := authoringDiagnosticsForDraft(t.Context(), db.DB, draft)
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

func newPinnedSkillDraftForFinalize(t *testing.T, db *orm.DB, draftID string) orm.WorkflowDraft {
	t.Helper()
	now := time.Now().UTC()
	draft := orm.WorkflowDraft{
		ID: draftID, Name: "Search Draft", CreatedBy: "user-1", Version: 1,
		SourceType: "skill", SourceSkillID: "skill-1", SourceSkillName: "demo-skill",
		SourceSkillRevisionID: "skill-1-rev", SourceSkillRevisionNo: 1, SourceSkillTreeHash: "tree-skill-1",
		WorkflowYAMLContent: "id: search-report\nname: Search Report\nslots:\n  - id: report\nsteps:\n  - id: search\n    label: Search\n",
		StateYAMLContent:    "transitions:\n  __start__: [{to: search}]\n  search: [{to: __end__}]\nsteps:\n  search:\n    prompt: Search the web and write the report.\n    outputs: [report]\n",
		ScenarioContent:     "# Search Report\n\nThe search step writes the report.\n",
		ScriptsContent:      "{}",
		CreatedAt:           now, UpdatedAt: now,
	}
	if err := db.Create(&draft).Error; err != nil {
		t.Fatal(err)
	}
	return draft
}

func TestValidateFinalizesInMemoryWithoutMovingDraftVersion(t *testing.T) {
	db := newHandlerTestDB(t)
	seedSkillForWorkflowConversion(t, db, "user-1", "skill-1", "# Search Skill\nUse web search to gather current sources and write a concise report.")
	stored := newPinnedSkillDraftForFinalize(t, db, "draft-1")
	req := httptest.NewRequest(http.MethodPost, "/drafts/draft-1:validate", nil)
	req = mux.SetURLVars(req, map[string]string{"draft_id": "draft-1"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	ValidateWorkflowDraft(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("validate status=%d body=%s", rec.Code, rec.Body.String())
	}
	var reloaded orm.WorkflowDraft
	if err := db.Where("id=?", "draft-1").First(&reloaded).Error; err != nil {
		t.Fatal(err)
	}
	// A read-only endpoint must not invalidate the caller's optimistic-lock version.
	if reloaded.Version != stored.Version || reloaded.StateYAMLContent != stored.StateYAMLContent {
		t.Fatalf("validate persisted finalization: version=%d state=\n%s", reloaded.Version, reloaded.StateYAMLContent)
	}
	finalized, _ := finalizedAuthoringDraft(t.Context(), db.DB, reloaded)
	for _, want := range []string{"web_search", "Workflow execution boundaries:", "Search budget:"} {
		if !strings.Contains(finalized.StateYAMLContent, want) {
			t.Fatalf("in-memory finalization missing %q:\n%s", want, finalized.StateYAMLContent)
		}
	}
}

func TestFinalizePersistsPinnedSkillDraftWithoutGenerationAnalysis(t *testing.T) {
	db := newHandlerTestDB(t)
	seedSkillForWorkflowConversion(t, db, "user-1", "skill-1", "# Search Skill\nUse web search to gather current sources and write a concise report.")
	draft := newPinnedSkillDraftForFinalize(t, db, "draft-1")
	final, err := finalizeAuthoringWorkflowDraft(t.Context(), db.DB, &draft)
	if err != nil {
		t.Fatal(err)
	}
	if !final.Changed {
		t.Fatal("finalization should rewrite a draft that lacks required capabilities")
	}
	var updated orm.WorkflowDraft
	if err := db.Where("id=?", "draft-1").First(&updated).Error; err != nil {
		t.Fatal(err)
	}
	if updated.SourceAnalysisID != "" {
		t.Fatalf("finalize should not require generation analysis, got %q", updated.SourceAnalysisID)
	}
	for _, want := range []string{"web_search", "Workflow execution boundaries:", "Search budget:"} {
		if !strings.Contains(updated.StateYAMLContent, want) {
			t.Fatalf("state yaml missing %q:\n%s", want, updated.StateYAMLContent)
		}
	}
	if updated.Version != 2 || draft.Version != 2 {
		t.Fatalf("stored version=%d in-memory version=%d, want 2", updated.Version, draft.Version)
	}
	if _, err := finalizeAuthoringWorkflowDraft(t.Context(), db.DB, &draft); err != nil {
		t.Fatal(err)
	}
	if draft.Version != 2 {
		t.Fatalf("finalization is not idempotent, version=%d", draft.Version)
	}
}

func TestDeterministicFinalizeIsIndependentOfGenerationAnalysis(t *testing.T) {
	baseWorkflow := "id: search-report\nname: Search Report\nslots:\n  - id: sources\n  - id: report\nsteps:\n  - id: search\n    label: Search\n  - id: summarize\n    label: Summarize\n"
	baseState := "transitions:\n  __start__: [{to: search}]\n  search: [{to: summarize}]\n  summarize: [{to: __end__}]\nsteps:\n  search:\n    prompt: Search the web and collect sources.\n    outputs: [sources]\n    acceptance_criteria: [At least three distinct sources are collected.]\n  summarize:\n    prompt: Write the concise report.\n    outputs: [report]\n    acceptance_criteria: [Every claim cites a collected source.]\n"
	baseScenario := "# Search Report\n\nSearch collects sources, then summarize writes the report and must cite sources.\n"
	build := func(draftID string, withAnalysis bool) orm.WorkflowDraft {
		db := newHandlerTestDB(t)
		seedSkillForWorkflowConversion(t, db, "user-1", "skill-1", "# Search Skill\nUse web search to gather current sources and write a concise report.")
		now := time.Now().UTC()
		sourceAnalysisID := ""
		if withAnalysis {
			sourceAnalysisID = "analysis-1"
			mappingJSON := `{"capability:web_search":{"action":"require","required":true,"capability":"web_search","workflow_capability":"web_search","framework_tool":"web_search","workflow_tools":["web_search"],"available":true,"label":"Web Search","reason":"Skill requires web search.","source":"deterministic_skill_capability_scan"}}`
			analysis := orm.WorkflowGenerationAnalysis{
				ID: sourceAnalysisID, DraftID: draftID, UserID: "user-1", SourceType: "skill",
				SourceSkillID: "skill-1", SourceSkillRevisionID: "skill-1-rev", SourceSkillTreeHash: "tree-skill-1",
				Status: "generatable", ToolMappingReportJSON: mappingJSON, CreatedAt: now, UpdatedAt: now,
			}
			if err := db.Create(&analysis).Error; err != nil {
				t.Fatal(err)
			}
		}
		draft := orm.WorkflowDraft{
			ID: draftID, Name: draftID, CreatedBy: "user-1", Version: 1,
			SourceType: "skill", SourceSkillID: "skill-1", SourceSkillName: "demo-skill",
			SourceSkillRevisionID: "skill-1-rev", SourceSkillRevisionNo: 1, SourceSkillTreeHash: "tree-skill-1",
			SourceAnalysisID:    sourceAnalysisID,
			WorkflowYAMLContent: baseWorkflow,
			StateYAMLContent:    baseState,
			ScenarioContent:     baseScenario,
			ScriptsContent:      "{}",
			CreatedAt:           now,
			UpdatedAt:           now,
		}
		if err := db.Create(&draft).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := finalizeAuthoringWorkflowDraft(t.Context(), db.DB, &draft); err != nil {
			t.Fatal(err)
		}
		return draft
	}
	// analysisDraft mirrors a UI-generated draft; snapshotDraft mirrors an MCP draft
	// authored without any generation analysis.
	analysisDraft := build("ui-draft", true)
	snapshotDraft := build("mcp-draft", false)
	for _, draft := range []orm.WorkflowDraft{analysisDraft, snapshotDraft} {
		if draft.SourceSkillRevisionID != "skill-1-rev" || draft.SourceSkillTreeHash != "tree-skill-1" {
			t.Fatalf("source binding changed: %#v", draft)
		}
		wanted := []string{
			"search:", "summarize:", "outputs:", "sources", "report", "web_search",
			"acceptance_criteria:", "At least three distinct sources are collected.",
			"Every claim cites a collected source.",
			"Workflow execution boundaries:", "Search budget:",
		}
		for _, want := range wanted {
			if !strings.Contains(draft.StateYAMLContent, want) {
				t.Fatalf("%s state yaml missing %q:\n%s", draft.ID, want, draft.StateYAMLContent)
			}
		}
		if !strings.Contains(draft.WorkflowYAMLContent, "step_id: search") || !strings.Contains(draft.WorkflowYAMLContent, "step_id: summarize") || !strings.Contains(draft.WorkflowYAMLContent, "slots:") {
			t.Fatalf("%s workflow yaml missing UI tab alignment:\n%s", draft.ID, draft.WorkflowYAMLContent)
		}
	}
	if analysisDraft.WorkflowYAMLContent != snapshotDraft.WorkflowYAMLContent ||
		analysisDraft.StateYAMLContent != snapshotDraft.StateYAMLContent {
		t.Fatalf("finalization diverged with and without generation analysis:\nwith workflow:\n%s\nwithout workflow:\n%s\nwith state:\n%s\nwithout state:\n%s",
			analysisDraft.WorkflowYAMLContent, snapshotDraft.WorkflowYAMLContent,
			analysisDraft.StateYAMLContent, snapshotDraft.StateYAMLContent)
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
