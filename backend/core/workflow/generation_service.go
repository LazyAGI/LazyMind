package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
)

type workflowGenerationRequest struct {
	Description    string                       `json:"description"`
	SkillID        string                       `json:"skill_id"`
	StartPhase     string                       `json:"start_phase"`
	Reanalyze      bool                         `json:"reanalyze"`
	Snapshot       *workflowSourceSkillSnapshot `json:"-"`
	IdempotencyKey string                       `json:"-"`
}

type workflowServiceError struct {
	Status  int
	Message string
}

func (e *workflowServiceError) Error() string { return e.Message }

// queueWorkflowDraftGeneration runs within the caller's transaction: source
// binding, cached analysis and job admission either all persist or all roll back.
func queueWorkflowDraftGeneration(ctx context.Context, db *gorm.DB, userID, draftID string, body workflowGenerationRequest) (orm.WorkflowDraft, *workflowServiceError) {
	db = db.WithContext(ctx)
	body.Description = strings.TrimSpace(body.Description)
	body.SkillID = strings.TrimSpace(body.SkillID)
	body.StartPhase = normalizeGenerateStartPhase(body.StartPhase)
	if body.StartPhase == "" {
		return orm.WorkflowDraft{}, &workflowServiceError{Status: http.StatusBadRequest, Message: "invalid start_phase"}
	}
	if body.Description == "" && body.SkillID == "" {
		return orm.WorkflowDraft{}, &workflowServiceError{Status: http.StatusBadRequest, Message: "description or skill_id is required"}
	}

	var draft orm.WorkflowDraft
	if err := db.Where("id = ? AND created_by = ? AND deleted_at IS NULL", draftID, userID).First(&draft).Error; err != nil {
		return orm.WorkflowDraft{}, &workflowServiceError{Status: http.StatusNotFound, Message: "not found"}
	}
	if err := validateGenerateResumePoint(draft, body.StartPhase); err != nil {
		return orm.WorkflowDraft{}, &workflowServiceError{Status: http.StatusBadRequest, Message: "invalid generation resume point: " + err.Error()}
	}

	skillContent := ""
	skillName := ""
	var skillSnapshot workflowSourceSkillSnapshot
	if body.SkillID != "" {
		var snapshot workflowSourceSkillSnapshot
		var err error
		if body.Snapshot != nil {
			snapshot = *body.Snapshot
		} else {
			snapshot, err = loadWorkflowSourceSkill(ctx, db, userID, body.SkillID)
		}
		if err != nil {
			status := http.StatusInternalServerError
			if isWorkflowSourceSkillNotFound(err) {
				status = http.StatusNotFound
			}
			return orm.WorkflowDraft{}, &workflowServiceError{status, "skill not found"}
		}
		skillSnapshot = snapshot
		skillContent = snapshot.skillMD()
		skillName = snapshot.Name
	}

	sourceUpdates := map[string]any{
		"generate_status": generateStatusForStartPhase(body.StartPhase),
		"updated_at":      time.Now().UTC(),
	}
	if body.SkillID != "" && body.StartPhase == generatePhaseDesignBrief {
		sourceUpdates["generate_status"] = generateStatusAnalyzing
	}
	if body.SkillID != "" {
		sourceUpdates["source_type"] = "skill"
		sourceUpdates["source_skill_id"] = body.SkillID
		sourceUpdates["source_skill_name"] = skillName
		sourceUpdates["source_skill_revision_id"] = skillSnapshot.RevisionID
		sourceUpdates["source_skill_revision_no"] = skillSnapshot.RevisionNo
		sourceUpdates["source_skill_tree_hash"] = skillSnapshot.TreeHash
	} else if draft.SourceType == "" {
		sourceUpdates["source_type"] = "ai"
	}

	if err := db.Model(&draft).Updates(sourceUpdates).Error; err != nil {
		return orm.WorkflowDraft{}, &workflowServiceError{Status: http.StatusInternalServerError, Message: "update failed"}
	}

	var skillPackage map[string]any
	if body.SkillID != "" {
		if b, marshalErr := json.Marshal(skillSnapshot); marshalErr == nil {
			_ = json.Unmarshal(b, &skillPackage)
		}
	}
	selectedCandidateJSON := ""
	reusableScripts := map[string]string(nil)
	if body.SkillID != "" && body.StartPhase == generatePhaseDesignBrief && !body.Reanalyze {
		var cached orm.WorkflowGenerationAnalysis
		// Only a positive analysis is a reusable generated artifact. Re-run rejected
		// and confirmation-required results so analyzer improvements cannot leave a
		// Skill blocked by a stale or non-user-resolvable verdict.
		cacheErr := db.Where("user_id=? AND source_skill_id=? AND source_skill_revision_id=? AND source_skill_tree_hash=? AND status = ?", userID, body.SkillID, skillSnapshot.RevisionID, skillSnapshot.TreeHash, "generatable").Order("created_at DESC").First(&cached).Error
		if cacheErr != nil && !errors.Is(cacheErr, gorm.ErrRecordNotFound) {
			return orm.WorkflowDraft{}, &workflowServiceError{http.StatusInternalServerError, "load cached analysis failed"}
		}
		if cacheErr == nil {
			now := time.Now().UTC()
			clone := cached
			clone.ID = uuid.NewString()
			clone.DraftID = draft.ID
			clone.CreatedAt = now
			clone.UpdatedAt = now
			packageJSON, _ := json.Marshal(manifestOnlySkillPackage(skillPackage))
			clone.SourcePackageJSON = string(packageJSON)
			var cachedMappings map[string]any
			_ = json.Unmarshal([]byte(clone.ToolMappingReportJSON), &cachedMappings)
			cachedMappings = reconcileDetectedCapabilityMappings(cachedMappings, detectSkillCapabilityRequirementsFromSnapshot(skillSnapshot))
			if mappingsJSON, marshalErr := json.Marshal(cachedMappings); marshalErr == nil {
				clone.ToolMappingReportJSON = string(mappingsJSON)
			}
			if err := db.Create(&clone).Error; err != nil {
				return orm.WorkflowDraft{}, &workflowServiceError{http.StatusInternalServerError, "copy cached analysis failed"}
			}
			if err := db.Model(&draft).Updates(map[string]any{"source_analysis_id": clone.ID, "generate_status": generateStatusGenerating, "generate_error": "", "generate_warning": ignoredScriptWarningJSON(clone.ScriptReportJSON), "updated_at": now}).Error; err != nil {
				return orm.WorkflowDraft{}, &workflowServiceError{http.StatusInternalServerError, "bind cached analysis failed"}
			}
			selectedCandidateJSON = cachedAnalysisContext(clone)
			reusableScripts = reusableSkillScriptsJSON(skillPackage, clone.ScriptReportJSON)
		}
	}
	_, err := asyncjob.EnqueueInTransaction(ctx, db, asyncjob.EnqueueRequest{
		JobType:        workflowDraftGenerateJobType,
		ResourceType:   "workflow_draft",
		ResourceID:     draftID,
		IdempotencyKey: body.IdempotencyKey,
		Payload: workflowDraftGeneratePayload{
			DraftID:               draftID,
			Name:                  draft.Name,
			Description:           body.Description,
			StartPhase:            body.StartPhase,
			SkillContent:          skillContent,
			SkillPackage:          skillPackage,
			SourceSkillRevisionID: skillSnapshot.RevisionID,
			SelectedCandidateJSON: selectedCandidateJSON,
			ReusableScripts:       reusableScripts,
			UserID:                userID,
		},
		MaxAttempts:  3,
		CreateUserID: userID,
	})
	if err != nil {
		return orm.WorkflowDraft{}, &workflowServiceError{Status: http.StatusInternalServerError, Message: "enqueue failed"}
	}

	if err := db.Where("id=?", draft.ID).First(&draft).Error; err != nil {
		return orm.WorkflowDraft{}, &workflowServiceError{http.StatusInternalServerError, "read draft failed"}
	}
	return draft, nil
}
