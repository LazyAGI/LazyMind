package modelconfig

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
)

// EvolutionValidationConfig is only for trusted offline validation tooling. It
// uses the same authorization, connection and adapter checks as task creation.
// Never expose its configuration through a browser API.
func EvolutionValidationConfig(ctx context.Context, db *gorm.DB, userID, modelID string) (map[string]any, EvolutionModelSummary, error) {
	list, configs, err := loadEvolutionModels(ctx, db, userID)
	if err != nil {
		return nil, EvolutionModelSummary{}, err
	}
	for _, model := range list.Models {
		if (modelID == "" && model.ModelRef == list.AvailableDefaultRef) ||
			(modelID != "" && strings.HasPrefix(model.ModelRef, "local:"+modelID+":")) || model.ModelRef == modelID {
			config, err := LoadLLMConfigWithEvolution(ctx, db, userID, configs[model.ModelRef])
			return config, model, err
		}
	}
	return nil, EvolutionModelSummary{}, ErrEvolutionModelUnavailable
}

type EvolutionValidationReport struct {
	ModelRef          string            `json:"model_ref"`
	Nonce             string            `json:"nonce"`
	ValidationVersion string            `json:"validation_version"`
	Passed            bool              `json:"passed"`
	Checks            map[string]bool   `json:"checks"`
	Failures          map[string]string `json:"failures"`
	Workflow          struct {
		ThreadID       string            `json:"thread_id"`
		Stages         []string          `json:"stages"`
		ArtifactSHA256 map[string]string `json:"artifact_sha256"`
		AttemptCount   int               `json:"attempt_count"`
	} `json:"workflow"`
}

func (r EvolutionValidationReport) Valid(modelRef, nonce string) bool {
	if !r.Passed || r.ModelRef != modelRef || r.Nonce != nonce || r.ValidationVersion != EvolutionValidationVersion || len(r.Failures) != 0 {
		return false
	}
	for _, name := range []string{"planning", "dataset_generation", "evaluation", "code_edit", "workflow"} {
		if !r.Checks[name] {
			return false
		}
	}
	if r.Workflow.ThreadID == "" || r.Workflow.AttemptCount < 5 || len(r.Workflow.Stages) != 5 {
		return false
	}
	for i, name := range []string{"dataset", "eval", "analysis", "repair", "abtest"} {
		if r.Workflow.Stages[i] != name || len(r.Workflow.ArtifactSHA256[name]) != 64 {
			return false
		}
	}
	return true
}

// SaveEvolutionValidation re-resolves the exact authorized configuration after
// execution. A configuration edit or permission revocation invalidates the run.
func SaveEvolutionValidation(ctx context.Context, db *gorm.DB, userID, modelRef, nonce, evidenceID string, report EvolutionValidationReport) error {
	_, model, err := EvolutionValidationConfig(ctx, db, userID, modelRef)
	if err != nil || model.ModelRef != modelRef {
		return ErrEvolutionModelUnavailable
	}
	if evidenceID == "" || len(evidenceID) > 255 || nonce == "" {
		return errors.New("invalid validation evidence")
	}
	now := time.Now().UTC()
	evidence := orm.EvolutionModelValidation{ModelRef: modelRef, ValidationVersion: EvolutionValidationVersion,
		EvidenceID: evidenceID, Passed: report.Valid(modelRef, nonce), VerifiedAt: now}
	return db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&evidence).Error
}
