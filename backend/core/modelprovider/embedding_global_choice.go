package modelprovider

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// GlobalEmbeddingChoice is the one text-embedding runtime used by every user.
// Legacy choices are deterministic but unsigned: they never prove that old
// vectors were encoded by that runtime.
type GlobalEmbeddingChoice struct {
	ModelID      string
	Source       string
	OwnerID      string
	ModelName    string
	ProviderName string
	Phase        string
	Legacy       bool
}

func ResolveGlobalEmbeddingChoice(ctx context.Context, db *gorm.DB) (GlobalEmbeddingChoice, bool, error) {
	if !db.Migrator().HasTable(&orm.EmbeddingIndexState{}) {
		return GlobalEmbeddingChoice{}, false, nil
	}
	var state orm.EmbeddingIndexState
	err := db.WithContext(ctx).Where("scope = ?", globalEmbeddingScope).Take(&state).Error
	if err == nil {
		phase := state.Phase
		if state.ActiveModelID != "" && !validEmbeddingFingerprint(state.Signature) && phase != "clearing" && phase != "cleanup_failed" {
			phase = "unverified"
		}
		choice := GlobalEmbeddingChoice{
			ModelID: state.ActiveModelID, Source: state.ActiveSource,
			ModelName: state.ActiveModelName, ProviderName: state.ActiveProviderName, Phase: phase,
		}
		if choice.Source == "own" && choice.ModelID != "" {
			var row struct{ CreateUserID string }
			if err := db.WithContext(ctx).Table("user_model_provider_group_models").Select("create_user_id").Where("id = ?", choice.ModelID).Take(&row).Error; err == nil {
				choice.OwnerID = row.CreateUserID
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return GlobalEmbeddingChoice{}, false, err
			}
		}
		return choice, true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return GlobalEmbeddingChoice{}, false, err
	}
	// Upgrade path: converge future runtime calls on one pre-existing selection.
	// A signed state is created only by a new, successful selection probe.
	var legacy struct {
		ModelID      string `gorm:"column:model_id"`
		OwnerID      string `gorm:"column:owner_id"`
		ModelName    string `gorm:"column:model_name"`
		ProviderName string `gorm:"column:provider_name"`
	}
	err = db.WithContext(ctx).Table("user_selected_models usm").
		Select("m.id AS model_id, usm.user_id AS owner_id, m.name AS model_name, p.name AS provider_name").
		Joins("JOIN user_model_provider_group_models m ON m.id = usm.user_model_provider_group_model_id AND m.create_user_id = usm.user_id AND m.deleted_at IS NULL").
		Joins("JOIN user_model_provider_groups g ON g.id = m.user_model_provider_group_id AND g.user_model_provider_id = m.user_model_provider_id AND g.create_user_id = m.create_user_id AND g.deleted_at IS NULL").
		Joins("JOIN user_model_providers p ON p.id = m.user_model_provider_id AND p.create_user_id = m.create_user_id AND p.deleted_at IS NULL").
		Where("usm.model_type = ? AND m.model_type IN ? AND m.enabled = ?", "embed_main", []string{"embed", "embedding", "embed_main"}, true).
		Order("usm.share DESC, usm.updated_at DESC, usm.user_id ASC, m.id ASC").Take(&legacy).Error
	if err == nil {
		phase, phaseErr := legacyEmbeddingPhase(ctx, db)
		if phaseErr != nil {
			return GlobalEmbeddingChoice{}, false, phaseErr
		}
		return GlobalEmbeddingChoice{
			ModelID: legacy.ModelID, Source: "own", OwnerID: legacy.OwnerID,
			ModelName: legacy.ModelName, ProviderName: legacy.ProviderName, Phase: phase, Legacy: true,
		}, true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return GlobalEmbeddingChoice{}, false, err
	}
	if db.Migrator().HasTable(&orm.UserSelectedCloudModel{}) {
		var cloud orm.UserSelectedCloudModel
		err := db.WithContext(ctx).Where("model_type = ?", "embed_main").Order("updated_at DESC, user_id ASC").Take(&cloud).Error
		if err == nil {
			phase, phaseErr := legacyEmbeddingPhase(ctx, db)
			if phaseErr != nil {
				return GlobalEmbeddingChoice{}, false, phaseErr
			}
			return GlobalEmbeddingChoice{ModelID: cloud.PublicModelKey, Source: "cloud", ModelName: cloud.DisplayNameSnapshot, ProviderName: CloudSystemProviderName, Phase: phase, Legacy: true}, true, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return GlobalEmbeddingChoice{}, false, err
		}
	}
	return GlobalEmbeddingChoice{}, false, nil
}

func validEmbeddingFingerprint(value string) bool {
	parts := strings.Split(value, ":")
	if len(parts) != 4 || parts[0] != "v1" || !embeddingSignaturePattern.MatchString(parts[2]) || !embeddingSignaturePattern.MatchString(parts[3]) {
		return false
	}
	dimensions, err := strconv.Atoi(parts[1])
	return err == nil && dimensions > 0 && dimensions <= 65536
}

// Before the global state was introduced, users could index with different
// models. No single historical selection can identify all of those vectors.
func legacyEmbeddingPhase(ctx context.Context, db *gorm.DB) (string, error) {
	if !db.Migrator().HasTable(&orm.Dataset{}) {
		return "", nil
	}
	nonIndexedLevels := []string{"stored", "parsed", "chunked"}
	var affected int64
	query := db.WithContext(ctx).Table("datasets d").Where("d.deleted_at IS NULL")
	if db.Migrator().HasTable(&orm.DocumentProcessingState{}) {
		query = query.Joins("LEFT JOIN document_processing_states s ON s.dataset_id = d.id").
			Where("d.processing_level NOT IN ? OR s.index_status = ?", nonIndexedLevels, "succeeded").Distinct("d.id")
	} else {
		query = query.Where("d.processing_level NOT IN ?", nonIndexedLevels)
	}
	if err := query.Count(&affected).Error; err != nil {
		return "", err
	}
	if affected > 0 {
		return "unverified", nil
	}
	return "", nil
}

func globalEmbeddingReadiness(ctx context.Context, db *gorm.DB, userID string) (modelReadyResponse, bool, error) {
	choice, present, err := ResolveGlobalEmbeddingChoice(ctx, db)
	if err != nil || !present {
		return modelReadyResponse{}, present, err
	}
	response := modelReadyResponse{
		Source: choice.Source, ProviderName: choice.ProviderName,
		ModelName: choice.ModelName, Reason: "model_unavailable",
	}
	if choice.Source == "own" && choice.OwnerID != "" && choice.OwnerID != userID {
		response.Source = "shared"
		response.SharedByID = choice.OwnerID
	}
	if choice.ModelID == "" || choice.Phase == "clearing" || choice.Phase == "cleanup_failed" || choice.Phase == "unverified" {
		return response, true, nil
	}
	if choice.Source == "cloud" {
		catalog, _ := ResolveCloudModelCatalog(ctx)
		response.CloudPlanURL = catalog.PlanURL
		if model := FindCloudCatalogModel(catalog, "embed_main", choice.ModelID); model != nil {
			response.ModelName = model.DisplayName
			response.Ready = cloudCatalogModelUsable(*model)
			if response.Ready {
				response.Reason = ""
			}
		} else if catalog.Reason != "" {
			response.Reason = catalog.Reason
		}
		return response, true, nil
	}
	if choice.Source != "own" {
		return response, true, nil
	}
	var model orm.UserModelProviderGroupModel
	if err := db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", choice.ModelID).Take(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response, true, nil
		}
		return modelReadyResponse{}, true, err
	}
	if !model.Enabled || !isTextEmbeddingModelType(model.ModelType) {
		return response, true, nil
	}
	var parent orm.UserModelProvider
	if err := db.WithContext(ctx).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", model.UserModelProviderID, model.CreateUserID).Take(&parent).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response, true, nil
		}
		return modelReadyResponse{}, true, err
	}
	baseURL, _, credentialErr := ResolveProviderRuntimeCredential(ctx, db, model.UserModelProviderGroupID)
	if credentialErr != nil {
		response.Reason = "credentials_unavailable"
		return response, true, nil
	}
	response.Ready = modelSupportsSelectionForProvider(ctx, db, parent, model, "embed_main", baseURL)
	if response.Ready {
		response.Reason = ""
	}
	return response, true, nil
}
