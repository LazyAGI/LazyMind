package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

const readinessWorkflow = "id: readiness\nslots:\n  - {id: result}\nsteps:\n  - {id: write, label: Write}\n"
const readinessState = "transitions:\n  __start__: [{to: write}]\n  write: [{to: __end__}]\nsteps:\n  write:\n    prompt: Write a report\n    outputs: [result]\n"

func seedReadinessRevision(t *testing.T, db *orm.DB, revision, state string, number int64) {
	t.Helper()
	compiled := graphengine.Compile(readinessWorkflow, state, "", graphengine.ProfilePublish)
	if number == 1 && !compiled.Valid {
		t.Fatalf("invalid fixture: %+v", compiled.Diagnostics)
	}
	graphJSON := compiled.Graph.JSON()
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowRevision{ID: revision, WorkflowResourceID: "readiness-resource", RevisionNo: number, TreeHash: revision, CompiledGraph: graphJSON, GraphHash: compiled.GraphHash, GraphSchemaVersion: graphengine.SchemaVersion, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{"workflow.yaml": readinessWorkflow, "scenario/state.yml": state} {
		sum := sha256.Sum256([]byte(body))
		hash := hex.EncodeToString(sum[:])
		var count int64
		if err := db.Model(&orm.WorkflowBlob{}).Where("hash = ?", hash).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			if err := db.Create(&orm.WorkflowBlob{Hash: hash, Content: []byte(body)}).Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := db.Create(&orm.WorkflowRevisionEntry{RevisionID: revision, Path: path, EntryType: "file", BlobHash: &hash}).Error; err != nil {
			t.Fatal(err)
		}
	}
}
func seedReadinessWorkflow(t *testing.T) *orm.DB {
	t.Helper()
	db := newHandlerTestDB(t)
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowDraft{ID: "repair-draft", CreatedBy: "owner", WorkflowYAMLContent: "[broken", StateYAMLContent: "[broken", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowResource{ID: "readiness-resource", WorkflowRef: "user:owner:readiness", WorkflowID: "readiness", Name: "Readiness", OwnerUserID: "owner", SourceDraftID: "repair-draft", Status: "active", HeadRevisionID: "good", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	seedReadinessRevision(t, db, "good", readinessState, 1)
	return db
}
func TestWorkflowReadinessIgnoresBrokenDraftAndDoesNotWrite(t *testing.T) {
	db := seedReadinessWorkflow(t)
	for i := 0; i < 2; i++ {
		report, err := CheckWorkflowReadiness(t.Context(), db.DB, "owner", "user:owner:readiness", "")
		if err != nil || !report.Ready || report.RevisionID != "good" || !report.Editable || report.RepairURL != "/memory-management/workflows/repair-draft" {
			t.Fatalf("readiness=%+v err=%v", report, err)
		}
	}
	for _, model := range []any{&orm.WorkflowSession{}, &orm.TaskCenterTask{}, &orm.ChatHistory{}} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("preflight wrote records: model=%T count=%d err=%v", model, count, err)
		}
	}
}
func TestWorkflowReadinessPinsExistingSessionAndRejectsInvalidHead(t *testing.T) {
	db := seedReadinessWorkflow(t)
	seedReadinessRevision(t, db, "bad", strings.ReplaceAll(readinessState, "to: write", "to: missing"), 2)
	if err := db.Model(&orm.WorkflowResource{}).Where("id = ?", "readiness-resource").Update("head_revision_id", "bad").Error; err != nil {
		t.Fatal(err)
	}
	report, err := CheckWorkflowReadiness(t.Context(), db.DB, "owner", "user:owner:readiness", "")
	if err != nil || report.Ready || len(report.Issues) == 0 {
		t.Fatalf("invalid head accepted: %+v %v", report, err)
	}
	if err := db.Create(&orm.WorkflowSession{ID: "active", ConversationID: "conversation", WorkflowRef: "user:owner:readiness", WorkflowRevisionID: "good", CreateUserID: "owner", Status: SessionStatusActive}).Error; err != nil {
		t.Fatal(err)
	}
	report, err = CheckWorkflowInvocationReadiness(t.Context(), db.DB, "owner", "user:owner:readiness", "conversation")
	if err != nil || !report.Ready || report.RevisionID != "good" {
		t.Fatalf("lost pinned version: %+v %v", report, err)
	}
}
func TestWorkflowReadinessDoesNotExposeAnotherOwnersRepairDraft(t *testing.T) {
	db := seedReadinessWorkflow(t)
	report, err := CheckWorkflowReadiness(t.Context(), db.DB, "other", "user:owner:readiness", "")
	if err != nil || report.Ready || report.Editable || strings.Contains(report.RepairURL, "repair-draft") {
		t.Fatalf("private workflow exposed: %+v %v", report, err)
	}
	if err := db.Model(&orm.WorkflowResource{}).Where("id = ?", "readiness-resource").Update("owner_user_id", "").Error; err != nil {
		t.Fatal(err)
	}
	report, err = CheckWorkflowReadiness(t.Context(), db.DB, "other", "user:owner:readiness", "")
	if err != nil || !report.Ready || report.Editable || strings.Contains(report.RepairURL, "repair-draft") {
		t.Fatalf("public repair scope invalid: %+v %v", report, err)
	}
}
func TestGetWorkflowReadinessIsReadOnly(t *testing.T) {
	seedReadinessWorkflow(t)
	request := httptest.NewRequest(http.MethodGet, "/chat/workflow-readiness?workflow_ref=user:owner:readiness", nil)
	request.Header.Set("X-User-Id", "owner")
	recorder := httptest.NewRecorder()
	GetWorkflowReadiness(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"ready":true`) {
		t.Fatalf("response %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestWorkflowReadinessSeparatesMissingConfigurationFromCheckFailure(t *testing.T) {
	db := seedReadinessWorkflow(t)
	state := strings.Replace(readinessState, "    prompt:", "    capabilities: [web_search]\n    prompt:", 1)
	seedReadinessRevision(t, db, "search", state, 2)
	if err := db.Model(&orm.WorkflowResource{}).Where("id = ?", "readiness-resource").Update("head_revision_id", "search").Error; err != nil {
		t.Fatal(err)
	}
	// A failed configuration read must not be presented as a broken Workflow.
	if _, err := CheckWorkflowReadiness(t.Context(), db.DB, "owner", "user:owner:readiness", ""); err == nil {
		t.Fatal("missing configuration tables should fail the check")
	}
	if err := db.AutoMigrate(&orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserSelectedProvider{}); err != nil {
		t.Fatal(err)
	}
	report, err := CheckWorkflowReadiness(t.Context(), db.DB, "owner", "user:owner:readiness", "")
	if err != nil || report.Ready || len(report.Issues) != 1 || report.Issues[0].Code != "WORKFLOW_REQUIRED_CONFIG_MISSING" || report.Issues[0].RepairURL != "/settings?section=system_tools" {
		t.Fatalf("missing configuration: %+v %v", report, err)
	}
}

func TestWorkflowReadinessChecksDeclaredToolsWithoutCapabilities(t *testing.T) {
	db := seedReadinessWorkflow(t)
	if err := db.AutoMigrate(&orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserSelectedProvider{}); err != nil {
		t.Fatal(err)
	}
	state := strings.Replace(readinessState, "    prompt:", "    tools: [web_search]\n    prompt:", 1)
	seedReadinessRevision(t, db, "tool-search", state, 2)
	report, err := CheckWorkflowReadiness(t.Context(), db.DB, "owner", "user:owner:readiness", "tool-search")
	if err != nil || report.Ready || len(report.Issues) != 1 || report.Issues[0].Code != "WORKFLOW_REQUIRED_CONFIG_MISSING" {
		t.Fatalf("tool config not checked: %+v %v", report, err)
	}
}
