package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/workflow/graphengine"
)

// newHandlerTestDB creates a SQLite DB with all models needed by HTTP handlers.
func newHandlerTestDB(t *testing.T) *orm.DB {
	t.Helper()
	db := newTestDB(t)
	if err := db.AutoMigrate(
		&orm.WorkflowDraft{},
		&orm.WorkflowResource{},
		&orm.WorkflowRevision{},
		&orm.WorkflowRevisionEntry{},
		&orm.WorkflowBlob{},
		&orm.UserWorkflowSetting{},
		&orm.AsyncJob{},
		&orm.WorkflowGenerationAnalysis{},
		&orm.WorkflowRepairRun{},
		&orm.SkillV2Skill{},
		&orm.SkillV2Revision{},
		&orm.SkillV2RevisionEntry{},
		&orm.SkillV2Blob{},
		&orm.UserUIPreferences{},
	); err != nil {
		t.Fatalf("auto migrate handler models: %v", err)
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	return db
}

// seedWorkflowDraft inserts a draft with valid YAML for validation tests.
func seedWorkflowDraft(t *testing.T, db *orm.DB, draftID, userID string) {
	t.Helper()
	now := time.Now().UTC()
	db.DB.Create(&orm.WorkflowDraft{
		ID: draftID, WorkflowID: "test-plugin", Name: "Test Workflow",
		CreatedBy: userID, Version: 1,
		WorkflowYAMLContent: "id: test-plugin\nslots:\n  - {id: out}\nsteps:\n  - {id: step_a, label: \"Do work\"}",
		StateYAMLContent:    "transitions:\n  __start__: [{to: __end__}]",
		ScenarioContent:     "",
		ScriptsContent:      "{}",
		CreatedAt:           now, UpdatedAt: now,
	})
}

// seedWorkflowResource inserts a minimal plugin resource for settings tests.
func seedWorkflowResource(t *testing.T, db *orm.DB, workflowRef, workflowID, userID string) {
	t.Helper()
	now := time.Now().UTC()
	db.DB.Create(&orm.WorkflowResource{
		WorkflowRef: workflowRef, WorkflowID: workflowID, Name: "Test Workflow",
		OwnerUserID: userID, Status: "active", RelativeRoot: workflowRef,
		HeadRevisionID: "rev-1", Version: 1,
		CreatedAt: now, UpdatedAt: now,
	})
}

// jsonBody returns an io.Reader for a JSON string.
func jsonBody(s string) io.Reader {
	return strings.NewReader(s)
}

func seedSkillForWorkflowConversion(t *testing.T, db *orm.DB, userID, skillID, skillMD string) {
	t.Helper()
	now := time.Now().UTC()
	revisionID := skillID + "-rev"
	hash := sha256.Sum256([]byte(skillMD))
	blobHash := hex.EncodeToString(hash[:])
	if err := db.Create(&orm.SkillV2Skill{
		ID: skillID, OwnerUserID: userID, CreateUserID: userID,
		Category: "writing", SkillName: "demo-skill", RelativeRoot: "skills/writing/demo-skill",
		HeadRevisionID: &revisionID, Version: 1, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.SkillV2Revision{
		ID: revisionID, SkillID: skillID, RevisionNo: 1, TreeHash: "tree-" + skillID, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.SkillV2Blob{
		Hash: blobHash, Size: int64(len(skillMD)), Mime: "text/markdown", FileType: "markdown",
		StorageBackend: "database", Content: []byte(skillMD), CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.SkillV2RevisionEntry{
		RevisionID: revisionID, Path: "SKILL.md", EntryType: "file", BlobHash: &blobHash,
		Size: int64(len(skillMD)), Mime: "text/markdown", FileType: "markdown", Mode: 0o644,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func addSkillRevisionFileForWorkflowConversion(t *testing.T, db *orm.DB, revisionID, path, content, fileType string) {
	t.Helper()
	now := time.Now().UTC()
	hash := sha256.Sum256([]byte(content))
	blobHash := hex.EncodeToString(hash[:])
	if err := db.Create(&orm.SkillV2Blob{
		Hash: blobHash, Size: int64(len(content)), Mime: "text/plain", FileType: fileType,
		StorageBackend: "database", Content: []byte(content), CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.SkillV2RevisionEntry{
		RevisionID: revisionID, Path: path, EntryType: "file", BlobHash: &blobHash,
		Size: int64(len(content)), Mime: "text/plain", FileType: fileType, Mode: 0o644,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowRefPathVarPreservesUserWorkflowID(t *testing.T) {
	ref := "user:user-1:ppt-workflow-copy"
	req := httptest.NewRequest(http.MethodGet, "/published-workflows/"+ref+"/versions", nil)
	req = mux.SetURLVars(req, map[string]string{"workflow_ref": ref})
	if got := workflowRefPathVar(req); got != ref {
		t.Fatalf("workflow ref = %q, want %q", got, ref)
	}

	actionReq := httptest.NewRequest(http.MethodPost, "/published-workflows/"+ref+":rollback", nil)
	actionReq = mux.SetURLVars(actionReq, map[string]string{"workflow_ref": ref + ":rollback"})
	if got := workflowRefPathVar(actionReq); got != ref {
		t.Fatalf("rollback workflow ref = %q, want %q", got, ref)
	}
}

// testError2 is a simple error for testing error-matching functions.
type testError2 struct{ msg string }

func (e *testError2) Error() string { return e.msg }

// --- ValidateWorkflowDraft ---

func TestValidateWorkflowDraft_NotFound(t *testing.T) {
	newHandlerTestDB(t)
	req := httptest.NewRequest(http.MethodPost, "/drafts/nonexistent/validate", nil)
	req = mux.SetURLVars(req, map[string]string{"draft_id": "nonexistent"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	ValidateWorkflowDraft(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestCopyWorkflowDraftCopiesEditableContentWithNewIdentity(t *testing.T) {
	db := newHandlerTestDB(t)
	now := time.Now().UTC()
	source := orm.WorkflowDraft{
		ID: "source-draft", WorkflowID: "research", Name: "Research", CreatedBy: "user-1", Version: 4,
		WorkflowYAMLContent: "id: research\nname: Research\ndescription: original\n", StateYAMLContent: "states: {}\n",
		StateLayoutContent: `{"nodes":{"start":{"x":10}}}`, ScenarioContent: "# Scenario", DriverContent: "# Driver",
		ScriptsContent: `{"scripts/run.py":"print('ok')"}`, DesignBriefContent: "# Brief", SourceType: "ai",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&source).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/workflow-drafts/source-draft:copy", strings.NewReader(`{"name":"Research 副本"}`))
	req = mux.SetURLVars(req, map[string]string{"draft_id": source.ID})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	CopyWorkflowDraft(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("copy status=%d body=%s", rec.Code, rec.Body.String())
	}
	var copied orm.WorkflowDraft
	if err := db.Where("created_by = ? AND plugin_id = ?", "user-1", "research-copy").First(&copied).Error; err != nil {
		t.Fatalf("load copy: %v", err)
	}
	if copied.ID == source.ID || copied.Name != "Research 副本" || copied.SourceType != "blank" || copied.Version != 1 {
		t.Fatalf("copy metadata = %#v", copied)
	}
	if copied.StateYAMLContent != source.StateYAMLContent || copied.StateLayoutContent != source.StateLayoutContent ||
		copied.ScenarioContent != source.ScenarioContent || copied.DriverContent != source.DriverContent ||
		copied.ScriptsContent != source.ScriptsContent || copied.DesignBriefContent != source.DesignBriefContent {
		t.Fatalf("copy did not retain all editable content: %#v", copied)
	}
	if !strings.Contains(copied.WorkflowYAMLContent, "id: research-copy") || !strings.Contains(copied.WorkflowYAMLContent, "description: original") {
		t.Fatalf("copy workflow yaml = %s", copied.WorkflowYAMLContent)
	}
}

func TestValidateWorkflowDraft_ValidDraft(t *testing.T) {
	db := newHandlerTestDB(t)
	seedWorkflowDraft(t, db, "draft-1", "user-1")
	req := httptest.NewRequest(http.MethodPost, "/drafts/draft-1/validate", nil)
	req = mux.SetURLVars(req, map[string]string{"draft_id": "draft-1"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	ValidateWorkflowDraft(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	data, _ := resp["data"].(map[string]any)
	if data == nil || data["valid"] == nil {
		t.Fatalf("expected valid field in response: %s", rec.Body.String())
	}
}

func TestPreflightSkillWorkflowConversionWarnsOnMissingDependency(t *testing.T) {
	db := newHandlerTestDB(t)
	seedSkillForWorkflowConversion(t, db, "user-1", "skill-1", "# Demo Skill\n请根据 references/missing.md 的规则完成写作，并使用 {{topic}} 作为主题。")
	req := httptest.NewRequest(http.MethodPost, "/workflow-conversions:preflight", strings.NewReader(`{"skill_id":"skill-1"}`))
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	PreflightSkillWorkflowConversion(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preflight status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data struct {
			Status string `json:"status"`
			Checks []struct {
				Code     string `json:"code"`
				Severity string `json:"severity"`
			} `json:"checks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Status != "warning" {
		t.Fatalf("status=%q checks=%#v", envelope.Data.Status, envelope.Data.Checks)
	}
	foundMissing, foundParam := false, false
	for _, check := range envelope.Data.Checks {
		if check.Code == "DEPENDENCY_RESOURCE_MISSING" && check.Severity == "warning" {
			foundMissing = true
		}
		if check.Code == "REQUIRED_PARAMETER_PLACEHOLDER" && check.Severity == "warning" {
			foundParam = true
		}
	}
	if !foundMissing || !foundParam {
		t.Fatalf("expected dependency and parameter checks, got %#v", envelope.Data.Checks)
	}
}

func TestPreflightSkillWorkflowConversionDoesNotTreatScriptArgsAsMissingPath(t *testing.T) {
	db := newHandlerTestDB(t)
	seedSkillForWorkflowConversion(t, db, "user-1", "skill-1", "# Demo Skill\n运行 scripts/fetch_covers.py --keyword {{keyword}}，并根据 references/report_template.html ./ 输出报告。")
	addSkillRevisionFileForWorkflowConversion(t, db, "skill-1-rev", "scripts/fetch_covers.py", "print('ok')", "python")
	addSkillRevisionFileForWorkflowConversion(t, db, "skill-1-rev", "references/report_template.html", "<html></html>", "html")
	req := httptest.NewRequest(http.MethodPost, "/workflow-conversions:preflight", strings.NewReader(`{"skill_id":"skill-1"}`))
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	PreflightSkillWorkflowConversion(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preflight status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data struct {
			Status string `json:"status"`
			Checks []struct {
				Code     string `json:"code"`
				Severity string `json:"severity"`
				Path     string `json:"path"`
			} `json:"checks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	for _, check := range envelope.Data.Checks {
		if check.Code == "DEPENDENCY_RESOURCE_MISSING" {
			t.Fatalf("dependency with CLI args should resolve to packaged file, got %#v", check)
		}
	}
}

func TestListSkillLinkedWorkflowsReturnsOnlyAvailableWhenEnabled(t *testing.T) {
	db := newHandlerTestDB(t)
	seedSkillForWorkflowConversion(t, db, "user-1", "skill-1", "# Demo Skill\n请生成结构化交付内容，包含输入、处理和输出。")
	now := time.Now().UTC()
	if err := db.Create(&orm.UserUIPreferences{UserID: "user-1", SkillsEnabled: true, WorkflowsEnabled: true, MCPEnabled: true, TaskCenterEnabled: true, SchedulesEnabled: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowResource{
		ID: "wf-resource-1", WorkflowRef: "user:user-1:demo-workflow", WorkflowID: "demo-workflow",
		OwnerUserID: "user-1", OwnerScope: "u_user_1", SourceType: "skill",
		SourceSkillID: "skill-1", SourceSkillName: "demo-skill", SourceSkillRevisionID: "skill-1-rev",
		SourceSkillRevisionNo: 1, SourceSkillTreeHash: "tree-skill-1", SourceDraftID: "draft-1",
		RelativeRoot: "workflows/u_user_1/demo-workflow", Name: "Demo Workflow",
		HeadRevisionID: "wf-rev-1", Version: 1, Status: "active", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowRevision{ID: "wf-rev-1", WorkflowResourceID: "wf-resource-1", RevisionNo: 1, TreeHash: "wf-tree", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.UserWorkflowSetting{UserID: "user-1", WorkflowRef: "user:user-1:demo-workflow", Enabled: true, CallMode: WorkflowCallModeManual, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/skills/skill-1/linked-workflows", nil)
	req = mux.SetURLVars(req, map[string]string{"skill_id": "skill-1"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	ListSkillLinkedWorkflows(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("linked status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data struct {
			Workflows []struct {
				WorkflowRef string `json:"workflow_ref"`
				Available   bool   `json:"available"`
				CallMode    string `json:"call_mode"`
			} `json:"workflows"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Workflows) != 1 || !envelope.Data.Workflows[0].Available || envelope.Data.Workflows[0].CallMode != WorkflowCallModeManual {
		t.Fatalf("linked workflows=%#v", envelope.Data.Workflows)
	}
}

func TestListSkillLinkedWorkflowsRejectsMissingRequiredCapability(t *testing.T) {
	db := newHandlerTestDB(t)
	seedSkillForWorkflowConversion(t, db, "user-1", "skill-1", "# Demo Skill\n请联网搜索 SkillHub 并返回匹配技能。")
	now := time.Now().UTC()
	if err := db.Create(&orm.UserUIPreferences{UserID: "user-1", SkillsEnabled: true, WorkflowsEnabled: true, MCPEnabled: true, TaskCenterEnabled: true, SchedulesEnabled: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowGenerationAnalysis{
		ID:                    "analysis-cap-linked",
		DraftID:               "draft-cap-linked",
		UserID:                "user-1",
		SourceType:            "skill",
		SourceSkillID:         "skill-1",
		SourceSkillRevisionID: "skill-1-rev",
		Status:                "generatable",
		ToolMappingReportJSON: `{"capability:web_search":{"action":"require","required":true,"workflow_capability":"web_search","framework_tool":"web_search","available":true,"label":"网页搜索"}}`,
		CreatedAt:             now,
		UpdatedAt:             now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowResource{
		ID: "wf-resource-cap", WorkflowRef: "user:user-1:demo-cap-workflow", WorkflowID: "demo-cap-workflow",
		OwnerUserID: "user-1", OwnerScope: "u_user_1", SourceType: "skill",
		SourceSkillID: "skill-1", SourceSkillName: "demo-skill", SourceDraftID: "draft-cap-linked",
		RelativeRoot: "workflows/u_user_1/demo-cap-workflow", Name: "Demo Cap Workflow",
		HeadRevisionID: "wf-rev-cap", Version: 1, Status: "active", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	compiled := graphengine.Compile(
		"id: demo-cap-workflow\nname: Demo\nslots:\n  - id: result\nsteps:\n  - id: collect\n",
		"steps:\n  collect:\n    outputs: [result]\ntransitions:\n  __start__: [{to: collect}]\n  collect: [{to: __end__}]\n",
		"# Scenario\n\n### collect\n\nCollect result.\n",
		graphengine.ProfilePublish,
	)
	graphJSON, _ := json.Marshal(compiled.Graph)
	if err := db.Create(&orm.WorkflowRevision{ID: "wf-rev-cap", WorkflowResourceID: "wf-resource-cap", RevisionNo: 1, TreeHash: "wf-tree", CompiledGraph: graphJSON, GraphHash: compiled.GraphHash, GraphSchemaVersion: graphengine.SchemaVersion, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.UserWorkflowSetting{UserID: "user-1", WorkflowRef: "user:user-1:demo-cap-workflow", Enabled: true, CallMode: WorkflowCallModeManual, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/skills/skill-1/linked-workflows", nil)
	req = mux.SetURLVars(req, map[string]string{"skill_id": "skill-1"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	ListSkillLinkedWorkflows(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("linked status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data struct {
			Workflows []struct {
				Available         bool   `json:"available"`
				UnavailableReason string `json:"unavailable_reason"`
			} `json:"workflows"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Workflows) != 1 || envelope.Data.Workflows[0].Available || envelope.Data.Workflows[0].UnavailableReason != "required_capability_missing" {
		t.Fatalf("linked workflows=%#v", envelope.Data.Workflows)
	}
}

func TestPublishWorkflowDraftPersistsSourceSkillBinding(t *testing.T) {
	db := newHandlerTestDB(t)
	seedSkillForWorkflowConversion(t, db, "user-1", "skill-1", "# Demo Skill\n请生成结构化交付内容，包含输入、处理和输出，并保留异常恢复建议。")
	now := time.Now().UTC()
	draft := orm.WorkflowDraft{
		ID: "draft-source-skill", WorkflowID: "skill-workflow", Name: "Skill Workflow",
		CreatedBy: "user-1", Version: 1, SourceType: "skill", SourceSkillID: "skill-1",
		SourceSkillName: "demo-skill", SourceSkillRevisionID: "skill-1-rev",
		SourceSkillRevisionNo: 1, SourceSkillTreeHash: "tree-skill-1",
		WorkflowYAMLContent: "id: skill-workflow\nname: Skill Workflow\ndescription: From skill\nwhen_to_use: Use for demos\nslots:\n  - id: result\n    type: text\nsteps:\n  - id: collect\n    label: Collect\nui:\n  tabs:\n    - id: result\n      label: Result\n      layout: vertical\n      slots:\n        - id: result\n",
		StateYAMLContent:    "initial: __start__\nsteps:\n  collect:\n    prompt: collect\n    outputs: [result]\ntransitions:\n  __start__:\n    - to: collect\n  collect:\n    - to: __end__\n",
		ScenarioContent:     "# Scenario\n\n### collect\nCollect the skill inputs, produce the result slot, and preserve recovery guidance for the user.\n",
		ScriptsContent:      "{}",
		CreatedAt:           now, UpdatedAt: now,
	}
	if err := db.Create(&draft).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/workflow-drafts/draft-source-skill:publish", nil)
	req = mux.SetURLVars(req, map[string]string{"draft_id": draft.ID})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	PublishWorkflowDraft(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resource orm.WorkflowResource
	if err := db.Where("plugin_ref = ?", "user:user-1:skill-workflow").Take(&resource).Error; err != nil {
		t.Fatal(err)
	}
	if resource.SourceType != "skill" || resource.SourceSkillID != "skill-1" ||
		resource.SourceSkillRevisionID != "skill-1-rev" || resource.SourceSkillTreeHash != "tree-skill-1" ||
		resource.SourceDraftID != draft.ID {
		t.Fatalf("source binding not persisted: %#v", resource)
	}
}

func TestPublishWorkflowDraftReactivatesArchivedWorkflow(t *testing.T) {
	db := newHandlerTestDB(t)
	seedSkillForWorkflowConversion(t, db, "user-1", "skill-1", "# Demo Skill\n请生成结构化交付内容，包含输入、处理和输出，并保留异常恢复建议。")
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowResource{
		ID: "wf-resource-archived", WorkflowRef: "user:user-1:skill-workflow", WorkflowID: "skill-workflow",
		OwnerUserID: "user-1", OwnerScope: "u_user_1", SourceType: "skill",
		SourceSkillID: "skill-1", SourceSkillName: "demo-skill", SourceDraftID: "old-draft",
		RelativeRoot: "workflows/u_user_1/skill-workflow", Name: "Old Workflow",
		HeadRevisionID: "old-rev", Version: 1, Status: "archived", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowRevision{ID: "old-rev", WorkflowResourceID: "wf-resource-archived", RevisionNo: 1, TreeHash: "old-tree", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	draft := orm.WorkflowDraft{
		ID: "draft-reactivate", WorkflowID: "skill-workflow", Name: "Skill Workflow",
		CreatedBy: "user-1", Version: 1, SourceType: "skill", SourceSkillID: "skill-1",
		SourceSkillName: "demo-skill", SourceSkillRevisionID: "skill-1-rev",
		SourceSkillRevisionNo: 1, SourceSkillTreeHash: "tree-skill-1",
		WorkflowYAMLContent: "id: skill-workflow\nname: Skill Workflow\ndescription: From skill\nwhen_to_use: Use for demos\nslots:\n  - id: result\n    type: text\nsteps:\n  - id: collect\n    label: Collect\nui:\n  tabs:\n    - id: result\n      label: Result\n      layout: vertical\n      slots:\n        - id: result\n",
		StateYAMLContent:    "initial: __start__\nsteps:\n  collect:\n    prompt: collect\n    outputs: [result]\ntransitions:\n  __start__:\n    - to: collect\n  collect:\n    - to: __end__\n",
		ScenarioContent:     "# Scenario\n\n### collect\nCollect the skill inputs and produce the result slot.\n",
		ScriptsContent:      "{}",
		CreatedAt:           now, UpdatedAt: now,
	}
	if err := db.Create(&draft).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/workflow-drafts/draft-reactivate:publish", nil)
	req = mux.SetURLVars(req, map[string]string{"draft_id": draft.ID})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	PublishWorkflowDraft(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resource orm.WorkflowResource
	if err := db.Where("plugin_ref = ?", "user:user-1:skill-workflow").Take(&resource).Error; err != nil {
		t.Fatal(err)
	}
	if resource.Status != "active" || resource.SourceDraftID != draft.ID || resource.HeadRevisionID == "old-rev" {
		t.Fatalf("workflow was not reactivated from publish: %#v", resource)
	}
}

func TestGenerateFailureKeepsDraftGeneratingWhenRetryRemains(t *testing.T) {
	db := newHandlerTestDB(t)
	now := time.Now().UTC()
	draft := orm.WorkflowDraft{
		ID: "draft-retry", WorkflowID: "retry-workflow", Name: "Retry Workflow",
		CreatedBy: "user-1", Version: 1,
		DesignBriefContent: "Brief is already available.",
		CreatedAt:          now, UpdatedAt: now,
	}
	if err := db.Create(&draft).Error; err != nil {
		t.Fatal(err)
	}
	jobRow := orm.AsyncJob{
		ID: "job-retry", JobType: workflowDraftGenerateJobType, Status: "running",
		ResourceType: "workflow_draft", ResourceID: draft.ID, AttemptCount: 1, MaxAttempts: 3,
		NextRunAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&jobRow).Error; err != nil {
		t.Fatal(err)
	}
	if err := markGenerateFailedForAttempt(db.DB, draft.ID, asyncjob.Job{ID: jobRow.ID, AttemptCount: 1}, "phase1 skeleton: temporary upstream timeout"); err != nil {
		t.Fatal(err)
	}
	var updated orm.WorkflowDraft
	if err := db.Where("id=?", draft.ID).First(&updated).Error; err != nil {
		t.Fatal(err)
	}
	if updated.GenerateStatus != generateStatusBriefDone || updated.GenerateError != "" || !strings.Contains(updated.GenerateWarning, "自动重试") {
		t.Fatalf("draft retry status not preserved: status=%q error=%q warning=%q", updated.GenerateStatus, updated.GenerateError, updated.GenerateWarning)
	}
}

func TestCancelWorkflowDraftGenerationCancelsActiveJob(t *testing.T) {
	db := newHandlerTestDB(t)
	now := time.Now().UTC()
	draft := orm.WorkflowDraft{
		ID: "11111111-1111-4111-8111-111111111111", Name: "Cancel Workflow", CreatedBy: "user-1",
		GenerateStatus: generateStatusBriefDone, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&draft).Error; err != nil {
		t.Fatal(err)
	}
	lockUntil := now.Add(time.Minute)
	jobRow := orm.AsyncJob{
		ID: "job-cancel", JobType: workflowDraftGenerateJobType, Status: string(asyncjob.StatusRunning),
		ResourceType: "workflow_draft", ResourceID: draft.ID, AttemptCount: 1, MaxAttempts: 3,
		NextRunAt: now, LockedBy: "worker", LockUntil: &lockUntil, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&jobRow).Error; err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/workflow-drafts/"+draft.ID+":cancel-generation", nil)
	req = mux.SetURLVars(req, map[string]string{"draft_id": draft.ID})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	CancelWorkflowDraftGeneration(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", rec.Code, rec.Body.String())
	}
	var updated orm.WorkflowDraft
	if err := db.Where("id=?", draft.ID).First(&updated).Error; err != nil {
		t.Fatal(err)
	}
	if updated.GenerateStatus != generateStatusFailed || !strings.Contains(updated.GenerateError, "GENERATION_CANCELED") {
		t.Fatalf("draft not marked canceled: status=%q error=%q", updated.GenerateStatus, updated.GenerateError)
	}
	var job orm.AsyncJob
	if err := db.Where("id=?", jobRow.ID).First(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != string(asyncjob.StatusCanceled) || job.LockedBy != "" || job.LockUntil != nil {
		t.Fatalf("job not canceled cleanly: %+v", job)
	}
}

func TestBestGenerateResumePhaseUsesExistingArtifactsOnRetry(t *testing.T) {
	draft := orm.WorkflowDraft{
		DesignBriefContent:  "Brief is ready.",
		WorkflowYAMLContent: "id: retry_workflow\nname: Retry Workflow\nslots:\n  - id: result\n    type: text\nsteps:\n  - id: collect\n    label: Collect\nui:\n  tabs:\n    - id: result\n      label: Result\n      layout: vertical\n      slots:\n        - id: result\n",
		StateYAMLContent:    "initial: __start__\nsteps:\n  collect:\n    prompt: collect\n    outputs: [result]\ntransitions:\n  __start__:\n    - to: collect\n  collect:\n    - to: __end__\n",
	}
	if got := bestGenerateResumePhase(draft, generatePhaseDesignBrief); got != generatePhaseScenarioScripts {
		t.Fatalf("resume phase=%q, want %q", got, generatePhaseScenarioScripts)
	}
}

// --- DisabledBuiltinWorkflowIDs ---

func TestDisabledBuiltinWorkflowIDs_Empty(t *testing.T) {
	db := newHandlerTestDB(t)
	ids, err := DisabledBuiltinWorkflowIDs(db.DB, "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected empty, got %v", ids)
	}
}

func TestDisabledBuiltinWorkflowIDs_ReturnsDisabled(t *testing.T) {
	db := newHandlerTestDB(t)
	now := time.Now().UTC()
	for _, id := range []string{"bsk_01", "bsk_02"} {
		db.DB.Create(&orm.WorkflowResource{
			ID: "resource-" + id, WorkflowRef: "builtin:" + id, WorkflowID: id,
			OwnerScope: "builtin", SourceType: "builtin", RelativeRoot: "workflows/builtin/" + id,
			Name: id, Status: "active", CreatedAt: now, UpdatedAt: now,
		})
	}
	db.DB.Create(&orm.UserWorkflowSetting{
		UserID: "user-1", WorkflowRef: "builtin:bsk_01", Enabled: false,
		UpdatedAt: now,
	})
	db.DB.Create(&orm.UserWorkflowSetting{
		UserID: "user-1", WorkflowRef: "builtin:bsk_02", Enabled: true, CallMode: WorkflowCallModeAuto,
		UpdatedAt: now,
	})
	ids, err := DisabledBuiltinWorkflowIDs(db.DB, "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 1 || ids[0] != "bsk_01" {
		t.Fatalf("got %v, want [bsk_01]", ids)
	}
}

// --- missingWorkflowTables ---

func TestMissingWorkflowTables(t *testing.T) {
	tests := []struct {
		errMsg string
		want   bool
	}{
		{"no such table: user_workflow_settings", true},
		{"relation \"user_workflow_settings\" does not exist", true},
		{"unknown error", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.errMsg, func(t *testing.T) {
			var err error
			if tt.errMsg != "" {
				err = &testError2{msg: tt.errMsg}
			}
			if got := missingWorkflowTables(err); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// --- ListWorkflowVersions ---

func TestListWorkflowVersions_NotFound(t *testing.T) {
	newHandlerTestDB(t)
	req := httptest.NewRequest(http.MethodGet, "/workflows/nonexistent/versions", nil)
	req = mux.SetURLVars(req, map[string]string{"workflow_ref": "nonexistent"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	ListWorkflowVersions(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// --- PatchUserWorkflowSetting ---

func TestListUserWorkflowSettings_ReturnsWorkflowContract(t *testing.T) {
	db := newHandlerTestDB(t)
	seedWorkflowResource(t, db, "custom-workflow", "wf-custom", "user-1")
	seedCatalogWorkflow(t, db, "writer-workflow")

	req := httptest.NewRequest(http.MethodGet, "/chat/settings/workflows", nil)
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	ListUserWorkflowSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, exists := resp.Data["plugins"]; exists {
		t.Fatalf("legacy plugins field must not be returned: %s", rec.Body.String())
	}
	var workflows []map[string]any
	if err := json.Unmarshal(resp.Data["workflows"], &workflows); err != nil {
		t.Fatalf("decode workflows: %v, body=%s", err, rec.Body.String())
	}
	if len(workflows) != 2 {
		t.Fatalf("unexpected workflows: %#v", workflows)
	}
	for _, item := range workflows {
		if item["workflow_ref"] == "builtin:writer-workflow" && (item["enabled"] != true || item["call_mode"] != WorkflowCallModeAuto) {
			t.Fatalf("built-in workflow must default to automatic matching: %#v", item)
		}
	}
}

func TestEnabledCatalogOnlyIncludesAutomaticWorkflows(t *testing.T) {
	db := newHandlerTestDB(t)
	seedCatalogWorkflow(t, db, "writer-workflow")
	catalog, err := EnabledCatalog(db.DB, "user-1")
	if err != nil || len(catalog) != 1 {
		t.Fatalf("default catalog = %#v, err=%v", catalog, err)
	}
	if err := db.Create(&orm.UserWorkflowSetting{
		UserID: "user-1", WorkflowRef: "builtin:writer-workflow", Enabled: true, CallMode: WorkflowCallModeManual,
	}).Error; err != nil {
		t.Fatal(err)
	}
	catalog, err = EnabledCatalog(db.DB, "user-1")
	if err != nil || len(catalog) != 0 {
		t.Fatalf("manual workflow leaked into automatic catalog = %#v, err=%v", catalog, err)
	}
	suppressedBuiltins, err := DisabledBuiltinWorkflowIDs(db.DB, "user-1")
	if err != nil || len(suppressedBuiltins) != 1 || suppressedBuiltins[0] != "writer-workflow" {
		t.Fatalf("manual builtin suppression = %#v, err=%v", suppressedBuiltins, err)
	}
	if err := db.Model(&orm.UserWorkflowSetting{}).
		Where("user_id=? AND plugin_ref=?", "user-1", "builtin:writer-workflow"). // workflow-naming: persistence
		Updates(map[string]any{"enabled": true, "call_mode": WorkflowCallModeAuto}).Error; err != nil {
		t.Fatal(err)
	}
	catalog, err = EnabledCatalog(db.DB, "user-1")
	if err != nil || len(catalog) != 1 || catalog[0]["call_mode"] != WorkflowCallModeAuto {
		t.Fatalf("automatic catalog = %#v, err=%v", catalog, err)
	}
	if err := db.Model(&orm.UserWorkflowSetting{}).
		Where("user_id=? AND plugin_ref=?", "user-1", "builtin:writer-workflow"). // workflow-naming: persistence
		Updates(map[string]any{"enabled": false, "call_mode": WorkflowCallModeDisabled}).Error; err != nil {
		t.Fatal(err)
	}
	catalog, err = EnabledCatalog(db.DB, "user-1")
	if err != nil || len(catalog) != 0 {
		t.Fatalf("paused workflow leaked into automatic catalog = %#v, err=%v", catalog, err)
	}
}

func TestPatchUserWorkflowSetting_Unauthorized(t *testing.T) {
	newHandlerTestDB(t)
	req := httptest.NewRequest(http.MethodPatch, "/workflows/test/settings", nil)
	req = mux.SetURLVars(req, map[string]string{"workflow_ref": "test"})
	rec := httptest.NewRecorder()
	PatchUserWorkflowSetting(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestPatchUserWorkflowSetting_ExistingWorkflowUpserts(t *testing.T) {
	db := newHandlerTestDB(t)
	seedWorkflowResource(t, db, "custom-plugin", "pid-custom", "user-1")
	body := jsonBody(`{"call_mode":"manual"}`)
	req := httptest.NewRequest(http.MethodPatch, "/workflows/custom-plugin/settings", body)
	req = mux.SetURLVars(req, map[string]string{"workflow_ref": "custom-plugin"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	PatchUserWorkflowSetting(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var setting orm.UserWorkflowSetting
	if err := db.Where("user_id=? AND plugin_ref=?", "user-1", "custom-plugin").Take(&setting).Error; err != nil {
		t.Fatal(err)
	}
	if !setting.Enabled || setting.CallMode != WorkflowCallModeManual {
		t.Fatalf("stored setting = %#v, want enabled manual", setting)
	}
}

func TestPatchUserWorkflowSettingRejectsInvalidCallMode(t *testing.T) {
	db := newHandlerTestDB(t)
	seedWorkflowResource(t, db, "custom-plugin", "pid-custom", "user-1")
	req := httptest.NewRequest(http.MethodPatch, "/workflows/custom-plugin/settings", jsonBody(`{"call_mode":"sometimes"}`))
	req = mux.SetURLVars(req, map[string]string{"workflow_ref": "custom-plugin"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	PatchUserWorkflowSetting(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestPatchUserWorkflowSetting_NonBuiltinNotFound(t *testing.T) {
	newHandlerTestDB(t)
	body := jsonBody(`{"enabled":true}`)
	req := httptest.NewRequest(http.MethodPatch, "/workflows/unknown-ref/settings", body)
	req = mux.SetURLVars(req, map[string]string{"workflow_ref": "unknown-ref"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	PatchUserWorkflowSetting(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestPatchUserWorkflowSetting_UnknownBuiltinNotFound(t *testing.T) {
	newHandlerTestDB(t)
	body := jsonBody(`{"enabled":true}`)
	req := httptest.NewRequest(http.MethodPatch, "/workflows/builtin:removed/settings", body)
	req = mux.SetURLVars(req, map[string]string{"workflow_ref": "builtin:removed"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	PatchUserWorkflowSetting(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func seedCatalogWorkflow(t *testing.T, db *orm.DB, id string) {
	t.Helper()
	now := time.Now().UTC()
	body := []byte("id: " + id + "\nname: Test Workflow\ndescription: Fast test\nsteps:\n  - {id: first, label: First}\nslots:\n  - {id: output, label: Output, type: text, cardinality: single}\nui:\n  tabs:\n    - id: result\n      label: Result\n      layout: list\n      slots: [{id: output}]\ni18n:\n  zh-CN:\n    name: 测试工作流\n    tabs:\n      result: {label: 结果}\n")
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	resourceID, revisionID := "resource-"+id, "revision-"+id
	if err := db.Create(&orm.WorkflowResource{ID: resourceID, WorkflowRef: "builtin:" + id,
		WorkflowID: id, Name: "Test Workflow", OwnerScope: "builtin", SourceType: "builtin",
		Status: "active", HeadRevisionID: revisionID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create resource: %v", err)
	}
	if err := db.Create(&orm.WorkflowRevision{ID: revisionID, WorkflowResourceID: resourceID,
		RevisionNo: 1, TreeHash: hash, CreatedAt: now}).Error; err != nil {
		t.Fatalf("create revision: %v", err)
	}
	if err := db.Create(&orm.WorkflowBlob{Hash: hash, Size: int64(len(body)), Mime: "application/yaml",
		Content: body, CreatedAt: now}).Error; err != nil {
		t.Fatalf("create blob: %v", err)
	}
	if err := db.Create(&orm.WorkflowRevisionEntry{RevisionID: revisionID, Path: "workflow.yaml",
		EntryType: "file", BlobHash: &hash, Size: int64(len(body)), Mime: "application/yaml"}).Error; err != nil {
		t.Fatalf("create entry: %v", err)
	}
	for path, content := range map[string][]byte{
		"scenario/state.yml":   []byte("transitions:\n  __start__: [{to: first}]\n  first: [{to: __end__}]\n"),
		"scenario/scenario.md": []byte("# Test scenario\n"),
		"scenario/layout.json": []byte(`{"first":{"x":120,"y":80}}`),
		"scripts/tools.py":     []byte("def test_tool():\n    return 'ok'\n"),
	} {
		fileSum := sha256.Sum256(content)
		fileHash := hex.EncodeToString(fileSum[:])
		if err := db.Create(&orm.WorkflowBlob{Hash: fileHash, Size: int64(len(content)), Content: content, CreatedAt: now}).Error; err != nil {
			t.Fatalf("create %s blob: %v", path, err)
		}
		if err := db.Create(&orm.WorkflowRevisionEntry{RevisionID: revisionID, Path: path,
			EntryType: "file", BlobHash: &fileHash, Size: int64(len(content))}).Error; err != nil {
			t.Fatalf("create %s entry: %v", path, err)
		}
	}
}

func TestWorkflowCatalogHandlersReadCoreRevisionWithoutChatUpstream(t *testing.T) {
	db := newHandlerTestDB(t)
	seedCatalogWorkflow(t, db, "catalog-test")
	seedWorkflowResource(t, db, "user:paper-search", "paper-search", "user-1")
	if err := db.Create(&orm.UserWorkflowSetting{UserID: "user-1", WorkflowRef: "builtin:catalog-test", Enabled: false}).Error; err != nil {
		t.Fatalf("disable workflow: %v", err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/workflows", nil)
	listReq.Header.Set("X-User-Id", "user-1")
	listRec := httptest.NewRecorder()
	ListWorkflows(listRec, listReq)
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), `"id":"catalog-test"`) {
		t.Fatalf("list must include disabled catalog workflow: status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	if strings.Contains(listRec.Body.String(), "paper-search") {
		t.Fatalf("published user workflow must not be duplicated in built-in catalog: %s", listRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/workflows/catalog-test", nil)
	getReq = mux.SetURLVars(getReq, map[string]string{"workflow_id": "catalog-test"})
	getReq.Header.Set("X-User-Id", "user-1")
	getReq.Header.Set("Accept-Language", "zh-CN")
	getRec := httptest.NewRecorder()
	GetWorkflowInfo(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	var spec map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &spec); err != nil {
		t.Fatalf("decode catalog response: %v", err)
	}
	if spec["name"] != "测试工作流" {
		t.Fatalf("localized name missing: %#v", spec["name"])
	}
	ui, _ := spec["ui"].(map[string]any)
	if ui == nil || ui["tabs"] == nil {
		t.Fatalf("panel UI declaration missing: %#v", spec)
	}
	for _, field := range []string{"workflow_yaml_raw", "state_yaml_raw", "layout_raw", "scenario_raw", "scripts_raw"} {
		if value, _ := spec[field].(string); value == "" {
			t.Fatalf("built-in detail field %s is empty: %#v", field, spec)
		}
	}
}

func TestCopyBuiltinWorkflowCopiesEveryEditablePackageFile(t *testing.T) {
	db := newHandlerTestDB(t)
	seedCatalogWorkflow(t, db, "catalog-copy-test")
	req := httptest.NewRequest(http.MethodPost, "/workflows/catalog-copy-test:copy", strings.NewReader(`{"name":"完整副本"}`))
	req = mux.SetURLVars(req, map[string]string{"workflow_id": "catalog-copy-test"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	CopyBuiltinWorkflow(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("copy built-in status=%d body=%s", rec.Code, rec.Body.String())
	}
	var copied orm.WorkflowDraft
	if err := db.Where("created_by=? AND plugin_id=?", "user-1", "catalog-copy-test-copy").First(&copied).Error; err != nil {
		t.Fatal(err)
	}
	if copied.StateLayoutContent != `{"first":{"x":120,"y":80}}` || !strings.Contains(copied.ScriptsContent, "scripts/tools.py") ||
		!strings.Contains(copied.StateYAMLContent, "__start__") || copied.ScenarioContent != "# Test scenario\n" {
		t.Fatalf("built-in copy is incomplete: %#v", copied)
	}
}

func TestEnrichSlotsExecutesRevisionCountQuery(t *testing.T) {
	db := newTestDB(t)
	index := 0
	if err := db.Create(&orm.WorkflowSlotRevision{SessionID: "session-1", SlotID: "output",
		Revision: 1, ListIndex: &index, Selected: true, Slot: "output", ContentSnapshot: json.RawMessage(`{"value":"ok"}`)}).Error; err != nil {
		t.Fatalf("create slot revision: %v", err)
	}
	slots := []slotDTO{{SlotID: "output", Slot: "output", Revision: 1, ListIndex: &index,
		ContentSnapshot: json.RawMessage(`{"value":"ok"}`)}}
	enrichSlots(t.Context(), db.DB, "session-1", slots)
	if slots[0].RevisionCount != 1 {
		t.Fatalf("revision count: got %d, want 1", slots[0].RevisionCount)
	}
}

func TestEnrichSlotsWriterDraftExcludesMutableHumanRevisionFromVersionCount(t *testing.T) {
	db := newTestDB(t)
	rows := []orm.WorkflowSlotRevision{
		{
			ID:        "writer-ai-1",
			SessionID: "session-1", SlotID: "draft_document", Revision: 1,
			Selected: false, Slot: "draft_document", ChangeSource: "ai",
			ContentSnapshot: json.RawMessage(`{"data":"# AI draft"}`),
		},
		{
			ID:        "writer-human-2",
			SessionID: "session-1", SlotID: "draft_document", Revision: 2,
			Selected: true, Slot: "draft_document", ChangeSource: "human",
			ContentSnapshot: json.RawMessage(`{"data":"# Working draft"}`),
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create writer revisions: %v", err)
	}
	slots := []slotDTO{{
		SlotID: "draft_document", Slot: "draft_document", Revision: 2,
		Selected: true, ChangeSource: "human",
		ContentSnapshot: json.RawMessage(`{"data":"# Working draft"}`),
	}}

	enrichSlots(t.Context(), db.DB, "session-1", slots)

	if slots[0].RevisionCount != 1 {
		t.Fatalf("writer version count: got %d, want 1", slots[0].RevisionCount)
	}
	if slots[0].VersionNumber != 1 {
		t.Fatalf("writer draft base version: got %d, want 1", slots[0].VersionNumber)
	}
}
