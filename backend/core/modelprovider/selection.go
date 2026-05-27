package modelprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

// Allowed keys match runtime_models.yaml role keys (selection slot types).
var allowedSelectionModelTypes = map[string]struct{}{
	"llm":           {},
	"evo_llm":       {},
	"vlm":           {},
	"text2image":    {},
	"embed_main":    {},
	"tts":           {},
	"image_editing": {},
	"stt":           {},
	"reranker":      {},
	"embed_image":   {},
}

// autoShareModelTypes are set share=true when an admin saves a selection so other users can use them.
var autoShareModelTypes = map[string]struct{}{
	"embed_main":  {},
	"embed_image": {},
}

type selectedModelUpsertItem struct {
	ModelType string `json:"model_type"`
	ModelID   string `json:"model_id"`
}

type setSelectedModelsRequest struct {
	Selections []selectedModelUpsertItem `json:"selections"`
}

type selectedModelItem struct {
	ModelType                string `json:"model_type"`
	ModelID                  string `json:"model_id"`
	UserModelProviderID      string `json:"user_model_provider_id"`
	UserModelProviderGroupID string `json:"user_model_provider_group_id"`
	Name                     string `json:"name"`
	ProviderName             string `json:"provider_name"`
	GroupName                string `json:"group_name"`
	BaseURL                  string `json:"base_url"`
	Share                    bool   `json:"share"`
}

type selectedModelsResponse struct {
	Selections []selectedModelItem `json:"selections"`
}

type setSharedModelRequest struct {
	ModelID string `json:"model_id"`
	Share   bool   `json:"share"`
}

type modelReadyResponse struct {
	Ready        bool   `json:"ready"`
	Source       string `json:"source,omitempty"`         // "own" | "shared"
	SharedByName string `json:"shared_by_name,omitempty"` // sharer's display name
	SharedByID   string `json:"shared_by_id,omitempty"`   // sharer's user_id
	ProviderName string `json:"provider_name,omitempty"`  // e.g. "OpenAI"
	ModelName    string `json:"model_name,omitempty"`     // e.g. "text-embedding-3-small"
}

type sharedModelDetail struct {
	UserID       string
	UserName     string
	ProviderName string
	ModelName    string
}

// getSharedModelDetail returns the detail of the active shared selection for the given model_type.
// Returns nil (no error) if no shared selection exists.
func getSharedModelDetail(ctx context.Context, db *gorm.DB, modelType string) (*sharedModelDetail, error) {
	var row struct {
		UserID       string `gorm:"column:user_id"`
		UserName     string `gorm:"column:user_name"`
		ProviderName string `gorm:"column:provider_name"`
		ModelName    string `gorm:"column:model_name"`
	}
	err := db.WithContext(ctx).
		Table("user_selected_models usm").
		Joins("JOIN user_model_provider_group_models m ON m.id = usm.user_model_provider_group_model_id AND m.deleted_at IS NULL").
		Where("usm.model_type = ? AND usm.share = ?", modelType, true).
		Select("usm.user_id, usm.user_name, m.provider_name, m.name AS model_name").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sharedModelDetail{
		UserID:       row.UserID,
		UserName:     row.UserName,
		ProviderName: row.ProviderName,
		ModelName:    row.ModelName,
	}, nil
}

// GetSelectedModels returns selected model rows for the current user.
func GetSelectedModels(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	out, err := loadSelectedModels(r.Context(), db, userID)
	if err != nil {
		common.ReplyErr(w, "query selected models failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, selectedModelsResponse{Selections: out})
}

// SetSelectedModels saves selected model rows by model_type for the current user.
func SetSelectedModels(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	userName := strings.TrimSpace(store.UserName(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	var req setSelectedModelsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	if len(req.Selections) == 0 {
		common.ReplyErr(w, "selections required", http.StatusBadRequest)
		return
	}

	modelIDSet := make(map[string]struct{}, len(req.Selections))
	selectionByType := make(map[string]string, len(req.Selections))
	modelIDs := make([]string, 0, len(req.Selections))
	for _, item := range req.Selections {
		modelType := strings.TrimSpace(item.ModelType)
		modelID := strings.TrimSpace(item.ModelID)
		if modelType == "" {
			common.ReplyErr(w, "model_type is required", http.StatusBadRequest)
			return
		}
		if _, ok := allowedSelectionModelTypes[modelType]; !ok {
			common.ReplyErr(w, "invalid model_type", http.StatusBadRequest)
			return
		}
		if _, exists := selectionByType[modelType]; exists {
			common.ReplyErr(w, "duplicate model_type in selections", http.StatusBadRequest)
			return
		}
		selectionByType[modelType] = modelID
		if modelID == "" {
			continue
		}
		if _, exists := modelIDSet[modelID]; !exists {
			modelIDSet[modelID] = struct{}{}
			modelIDs = append(modelIDs, modelID)
		}
	}

	var models []orm.UserModelProviderGroupModel
	if len(modelIDs) > 0 {
		if err := db.WithContext(r.Context()).
			Where("id IN ? AND create_user_id = ? AND deleted_at IS NULL", modelIDs, userID).
			Find(&models).Error; err != nil {
			common.ReplyErr(w, "query models failed", http.StatusInternalServerError)
			return
		}
	}
	modelByID := make(map[string]orm.UserModelProviderGroupModel, len(models))
	for _, m := range models {
		modelByID[m.ID] = m
	}
	for _, modelID := range selectionByType {
		if modelID == "" {
			continue
		}
		if _, ok := modelByID[modelID]; !ok {
			common.ReplyErr(w, "model not found", http.StatusBadRequest)
			return
		}
	}

	// When embed_image is configured for the first time, clear lazy_mode so
	// image embedding runs immediately. When it is cleared, reset lazy_mode to embed.
	multimodalModelID, hasMultimodalSelection := selectionByType["embed_image"]
	triggerLazyModeClear := false
	checkLazyModeResetAfterSave := false
	if hasMultimodalSelection {
		if multimodalModelID == "" {
			checkLazyModeResetAfterSave = true
		} else {
			wasReady, err := IsModelReady(r.Context(), store.DB(), userID, "embed_image")
			if err == nil && !wasReady {
				hadAny, gerr := HasAnyMultimodalEmbeddingSelection(r.Context(), store.DB())
				if gerr == nil && !hadAny {
					triggerLazyModeClear = true
				}
			}
		}
	}

	now := time.Now()
	if err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		for modelType, modelID := range selectionByType {
			if modelID == "" {
				if err := tx.Where("user_id = ? AND model_type = ?", userID, modelType).
					Delete(&orm.UserSelectedModel{}).Error; err != nil {
					return err
				}
				continue
			}
			_, autoShare := autoShareModelTypes[modelType]
			if autoShare {
				if err := tx.Model(&orm.UserSelectedModel{}).
					Where("model_type = ? AND share = ?", modelType, true).
					Updates(map[string]any{"share": false, "updated_at": now}).Error; err != nil {
					return err
				}
			}
			var row orm.UserSelectedModel
			err := tx.Where("user_id = ? AND model_type = ?", userID, modelType).Take(&row).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				createFields := map[string]any{
					"user_id":                            userID,
					"model_type":                         modelType,
					"user_model_provider_group_model_id": modelID,
					"user_name":                          userName,
					"created_at":                         now,
					"updated_at":                         now,
				}
				if autoShare {
					createFields["share"] = true
				}
				if err := tx.Model(&orm.UserSelectedModel{}).Create(createFields).Error; err != nil {
					return err
				}
			} else if err != nil {
				return err
			} else {
				updateFields := map[string]any{
					"user_model_provider_group_model_id": modelID,
					"user_name":                          userName,
					"updated_at":                         now,
				}
				if autoShare {
					updateFields["share"] = true
				}
				if err := tx.Model(&orm.UserSelectedModel{}).
					Where("id = ?", row.ID).
					Updates(updateFields).Error; err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		common.ReplyErr(w, "save selected models failed", http.StatusInternalServerError)
		return
	}

	if triggerLazyModeClear {
		scheduleImageGroupLazyClear(r.Context())
	}
	if checkLazyModeResetAfterSave {
		maybeScheduleImageGroupLazyReset(r.Context(), db)
	}

	out, err := loadSelectedModels(r.Context(), db, userID)
	if err != nil {
		common.ReplyErr(w, "query selected models failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, selectedModelsResponse{Selections: out})
}

func loadSelectedModels(ctx context.Context, db *gorm.DB, userID string) ([]selectedModelItem, error) {
	out := make([]selectedModelItem, 0)
	err := db.WithContext(ctx).
		Table("user_selected_models usm").
		Select(
			"usm.model_type, "+
				"usm.user_model_provider_group_model_id AS model_id, "+
				"usm.share, "+
				"m.user_model_provider_id, "+
				"m.user_model_provider_group_id, "+
				"m.name, "+
				"m.provider_name, "+
				"g.name AS group_name, "+
				"g.base_url",
		).
		Joins(
			"JOIN user_model_provider_group_models m ON "+
				"m.id = usm.user_model_provider_group_model_id AND "+
				"m.create_user_id = usm.user_id AND "+
				"m.deleted_at IS NULL",
		).
		Joins(
			"JOIN user_model_provider_groups g ON "+
				"g.id = m.user_model_provider_group_id AND "+
				"g.create_user_id = usm.user_id AND "+
				"g.deleted_at IS NULL",
		).
		Where("usm.user_id = ?", strings.TrimSpace(userID)).
		Order("usm.model_type ASC").
		Scan(&out).Error
	return out, err
}

// SetSharedModel sets or clears the share flag for a selected model row.
// Protected by document.write permission (admin only).
func SetSharedModel(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	var req setSharedModelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	modelID := strings.TrimSpace(req.ModelID)
	if modelID == "" {
		common.ReplyErr(w, "model_id is required", http.StatusBadRequest)
		return
	}

	now := time.Now()
	err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		// Look up the row to get its model_type (needed to clear other share=true rows of the same type).
		var row orm.UserSelectedModel
		if err := tx.Where("user_model_provider_group_model_id = ?", modelID).
			First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("not found")
			}
			return err
		}
		if req.Share {
			// Clear any existing share=true for this model_type first.
			if err := tx.Model(&orm.UserSelectedModel{}).
				Where("model_type = ? AND share = ?", row.ModelType, true).
				Updates(map[string]any{"share": false, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		return tx.Model(&orm.UserSelectedModel{}).
			Where("user_model_provider_group_model_id = ?", modelID).
			Updates(map[string]any{"share": req.Share, "updated_at": now}).Error
	})
	if err != nil {
		if err.Error() == "not found" {
			common.ReplyErr(w, "model not found in selected models", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "update share failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"ok": true})
}

// GetModelReady checks whether a model of the given model_type is ready for the current user.
// It first checks the user's own selection, then falls back to any share=true row.
func GetModelReady(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	modelType := strings.TrimSpace(r.URL.Query().Get("model_type"))
	if modelType == "" {
		common.ReplyErr(w, "model_type is required", http.StatusBadRequest)
		return
	}

	ownCount, err := countValidModelSelection(r.Context(), db, userID, modelType, false)
	if err != nil {
		common.ReplyErr(w, "query failed", http.StatusInternalServerError)
		return
	}
	if ownCount > 0 {
		common.ReplyOK(w, modelReadyResponse{Ready: true, Source: "own"})
		return
	}

	sharedCount, err := countValidModelSelection(r.Context(), db, userID, modelType, true)
	if err != nil {
		common.ReplyErr(w, "query failed", http.StatusInternalServerError)
		return
	}
	if sharedCount > 0 {
		detail, detailErr := getSharedModelDetail(r.Context(), db, modelType)
		if detailErr != nil {
			common.ReplyOK(w, modelReadyResponse{Ready: true, Source: "shared"})
			return
		}
		resp := modelReadyResponse{Ready: true, Source: "shared"}
		if detail != nil {
			resp.SharedByName = detail.UserName
			resp.SharedByID = detail.UserID
			resp.ProviderName = detail.ProviderName
			resp.ModelName = detail.ModelName
		}
		common.ReplyOK(w, resp)
		return
	}

	common.ReplyOK(w, modelReadyResponse{Ready: false})
}

// HasAnyMultimodalEmbeddingSelection reports whether any user still has a valid
// embed_image selection (joined model row not soft-deleted).
func HasAnyMultimodalEmbeddingSelection(ctx context.Context, db *gorm.DB) (bool, error) {
	var count int64
	err := db.WithContext(ctx).
		Table("user_selected_models usm").
		Joins(
			"JOIN user_model_provider_group_models m ON "+
				"m.id = usm.user_model_provider_group_model_id AND "+
				"m.deleted_at IS NULL",
		).
		Where("usm.model_type = ?", "embed_image").
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func maybeScheduleImageGroupLazyReset(ctx context.Context, db *gorm.DB) {
	if !GetCachedModelFeatures().ImageEmbedEnabled {
		return
	}
	hasAny, err := HasAnyMultimodalEmbeddingSelection(ctx, db)
	if err != nil || hasAny {
		return
	}
	scheduleImageGroupLazyEmbed(ctx)
}

// countValidModelSelection counts selections whose model row still exists (not soft-deleted).
// When sharedOnly is true, only share=true rows are counted (any user).
func countValidModelSelection(ctx context.Context, db *gorm.DB, userID, modelType string, sharedOnly bool) (int64, error) {
	var count int64
	q := db.WithContext(ctx).
		Table("user_selected_models usm").
		Joins(
			"JOIN user_model_provider_group_models m ON "+
				"m.id = usm.user_model_provider_group_model_id AND "+
				"m.deleted_at IS NULL",
		).
		Where("usm.model_type = ?", modelType)
	if sharedOnly {
		q = q.Where("usm.share = ?", true)
	} else {
		q = q.Where("usm.user_id = ?", userID)
	}
	err := q.Count(&count).Error
	return count, err
}

// IsModelReady checks whether a model of the given model_type is available for the user.
// It first checks the user's own valid selection, then falls back to any valid share=true row.
func IsModelReady(ctx context.Context, db *gorm.DB, userID, modelType string) (bool, error) {
	ownCount, err := countValidModelSelection(ctx, db, userID, modelType, false)
	if err != nil {
		return false, err
	}
	if ownCount > 0 {
		return true, nil
	}
	sharedCount, err := countValidModelSelection(ctx, db, userID, modelType, true)
	if err != nil {
		return false, err
	}
	return sharedCount > 0, nil
}
