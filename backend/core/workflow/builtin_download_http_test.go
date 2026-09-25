package workflow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	skillbuiltin "lazymind/core/skillv2/builtin"
)

func TestBuiltinWorkflowEntryPointsReportDownloadFailure(t *testing.T) {
	uid := useWorkflowBuiltinCatalog(t)
	t.Setenv("LAZYMIND_BUILTIN_SKILL_CACHE", t.TempDir())
	catalogPath := skillbuiltin.CatalogPath()
	catalog, err := skillbuiltin.LoadCatalog(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	catalog.Skills[0].ResolvedURL = "http://example.test/locked.zip"
	encoded, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogPath, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	db := newHandlerTestDB(t)
	draftID := uuid.NewString()
	seedWorkflowDraft(t, db, draftID, "user-1")
	authoringDraftID := uuid.NewString()
	var authoringDraft orm.WorkflowDraft
	if err := db.Where("id = ?", draftID).Take(&authoringDraft).Error; err != nil {
		t.Fatal(err)
	}
	authoringDraft.ID = authoringDraftID
	authoringDraft.WorkflowID = "authoring-plugin"
	authoringDraft.SourceType = "skill"
	authoringDraft.SourceSkillID = "builtin:" + uid
	authoringDraft.SourceSkillRevisionID = "builtin:" + uid
	authoringDraft.SourceSkillTreeHash = "pinned-tree"
	if err := db.Create(&authoringDraft).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowGenerationAnalysis{
		ID: "analysis-1", DraftID: draftID, UserID: "user-1", SourceType: "skill",
		SourceSkillID: "builtin:" + uid, SourceSkillRevisionID: "builtin:" + uid,
		SourceSkillTreeHash: "pinned-tree", Status: "needs_confirmation",
		CandidatesJSON: `[{"id":"candidate-1"}]`, ToolMappingReportJSON: "{}", ScriptReportJSON: "{}",
	}).Error; err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, method, target, body string
		invoke                     func(http.ResponseWriter, *http.Request)
		vars                       map[string]string
	}{
		{"preflight", http.MethodPost, "/workflow-conversions:preflight", `{"skill_id":"builtin:` + uid + `"}`, PreflightSkillWorkflowConversion, nil},
		{"authoring context", http.MethodGet, "/skill-conversion-context?skill_id=" + url.QueryEscape("builtin:"+uid), "", GetSkillConversionContext, nil},
		{"linked workflows", http.MethodGet, "/skills/builtin:" + uid + "/linked-workflows", "", ListSkillLinkedWorkflows, map[string]string{"skill_id": "builtin:" + uid}},
		{"generation", http.MethodPost, "/workflow-drafts/" + draftID + ":ai-generate", `{"skill_id":"builtin:` + uid + `"}`, AIGenerateWorkflowDraft, map[string]string{"draft_id": draftID}},
		{"confirmation", http.MethodPost, "/workflow-drafts/" + draftID + ":confirm", `{"analysis_id":"analysis-1","candidate_id":"candidate-1","source_skill_revision_id":"builtin:` + uid + `","draft_version":1}`, ConfirmWorkflowWorkflow, map[string]string{"draft_id": draftID}},
		{"authoring diagnostics", http.MethodGet, "/workflow-drafts/" + authoringDraftID + "/diagnostics", "", GetAuthoringWorkflowDiagnostics, map[string]string{"draft_id": authoringDraftID}},
		{"authoring publish", http.MethodPost, "/workflow-drafts/" + authoringDraftID + ":publish", "", PublishAuthoringWorkflow, map[string]string{"draft_id": authoringDraftID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			req.Header.Set("X-User-Id", "user-1")
			if tc.vars != nil {
				req = mux.SetURLVars(req, tc.vars)
			}
			rec := httptest.NewRecorder()
			tc.invoke(rec, req)
			if rec.Code != http.StatusBadGateway {
				t.Fatalf("status=%d body=%s, want 502", rec.Code, rec.Body.String())
			}
			var envelope struct {
				Code int `json:"code"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil || envelope.Code != 2003116 {
				t.Fatalf("error code=%d decode err=%v body=%s", envelope.Code, err, rec.Body.String())
			}
		})
	}
	var draft orm.WorkflowDraft
	if err := db.Where("id = ?", draftID).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	if draft.GenerateStatus != "" {
		t.Fatalf("failed download changed generation status: %q", draft.GenerateStatus)
	}
}
