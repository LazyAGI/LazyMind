package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func TestFrontendAndHostedGenerationShareSnapshotAndAnalysis(t *testing.T) {
	db := newHandlerTestDB(t)
	seedSkillForWorkflowConversion(t, db, "owner", "skill", "# Report\nProduce a report from the supplied text.")
	snapshot, err := loadWorkflowSourceSkill(t.Context(), db.DB, "owner", "skill")
	if err != nil {
		t.Fatal(err)
	}
	cached := orm.WorkflowGenerationAnalysis{ID: "cached", UserID: "owner", SourceSkillID: "skill",
		SourceSkillRevisionID: snapshot.RevisionID, SourceSkillTreeHash: snapshot.TreeHash,
		Status: "generatable", SelectedCandidateID: "report", CandidatesJSON: `[{"id":"report"}]`,
		ToolMappingReportJSON: `{}`, ScriptReportJSON: `{}`}
	if err := db.Create(&cached).Error; err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"frontend", "hosted"} {
		draftID := uuid.NewString()
		if err := db.Create(&orm.WorkflowDraft{ID: draftID, WorkflowID: mode, Name: "Report", CreatedBy: "owner", Version: 1, ScriptsContent: "{}"}).Error; err != nil {
			t.Fatal(err)
		}
		if mode == "frontend" {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"skill_id":"skill"}`))
			req.Header.Set("X-User-Id", "owner")
			req = mux.SetURLVars(req, map[string]string{"draft_id": draftID})
			rec := httptest.NewRecorder()
			AIGenerateWorkflowDraft(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("frontend: %s", rec.Body)
			}
		} else {
			err := db.Transaction(func(tx *gorm.DB) error {
				return enqueueExternalWorkflowGeneration(t.Context(), tx, orm.ExternalAgentWorkflowTask{
					ID: "task", OwnerUserID: "owner", SkillID: "skill", TaskDescription: "Make report"},
					orm.WorkflowDraft{ID: draftID}, snapshot)
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		var job orm.AsyncJob
		if err := db.First(&job, "resource_id=?", draftID).Error; err != nil {
			t.Fatal(err)
		}
		var payload workflowDraftGeneratePayload
		if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil {
			t.Fatal(err)
		}
		if job.JobType != workflowDraftGenerateJobType || job.CreateUserID != "owner" || payload.UserID != "owner" ||
			payload.SourceSkillRevisionID != snapshot.RevisionID || payload.SkillContent != snapshot.skillMD() || payload.SelectedCandidateJSON == "" {
			t.Fatalf("%s did not preserve shared generation contract: %+v", mode, payload)
		}
		if strings.Contains(string(job.PayloadJSON), "llm_config") {
			t.Fatal("model credentials must not be persisted in generation jobs")
		}
	}
}

func TestGenerationAdmissionRollsBackDraftWhenQueueFails(t *testing.T) {
	db := newHandlerTestDB(t)
	draftID := uuid.NewString()
	seedWorkflowDraft(t, db, draftID, "owner")
	if err := db.Callback().Create().Before("gorm:create").Register("fail_test_job", func(tx *gorm.DB) {
		if tx.Statement.Table == "async_jobs" {
			tx.AddError(errors.New("injected queue failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Callback().Create().Remove("fail_test_job") })
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"description":"Make a report"}`))
	req.Header.Set("X-User-Id", "owner")
	req = mux.SetURLVars(req, map[string]string{"draft_id": draftID})
	rec := httptest.NewRecorder()
	AIGenerateWorkflowDraft(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected queue failure: %s", rec.Body)
	}
	var draft orm.WorkflowDraft
	if err := db.First(&draft, "id=?", draftID).Error; err != nil {
		t.Fatal(err)
	}
	if draft.GenerateStatus != "" || draft.SourceType != "" {
		t.Fatalf("partial generation admission persisted: %+v", draft)
	}
}

func TestHostedPublicationRetryReusesRevisionAndChecksOwnership(t *testing.T) {
	db := newHandlerTestDB(t)
	draft := orm.WorkflowDraft{ID: "draft", Name: "Report", CreatedBy: "owner", SourceType: "blank", Version: 1,
		WorkflowYAMLContent: "id: report\nslots: []\nsteps:\n  - {id: execute, label: Execute}\n",
		StateYAMLContent:    "transitions:\n  __start__: [{to: execute}]\n  execute: [{to: __end__}]\nsteps:\n  execute: {outputs: []}\n",
		ScenarioContent:     "# Report\n\nThe execute step produces the requested result.\n", ScriptsContent: "{}"}
	if err := db.Create(&draft).Error; err != nil {
		t.Fatal(err)
	}
	ref, revision, err := publishDraftForExternalTask(context.Background(), db.DB, "owner", draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	var setting orm.UserWorkflowSetting
	if err := db.Where("user_id=? AND plugin_ref=?", "owner", ref).First(&setting).Error; err != nil {
		t.Fatal(err)
	}
	if !setting.Enabled || setting.CallMode != WorkflowCallModeManual {
		t.Fatalf("external publication must be runnable by default: %#v", setting)
	}
	retryRef, retryRevision, err := publishDraftForExternalTask(context.Background(), db.DB, "owner", draft.ID)
	if err != nil || ref != retryRef || revision != retryRevision {
		t.Fatalf("retry changed publication: %s %s %v", retryRef, retryRevision, err)
	}
	var count int64
	db.Model(&orm.WorkflowRevision{}).Count(&count)
	if count != 1 {
		t.Fatalf("duplicate revisions: %d", count)
	}
	if _, _, err := publishDraftForExternalTask(context.Background(), db.DB, "other", draft.ID); err == nil {
		t.Fatal("another user published owned draft")
	}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-User-Id", "owner")
	req = mux.SetURLVars(req, map[string]string{"draft_id": draft.ID})
	rec := httptest.NewRecorder()
	PublishAuthoringWorkflow(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("frontend no-change contract changed: %s", rec.Body)
	}
}

func TestEnsureExternalWorkflowRunnablePreservesAutomaticMode(t *testing.T) {
	db := newHandlerTestDB(t)
	ref := "user:owner:report"
	if err := ensureExternalWorkflowRunnable(context.Background(), db.DB, "owner", ref); err != nil {
		t.Fatal(err)
	}
	var setting orm.UserWorkflowSetting
	if err := db.Where("user_id=? AND plugin_ref=?", "owner", ref).First(&setting).Error; err != nil {
		t.Fatal(err)
	}
	if !setting.Enabled || setting.CallMode != WorkflowCallModeManual {
		t.Fatalf("missing external workflow setting should default to manual: %#v", setting)
	}
	if err := db.Model(&setting).Updates(map[string]any{"enabled": true, "call_mode": WorkflowCallModeAuto}).Error; err != nil {
		t.Fatal(err)
	}
	if err := ensureExternalWorkflowRunnable(context.Background(), db.DB, "owner", ref); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("user_id=? AND plugin_ref=?", "owner", ref).First(&setting).Error; err != nil {
		t.Fatal(err)
	}
	if !setting.Enabled || setting.CallMode != WorkflowCallModeAuto {
		t.Fatalf("external workflow setting should preserve automatic mode: %#v", setting)
	}
}
