package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/workflow/graphengine"
)

const AuthoringContractVersion = "workflow.authoring.v1"

type authoringDiagnostic struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Path     string `json:"path,omitempty"`
	Message  string `json:"message"`
}

type authoringDiagnostics struct {
	ContractVersion       string                `json:"contract_version"`
	Valid                 bool                  `json:"valid"`
	DraftVersion          int                   `json:"draft_version"`
	SourceSkillRevisionID string                `json:"source_skill_revision_id"`
	SourceSkillTreeHash   string                `json:"source_skill_tree_hash"`
	Diagnostics           []authoringDiagnostic `json:"diagnostics"`
}

func authoringDiagnosticsForDraft(db *gorm.DB, draft orm.WorkflowDraft) authoringDiagnostics {
	out := authoringDiagnostics{ContractVersion: AuthoringContractVersion, DraftVersion: draft.Version, SourceSkillRevisionID: draft.SourceSkillRevisionID, SourceSkillTreeHash: draft.SourceSkillTreeHash, Diagnostics: []authoringDiagnostic{}}
	if draft.SourceType == "skill" {
		if draft.SourceSkillRevisionID == "" || draft.SourceSkillTreeHash == "" {
			out.Diagnostics = append(out.Diagnostics, authoringDiagnostic{Code: "SKILL_SNAPSHOT_REQUIRED", Severity: "error", Message: "a fixed Skill revision and tree hash are required"})
		} else if !strings.HasPrefix(draft.SourceSkillRevisionID, "builtin:") {
			var treeHash string
			err := db.Table("skill_revisions").Select("tree_hash").Where("id=? AND skill_id=?", draft.SourceSkillRevisionID, draft.SourceSkillID).Scan(&treeHash).Error
			if err != nil || treeHash != draft.SourceSkillTreeHash {
				out.Diagnostics = append(out.Diagnostics, authoringDiagnostic{Code: "SKILL_SNAPSHOT_CHANGED", Severity: "error", Message: "the fixed Skill snapshot is unavailable or changed"})
			}
		}
	}
	if _, err := workflowFiles(draft); err != nil {
		out.Diagnostics = append(out.Diagnostics, authoringDiagnostic{Code: "PACKAGE_INCOMPLETE", Severity: "error", Message: err.Error()})
	}
	compiled := graphengine.Compile(draft.WorkflowYAMLContent, draft.StateYAMLContent, draft.ScenarioContent, graphengine.ProfilePublish)
	blocking := false
	for _, diagnostic := range compiled.Diagnostics {
		out.Diagnostics = append(out.Diagnostics, authoringDiagnostic{Code: diagnostic.Code, Severity: diagnostic.Severity, Path: diagnostic.Path, Message: diagnostic.Message})
		if diagnostic.Severity == "error" {
			blocking = true
		}
	}
	if !frameworkToolsAvailableForPublish(db, draft) {
		out.Diagnostics = append(out.Diagnostics, authoringDiagnostic{Code: "FRAMEWORK_TOOL_UNAVAILABLE", Severity: "error", Message: "a mapped framework tool is unavailable"})
	}
	files, _ := workflowFiles(draft)
	if len(files) > 3 && !scriptsApprovedForPublish(db, draft) {
		out.Diagnostics = append(out.Diagnostics, authoringDiagnostic{Code: "SCRIPT_APPROVAL_REQUIRED", Severity: "error", Message: "custom scripts require a matching deterministic audit"})
	}
	sort.SliceStable(out.Diagnostics, func(i, j int) bool {
		if out.Diagnostics[i].Code == out.Diagnostics[j].Code {
			return out.Diagnostics[i].Message < out.Diagnostics[j].Message
		}
		return out.Diagnostics[i].Code < out.Diagnostics[j].Code
	})
	out.Valid = compiled.Valid && !blocking
	return out
}

func GetSkillConversionContext(w http.ResponseWriter, r *http.Request) {
	userID, skillID, revisionID := common.UserID(r), r.URL.Query().Get("skill_id"), r.URL.Query().Get("revision_id")
	var snapshot workflowSourceSkillSnapshot
	var err error
	if revisionID == "" {
		snapshot, err = loadWorkflowSourceSkill(r.Context(), store.DB(), userID, skillID)
	} else {
		snapshot, err = loadWorkflowSourceSkillRevision(r.Context(), store.DB(), userID, skillID, revisionID)
	}
	if err != nil {
		common.ReplyErr(w, "plugin source skill not found", http.StatusNotFound)
		return
	}
	common.ReplyOK(w, map[string]any{"contract_version": AuthoringContractVersion, "snapshot": snapshot})
}

func CreateAuthoringWorkflowDraft(w http.ResponseWriter, r *http.Request) {
	userID := common.UserID(r)
	var body struct {
		Name       string            `json:"name"`
		SourceType string            `json:"source_type"`
		SkillID    string            `json:"skill_id"`
		RevisionID string            `json:"revision_id"`
		TreeHash   string            `json:"tree_hash"`
		Files      map[string]string `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	if body.SourceType == "" {
		body.SourceType = "skill"
	}
	if body.SourceType != "skill" && body.SourceType != "blank" && body.SourceType != "import" {
		common.ReplyErr(w, "invalid source_type", http.StatusBadRequest)
		return
	}
	for path := range body.Files {
		if !validAuthoringPath(path) {
			common.ReplyErr(w, "invalid resource path", http.StatusBadRequest)
			return
		}
	}
	var snapshot workflowSourceSkillSnapshot
	if body.SourceType == "skill" {
		if body.SkillID == "" || body.RevisionID == "" || body.TreeHash == "" {
			common.ReplyErr(w, "skill_id, revision_id and tree_hash are required", http.StatusBadRequest)
			return
		}
		var err error
		snapshot, err = loadWorkflowSourceSkillRevision(r.Context(), store.DB(), userID, body.SkillID, body.RevisionID)
		if err != nil || snapshot.TreeHash != body.TreeHash {
			common.ReplyErr(w, "draft snapshot changed", http.StatusConflict)
			return
		}
	}
	draft := orm.WorkflowDraft{ID: uuid.NewString(), Name: body.Name, CreatedBy: userID, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Version: 1, SourceType: body.SourceType, SourceSkillID: body.SkillID, SourceSkillName: snapshot.Name, SourceSkillRevisionID: snapshot.RevisionID, SourceSkillRevisionNo: snapshot.RevisionNo, SourceSkillTreeHash: snapshot.TreeHash, ScriptsContent: "{}"}
	applyAuthoringFiles(&draft, body.Files)
	if err := store.DB().Create(&draft).Error; err != nil {
		common.ReplyErr(w, "create failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"contract_version": AuthoringContractVersion, "draft": toDraftResponse(draft)})
}

func applyAuthoringFiles(draft *orm.WorkflowDraft, files map[string]string) {
	scripts := map[string]string{}
	for path, content := range files {
		path = filepath.ToSlash(filepath.Clean(path))
		switch path {
		case "workflow.yaml":
			draft.WorkflowYAMLContent = content
		case "scenario/state.yml":
			draft.StateYAMLContent = content
		case "scenario/scenario.md":
			draft.ScenarioContent = content
		case "scenario/driver.md":
			draft.DriverContent = content
		case "scenario/layout.json":
			draft.StateLayoutContent = content
		default:
			if strings.HasPrefix(path, "scripts/") && !strings.Contains(path, "..") {
				scripts[path] = content
			}
		}
	}
	if len(scripts) > 0 {
		encoded, _ := json.Marshal(scripts)
		draft.ScriptsContent = string(encoded)
	}
}

func validAuthoringPath(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean != path || filepath.IsAbs(path) || strings.HasPrefix(clean, "../") {
		return false
	}
	switch clean {
	case "workflow.yaml", "scenario/state.yml", "scenario/scenario.md", "scenario/driver.md", "scenario/layout.json":
		return true
	}
	return strings.HasPrefix(clean, "scripts/") && len(strings.TrimPrefix(clean, "scripts/")) > 0
}

func UpdateAuthoringWorkflowDraftFile(w http.ResponseWriter, r *http.Request) {
	userID, draftID := common.UserID(r), common.PathVar(r, "draft_id")
	var body struct {
		Path            string `json:"path"`
		Content         string `json:"content"`
		ExpectedVersion int    `json:"expected_version"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Path == "" || body.ExpectedVersion < 1 {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	if !validAuthoringPath(body.Path) {
		common.ReplyErr(w, "invalid resource path", http.StatusBadRequest)
		return
	}
	var draft orm.WorkflowDraft
	if store.DB().Where("id=? AND created_by=?", draftID, userID).First(&draft).Error != nil {
		common.ReplyErr(w, "not found", http.StatusNotFound)
		return
	}
	if draft.Version != body.ExpectedVersion {
		common.ReplyErrWithData(w, "conflict", toDraftResponse(draft), http.StatusConflict)
		return
	}
	files := map[string]string{body.Path: body.Content}
	applyAuthoringFiles(&draft, files)
	// Map updates use physical persistence columns; keep legacy names centralized.
	updates := map[string]any{"state_yaml_content": draft.StateYAMLContent, "scenario_content": draft.ScenarioContent, "driver_content": draft.DriverContent, "state_layout_content": draft.StateLayoutContent, "scripts_content": draft.ScriptsContent, "version": gorm.Expr("version + 1"), "updated_at": time.Now().UTC()}
	setWorkflowYAMLUpdate(updates, draft.WorkflowYAMLContent)
	result := store.DB().Model(&orm.WorkflowDraft{}).Where("id=? AND created_by=? AND version=?", draftID, userID, body.ExpectedVersion).Updates(updates)
	if result.Error != nil {
		common.ReplyErr(w, "save failed", http.StatusInternalServerError)
		return
	}
	if result.RowsAffected == 0 {
		common.ReplyErr(w, "conflict", http.StatusConflict)
		return
	}
	store.DB().Where("id=?", draftID).First(&draft)
	common.ReplyOK(w, map[string]any{"contract_version": AuthoringContractVersion, "draft": toDraftResponse(draft)})
}

func GetAuthoringWorkflowDiagnostics(w http.ResponseWriter, r *http.Request) {
	var draft orm.WorkflowDraft
	if store.DB().Where("id=? AND created_by=?", common.PathVar(r, "draft_id"), common.UserID(r)).First(&draft).Error != nil {
		common.ReplyErr(w, "not found", 404)
		return
	}
	common.ReplyOK(w, authoringDiagnosticsForDraft(store.DB(), draft))
}

func PublishAuthoringWorkflow(w http.ResponseWriter, r *http.Request) {
	var draft orm.WorkflowDraft
	if store.DB().Where("id=? AND created_by=?", common.PathVar(r, "draft_id"), common.UserID(r)).First(&draft).Error != nil {
		common.ReplyErr(w, "not found", 404)
		return
	}
	diagnostics := authoringDiagnosticsForDraft(store.DB(), draft)
	if !diagnostics.Valid {
		common.ReplyErrWithData(w, "plugin validation failed", diagnostics, http.StatusUnprocessableEntity)
		return
	}
	PublishWorkflowDraft(w, r)
}

func GenerateAuthoringFixture(w http.ResponseWriter, r *http.Request) {
	treeHash := r.URL.Query().Get("tree_hash")
	if treeHash == "" {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	sum := sha256.Sum256([]byte(treeHash))
	id := hex.EncodeToString(sum[:16])
	files := map[string]string{"workflow.yaml": "id: " + id + "\nslots: []\nsteps:\n  - {id: execute, label: Execute}\n", "scenario/state.yml": "transitions:\n  __start__: [{to: execute}]\n  execute: [{to: __end__}]\nsteps:\n  execute: {outputs: []}\n", "scenario/scenario.md": "# Deterministic fixture\n\nThe execute step produces the requested result.\n"}
	common.ReplyOK(w, map[string]any{"contract_version": AuthoringContractVersion, "generator": "deterministic-fixture-v1", "files": files})
}
