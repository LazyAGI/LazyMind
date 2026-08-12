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

	"lazymind/core/common/orm"
	"lazymind/core/store"
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
		&orm.WorkflowGenerationAnalysis{},
		&orm.WorkflowRepairRun{},
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
		UserID: "user-1", WorkflowRef: "builtin:bsk_02", Enabled: true,
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
		if item["workflow_ref"] == "builtin:writer-workflow" && item["enabled"] != true {
			t.Fatalf("built-in workflow must default enabled: %#v", item)
		}
	}
}

func TestEnabledCatalogDefaultsBuiltinOnUnlessDisabled(t *testing.T) {
	db := newHandlerTestDB(t)
	seedCatalogWorkflow(t, db, "writer-workflow")
	catalog, err := EnabledCatalog(db.DB, "user-1")
	if err != nil || len(catalog) != 1 {
		t.Fatalf("default catalog = %#v, err=%v", catalog, err)
	}
	if err := db.Create(&orm.UserWorkflowSetting{
		UserID: "user-1", WorkflowRef: "builtin:writer-workflow", Enabled: false,
	}).Error; err != nil {
		t.Fatal(err)
	}
	catalog, err = EnabledCatalog(db.DB, "user-1")
	if err != nil || len(catalog) != 0 {
		t.Fatalf("disabled catalog = %#v, err=%v", catalog, err)
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
	body := jsonBody(`{"enabled":false}`)
	req := httptest.NewRequest(http.MethodPatch, "/workflows/custom-plugin/settings", body)
	req = mux.SetURLVars(req, map[string]string{"workflow_ref": "custom-plugin"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	PatchUserWorkflowSetting(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
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
	for _, field := range []string{"workflow_yaml_raw", "state_yaml_raw", "scenario_raw", "scripts_raw"} {
		if value, _ := spec[field].(string); value == "" {
			t.Fatalf("built-in detail field %s is empty: %#v", field, spec)
		}
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
