package modelconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// Bump when the Evo capability contract or its adapter changes incompatibly.
const EvolutionValidationVersion = "evo-opencode-v1"

var ErrEvolutionModelUnavailable = errors.New("evolution model unavailable")

type EvolutionModelSummary struct {
	ModelRef          string `json:"model_ref"`
	DisplayName       string `json:"display_name"`
	ProviderName      string `json:"provider_name"`
	Source            string `json:"source"`
	ValidationStatus  string `json:"validation_status"`
	ValidationVersion string `json:"validation_version,omitempty"`
}

type EvolutionModels struct {
	Models              []EvolutionModelSummary `json:"models"`
	ConfiguredDefault   *EvolutionModelSummary  `json:"configured_default,omitempty"`
	AvailableDefaultRef string                  `json:"available_default_ref,omitempty"`
	CanSelect           bool                    `json:"can_select"`
	UnavailableReason   string                  `json:"unavailable_reason,omitempty"`
}

type evolutionCandidate struct {
	SelectedRuntimeModel
	ID                string
	OwnerID           string
	GroupID           string
	Verified          bool
	CredentialVersion int
	ModelUpdatedAt    time.Time
	GroupUpdatedAt    time.Time
}

func (row evolutionCandidate) summary(userID string) EvolutionModelSummary {
	// Bind the reference to the exact configuration and adapter, without exposing
	// endpoint or credentials. Updated timestamps also invalidate legacy key edits.
	descriptor, _ := openCodeDescriptor(row.SelectedRuntimeModel)
	raw, _ := json.Marshal([]any{row.ID, row.OwnerID, row.GroupID, row.ProviderName, row.ModelName, row.TechnicalModelType, row.BaseURL, row.MaxInputTokens, row.CredentialVersion, row.ModelUpdatedAt.UTC(), row.GroupUpdatedAt.UTC(), descriptor, EvolutionValidationVersion})
	hash := sha256.Sum256(raw)
	source := "personal"
	if row.OwnerID != userID {
		source = "shared"
	}
	return EvolutionModelSummary{ModelRef: "local:" + row.ID + ":" + hex.EncodeToString(hash[:]), DisplayName: row.ModelName, ProviderName: row.ProviderName, Source: source, ValidationStatus: "unverified"}
}

func evolutionCandidates(ctx context.Context, db *gorm.DB, userID string) ([]evolutionCandidate, string, error) {
	var rows []evolutionCandidate
	// Reuse the existing personal / explicitly shared evo_llm selection boundary.
	err := db.WithContext(ctx).Table("user_model_provider_group_models m").
		Select("m.id, m.create_user_id AS owner_id, m.user_model_provider_group_id AS group_id, m.provider_name, m.name AS model_name, m.model_type AS technical_model_type, m.is_default, m.max_input_tokens, m.updated_at AS model_updated_at, g.updated_at AS group_updated_at, g.base_url, g.api_key, g.api_key_ciphertext, g.credential_version, g.is_verified AS verified").
		Joins("JOIN user_model_providers p ON p.id = m.user_model_provider_id AND p.create_user_id = m.create_user_id AND p.deleted_at IS NULL AND p.capabilities LIKE ?", "%has_models%").
		Joins("JOIN user_model_provider_groups g ON g.id = m.user_model_provider_group_id AND g.user_model_provider_id = m.user_model_provider_id AND g.create_user_id = m.create_user_id AND g.deleted_at IS NULL").
		Where("m.deleted_at IS NULL AND (m.create_user_id = ? OR EXISTS (SELECT 1 FROM user_selected_models s WHERE s.user_model_provider_group_model_id = m.id AND s.user_id = m.create_user_id AND s.model_type = ? AND s.share = ?))", userID, "evo_llm", true).
		Order("m.name, m.id").Scan(&rows).Error
	if err != nil {
		return nil, "", err
	}
	var selections []orm.UserSelectedModel
	err = db.WithContext(ctx).Where("model_type = ? AND (user_id = ? OR share = ?)", "evo_llm", userID, true).Order("updated_at DESC, id DESC").Find(&selections).Error
	if err != nil {
		return nil, "", err
	}
	defaultID := ""
	for _, s := range selections {
		if s.UserID == userID {
			defaultID = s.UserModelProviderGroupModelID
			break
		}
		if defaultID == "" {
			defaultID = s.UserModelProviderGroupModelID
		}
	}
	return rows, defaultID, nil
}

func loadEvolutionModels(ctx context.Context, db *gorm.DB, userID string) (EvolutionModels, map[string]map[string]any, error) {
	out := EvolutionModels{Models: []EvolutionModelSummary{}}
	configs := map[string]map[string]any{}
	if strings.TrimSpace(userID) == "" {
		return out, configs, ErrEvolutionModelUnavailable
	}
	rows, defaultID, err := evolutionCandidates(ctx, db, userID)
	if err != nil {
		return out, nil, err
	}
	out.UnavailableReason = "not_configured"
	if defaultID != "" {
		out.UnavailableReason = "unavailable"
	}
	refs := make([]string, 0, len(rows))
	for _, row := range rows {
		refs = append(refs, row.summary(userID).ModelRef)
	}
	var evidence []orm.EvolutionModelValidation
	if len(refs) > 0 {
		if err := db.WithContext(ctx).Where("model_ref IN ? AND validation_version = ?", refs, EvolutionValidationVersion).Find(&evidence).Error; err != nil {
			return out, nil, err
		}
	}
	validationStatus := map[string]string{}
	now := time.Now().UTC()
	for _, e := range evidence {
		if strings.TrimSpace(e.EvidenceID) == "" || e.VerifiedAt.IsZero() || e.VerifiedAt.After(now) {
			continue
		}
		validationStatus[e.ModelRef] = "failed"
		if e.Passed {
			validationStatus[e.ModelRef] = "passed"
		}
	}
	for _, row := range rows {
		summary := row.summary(userID)
		// Workflow reports describe acceptance results, not model admission.
		if status := validationStatus[summary.ModelRef]; status != "" {
			summary.ValidationStatus = status
			summary.ValidationVersion = EvolutionValidationVersion
		}
		isDefault := row.ID == defaultID
		if isDefault {
			copy := summary
			out.ConfiguredDefault = &copy
		}
		row.ModelType = "evo_llm"
		if !row.Verified {
			if isDefault {
				out.UnavailableReason = "connection_unverified"
			}
			continue
		}
		if strings.TrimSpace(row.BaseURL) == "" {
			if isDefault {
				out.UnavailableReason = "configuration_incomplete"
			}
			continue
		}
		if _, ok := openCodeDescriptor(row.SelectedRuntimeModel); !ok {
			if isDefault {
				out.UnavailableReason = "incompatible"
			}
			continue
		}
		models := []SelectedRuntimeModel{row.SelectedRuntimeModel}
		if err := decryptRuntimeModels(models); err != nil {
			if isDefault {
				out.UnavailableReason = "credentials_unavailable"
			}
			continue
		}
		if strings.TrimSpace(models[0].APIKey) == "" || strings.TrimSpace(models[0].BaseURL) == "" {
			if isDefault {
				out.UnavailableReason = "configuration_incomplete"
			}
			continue
		}
		config, ok := BuildLLMConfig(models)["evo_llm"].(map[string]any)
		if !ok {
			continue
		}
		out.Models = append(out.Models, summary)
		configs[summary.ModelRef] = config
		if isDefault {
			out.AvailableDefaultRef = summary.ModelRef
			out.ConfiguredDefault = &summary
			out.UnavailableReason = ""
		}
	}
	out.CanSelect = len(out.Models) > 0
	return out, configs, nil
}

func LoadEvolutionModels(ctx context.Context, db *gorm.DB, userID string) (EvolutionModels, error) {
	out, _, err := loadEvolutionModels(ctx, db, userID)
	return out, err
}

func ResolveEvolutionModel(ctx context.Context, db *gorm.DB, userID, ref string) (map[string]any, EvolutionModelSummary, error) {
	out, configs, err := loadEvolutionModels(ctx, db, userID)
	if err != nil {
		return nil, EvolutionModelSummary{}, err
	}
	if ref == "" {
		ref = out.AvailableDefaultRef
	}
	for _, model := range out.Models {
		if model.ModelRef == ref {
			return configs[ref], model, nil
		}
	}
	return nil, EvolutionModelSummary{}, ErrEvolutionModelUnavailable
}
