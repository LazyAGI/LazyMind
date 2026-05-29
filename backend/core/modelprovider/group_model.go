package modelprovider

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type addGroupModelRequest struct {
	Name      string `json:"name"`
	ModelType string `json:"model_type"`
}

type addGroupModelResponse struct {
	ID                       string `json:"id"`
	UserModelProviderID      string `json:"user_model_provider_id"`
	UserModelProviderGroupID string `json:"user_model_provider_group_id"`
	Name                     string `json:"name"`
	ModelType                string `json:"model_type"`
	ProviderName             string `json:"provider_name"`
	GroupName                string `json:"group_name"`
	BaseURL                  string `json:"base_url"`
	IsDefault                bool   `json:"is_default"`
}

type groupModelListItem struct {
	ID                       string `json:"id"`
	UserModelProviderID      string `json:"user_model_provider_id"`
	UserModelProviderGroupID string `json:"user_model_provider_group_id"`
	Name                     string `json:"name"`
	ModelType                string `json:"model_type"`
	ProviderName             string `json:"provider_name"`
	GroupName                string `json:"group_name"`
	BaseURL                  string `json:"base_url"`
	IsDefault                bool   `json:"is_default"`
}

type groupModelListResponse struct {
	Models []groupModelListItem `json:"models"`
}

// AddGroupModel inserts a user-defined model row under a connection group (custom model name and model_type).
func AddGroupModel(w http.ResponseWriter, r *http.Request) {
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

	parentID := strings.TrimSpace(mux.Vars(r)["model_provider_id"])
	groupID := strings.TrimSpace(mux.Vars(r)["group_id"])
	if parentID == "" || groupID == "" {
		common.ReplyErr(w, "missing model_provider_id or group_id", http.StatusBadRequest)
		return
	}

	var req addGroupModelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	modelType := strings.TrimSpace(req.ModelType)
	if name == "" || modelType == "" {
		common.ReplyErr(w, "name and model_type are required", http.StatusBadRequest)
		return
	}

	var parent orm.UserModelProvider
	err := db.WithContext(r.Context()).
		Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", parentID, userID).
		Take(&parent).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "model provider not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query model provider failed", http.StatusInternalServerError)
		return
	}

	if !parent.HasCapability("has_models") {
		common.ReplyErr(w, "this provider does not support models", http.StatusBadRequest)
		return
	}

	var group orm.UserModelProviderGroup
	err = db.WithContext(r.Context()).
		Where("id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", groupID, parent.ID, userID).
		Take(&group).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "group not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query group failed", http.StatusInternalServerError)
		return
	}

	var dupUser int64
	if err := db.WithContext(r.Context()).Model(&orm.UserModelProviderGroupModel{}).
		Where(
			"user_model_provider_group_id = ? AND create_user_id = ? AND deleted_at IS NULL AND name = ?",
			group.ID, userID, name,
		).Count(&dupUser).Error; err != nil {
		common.ReplyErr(w, "check existing model failed", http.StatusInternalServerError)
		return
	}
	if dupUser > 0 {
		common.ReplyErr(w, "model name already exists in this group", http.StatusConflict)
		return
	}

	now := time.Now()
	row := orm.UserModelProviderGroupModel{
		ID:                       common.GenerateID(),
		UserModelProviderID:      parent.ID,
		UserModelProviderGroupID: group.ID,
		ProviderName:             parent.Name,
		Name:                     name,
		ModelType:                modelType,
		IsDefault:                false,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userName,
			CreatedAt:      now,
			UpdatedAt:      now,
			DeletedAt:      nil,
		},
	}
	if err := db.WithContext(r.Context()).Create(&row).Error; err != nil {
		common.ReplyErr(w, "create model failed", http.StatusInternalServerError)
		return
	}

	common.ReplyOK(w, addGroupModelResponse{
		ID:                       row.ID,
		UserModelProviderID:      row.UserModelProviderID,
		UserModelProviderGroupID: row.UserModelProviderGroupID,
		Name:                     row.Name,
		ModelType:                row.ModelType,
		ProviderName:             row.ProviderName,
		GroupName:                group.Name,
		BaseURL:                  group.BaseURL,
		IsDefault:                row.IsDefault,
	})
}

// ListGroupModels returns active models under a connection group.
func ListGroupModels(w http.ResponseWriter, r *http.Request) {
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

	parentID := strings.TrimSpace(mux.Vars(r)["model_provider_id"])
	groupID := strings.TrimSpace(mux.Vars(r)["group_id"])
	if parentID == "" || groupID == "" {
		common.ReplyErr(w, "missing model_provider_id or group_id", http.StatusBadRequest)
		return
	}

	var parent orm.UserModelProvider
	err := db.WithContext(r.Context()).
		Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", parentID, userID).
		Take(&parent).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "model provider not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query model provider failed", http.StatusInternalServerError)
		return
	}

	// Providers without has_models return an empty list rather than an error.
	if !parent.HasCapability("has_models") {
		common.ReplyOK(w, groupModelListResponse{Models: []groupModelListItem{}})
		return
	}

	var group orm.UserModelProviderGroup
	err = db.WithContext(r.Context()).
		Where("id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", groupID, parent.ID, userID).
		Take(&group).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "group not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query group failed", http.StatusInternalServerError)
		return
	}

	var rows []orm.UserModelProviderGroupModel
	if err := db.WithContext(r.Context()).
		Where(
			"user_model_provider_group_id = ? AND create_user_id = ? AND deleted_at IS NULL",
			group.ID, userID,
		).
		Order("name ASC").
		Find(&rows).Error; err != nil {
		common.ReplyErr(w, "list models failed", http.StatusInternalServerError)
		return
	}

	out := make([]groupModelListItem, 0, len(rows))
	for i := range rows {
		m := rows[i]
		out = append(out, groupModelListItem{
			ID:                       m.ID,
			UserModelProviderID:      m.UserModelProviderID,
			UserModelProviderGroupID: m.UserModelProviderGroupID,
			Name:                     m.Name,
			ModelType:                m.ModelType,
			ProviderName:             m.ProviderName,
			GroupName:                group.Name,
			BaseURL:                  group.BaseURL,
			IsDefault:                m.IsDefault,
		})
	}
	common.ReplyOK(w, groupModelListResponse{Models: out})
}

// ListUserModelsByModelType lists the current user's models across all user_model_providers,
// filtered by required query model_type. Response shape matches ListGroupModels.
func ListUserModelsByModelType(w http.ResponseWriter, r *http.Request) {
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

	var rows []orm.UserModelProviderGroupModel
	if err := db.WithContext(r.Context()).
		Joins("JOIN user_model_providers ON user_model_providers.id = user_model_provider_group_models.user_model_provider_id AND user_model_providers.deleted_at IS NULL AND user_model_providers.capabilities LIKE '%has_models%'").
		Where("user_model_provider_group_models.create_user_id = ? AND user_model_provider_group_models.deleted_at IS NULL AND user_model_provider_group_models.model_type = ?", userID, modelType).
		Order("user_model_provider_group_models.user_model_provider_id ASC, user_model_provider_group_models.user_model_provider_group_id ASC, user_model_provider_group_models.name ASC").
		Find(&rows).Error; err != nil {
		common.ReplyErr(w, "list models failed", http.StatusInternalServerError)
		return
	}

	groupIDs := make([]string, 0)
	seenGroup := make(map[string]struct{})
	for i := range rows {
		gid := rows[i].UserModelProviderGroupID
		if _, ok := seenGroup[gid]; !ok {
			seenGroup[gid] = struct{}{}
			groupIDs = append(groupIDs, gid)
		}
	}

	type groupInfo struct {
		name       string
		baseURL    string
		isVerified bool
	}
	groupByID := make(map[string]groupInfo)
	if len(groupIDs) > 0 {
		var grps []orm.UserModelProviderGroup
		if err := db.WithContext(r.Context()).
			Where("id IN ? AND create_user_id = ? AND deleted_at IS NULL", groupIDs, userID).
			Find(&grps).Error; err != nil {
			common.ReplyErr(w, "list groups failed", http.StatusInternalServerError)
			return
		}
		for i := range grps {
			groupByID[grps[i].ID] = groupInfo{name: grps[i].Name, baseURL: grps[i].BaseURL, isVerified: grps[i].IsVerified}
		}
	}

	out := make([]groupModelListItem, 0, len(rows))
	for i := range rows {
		m := rows[i]
		grp, ok := groupByID[m.UserModelProviderGroupID]
		if !ok || !grp.isVerified {
			continue
		}
		out = append(out, groupModelListItem{
			ID:                       m.ID,
			UserModelProviderID:      m.UserModelProviderID,
			UserModelProviderGroupID: m.UserModelProviderGroupID,
			Name:                     m.Name,
			ModelType:                m.ModelType,
			ProviderName:             m.ProviderName,
			GroupName:                grp.name,
			BaseURL:                  grp.baseURL,
			IsDefault:                m.IsDefault,
		})
	}
	common.ReplyOK(w, groupModelListResponse{Models: out})
}

type deleteGroupModelResponse struct {
	ID string `json:"id"`
}

// DeleteGroupModel soft-deletes one user_model_provider_group_models row under the given group.
func DeleteGroupModel(w http.ResponseWriter, r *http.Request) {
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

	parentID := strings.TrimSpace(mux.Vars(r)["model_provider_id"])
	groupID := strings.TrimSpace(mux.Vars(r)["group_id"])
	modelID := strings.TrimSpace(mux.Vars(r)["model_id"])
	if parentID == "" || groupID == "" || modelID == "" {
		common.ReplyErr(w, "missing model_provider_id, group_id, or model_id", http.StatusBadRequest)
		return
	}

	var parent orm.UserModelProvider
	err := db.WithContext(r.Context()).
		Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", parentID, userID).
		Take(&parent).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "model provider not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query model provider failed", http.StatusInternalServerError)
		return
	}

	if !parent.HasCapability("has_models") {
		common.ReplyErr(w, "this provider does not support models", http.StatusBadRequest)
		return
	}

	var group orm.UserModelProviderGroup
	err = db.WithContext(r.Context()).
		Where("id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", groupID, parent.ID, userID).
		Take(&group).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "group not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query group failed", http.StatusInternalServerError)
		return
	}

	var row orm.UserModelProviderGroupModel
	err = db.WithContext(r.Context()).
		Where(
			"id = ? AND user_model_provider_group_id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL",
			modelID, group.ID, parent.ID, userID,
		).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "model not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query model failed", http.StatusInternalServerError)
		return
	}

	clearMultimodalSelection := isMultimodalEmbeddingModelType(row.ModelType)
	now := time.Now().UTC()
	if err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&orm.UserModelProviderGroupModel{}).
			Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", row.ID, userID).
			Updates(map[string]interface{}{
				"deleted_at": now,
				"updated_at": now,
			}).Error; err != nil {
			return err
		}
		// Drop any default-model rows pointing at this model (avoids stale share=true).
		if err := tx.Where("user_model_provider_group_model_id = ?", row.ID).
			Delete(&orm.UserSelectedModel{}).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		common.ReplyErr(w, "delete model failed", http.StatusInternalServerError)
		return
	}

	if clearMultimodalSelection {
		maybeScheduleImageGroupLazyReset(r.Context(), db)
	}

	common.ReplyOK(w, deleteGroupModelResponse{ID: modelID})
}
