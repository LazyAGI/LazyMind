package modelprovider

import (
	"context"
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

type createGroupRequest struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
}

type createGroupResponse struct {
	ID                  string `json:"id"`
	UserModelProviderID string `json:"user_model_provider_id"`
	Name                string `json:"name"`
	BaseURL             string `json:"base_url"`
}

type groupListItem struct {
	ID                  string `json:"id"`
	UserModelProviderID string `json:"user_model_provider_id"`
	Name                string `json:"name"`
	BaseURL             string `json:"base_url"`
	APIKey              string `json:"api_key"`
	IsVerified          bool   `json:"is_verified"`
}

type groupListResponse struct {
	Groups []groupListItem `json:"groups"`
}

// ListGroups returns active connection groups for the given user model provider (path model_provider_id).
func ListGroups(w http.ResponseWriter, r *http.Request) {
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
	if parentID == "" {
		common.ReplyErr(w, "missing model_provider_id", http.StatusBadRequest)
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

	var rows []orm.UserModelProviderGroup
	if err := db.WithContext(r.Context()).
		Where("user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", parent.ID, userID).
		Order("name ASC").
		Find(&rows).Error; err != nil {
		common.ReplyErr(w, "list groups failed", http.StatusInternalServerError)
		return
	}

	out := make([]groupListItem, 0, len(rows))
	for i := range rows {
		g := rows[i]
		out = append(out, groupListItem{
			ID:                  g.ID,
			UserModelProviderID: g.UserModelProviderID,
			Name:                g.Name,
			BaseURL:             g.BaseURL,
			APIKey:              g.APIKey,
			IsVerified:          g.IsVerified,
		})
	}
	common.ReplyOK(w, groupListResponse{Groups: out})
}

type updateGroupRequest struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key,omitempty"`
}

// CreateGroup creates a connection group under the user's model provider (path model_provider_id = user_model_providers.id).
func CreateGroup(w http.ResponseWriter, r *http.Request) {
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
	if parentID == "" {
		common.ReplyErr(w, "missing model_provider_id", http.StatusBadRequest)
		return
	}

	var req createGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	baseURL := strings.TrimSpace(req.BaseURL)
	apiKey := strings.TrimSpace(req.APIKey)
	if name == "" || baseURL == "" {
		common.ReplyErr(w, "name and base_url are required", http.StatusBadRequest)
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

	now := time.Now()
	row := orm.UserModelProviderGroup{
		ID:                  common.GenerateID(),
		UserModelProviderID: parent.ID,
		Name:                name,
		BaseURL:             baseURL,
		APIKey:              apiKey,
		IsVerified:          false,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userName,
			CreatedAt:      now,
			UpdatedAt:      now,
			DeletedAt:      nil,
		},
	}
	err = db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return seedGroupModelsFromDefaults(tx, r.Context(), &row, &parent, baseURL, userID, userName, now)
	})
	if err != nil {
		common.ReplyErr(w, "create group failed", http.StatusInternalServerError)
		return
	}

	common.ReplyOK(w, createGroupResponse{
		ID:                  row.ID,
		UserModelProviderID: row.UserModelProviderID,
		Name:                row.Name,
		BaseURL:             row.BaseURL,
	})
}

// UpdateGroup updates a connection group (name, base_url, optional api_key). The target group is path group_id.
// Empty api_key in the body leaves the stored key unchanged.
func UpdateGroup(w http.ResponseWriter, r *http.Request) {
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

	var req updateGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	baseURL := strings.TrimSpace(req.BaseURL)
	apiKey := strings.TrimSpace(req.APIKey)
	if name == "" || baseURL == "" {
		common.ReplyErr(w, "name and base_url are required", http.StatusBadRequest)
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

	var row orm.UserModelProviderGroup
	err = db.WithContext(r.Context()).
		Where("id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", groupID, parent.ID, userID).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "group not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query group failed", http.StatusInternalServerError)
		return
	}

	now := time.Now()
	updates := map[string]interface{}{
		"name":       name,
		"base_url":   baseURL,
		"updated_at": now,
	}
	if baseURL != row.BaseURL {
		updates["is_verified"] = false
	}
	if apiKey != "" {
		updates["api_key"] = apiKey
		if apiKey != row.APIKey {
			updates["is_verified"] = false
		}
	}
	if err := db.WithContext(r.Context()).Model(&row).Updates(updates).Error; err != nil {
		common.ReplyErr(w, "update group failed", http.StatusInternalServerError)
		return
	}
	row.Name = name
	row.BaseURL = baseURL
	if apiKey != "" {
		row.APIKey = apiKey
	}

	common.ReplyOK(w, createGroupResponse{
		ID:                  row.ID,
		UserModelProviderID: row.UserModelProviderID,
		Name:                row.Name,
		BaseURL:             row.BaseURL,
	})
}

type deleteGroupResponse struct {
	ID string `json:"id"`
}

// DeleteGroup soft-deletes a connection group and its user_model_provider_group_models rows.
func DeleteGroup(w http.ResponseWriter, r *http.Request) {
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

	var row orm.UserModelProviderGroup
	err = db.WithContext(r.Context()).
		Where("id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", groupID, parent.ID, userID).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "group not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query group failed", http.StatusInternalServerError)
		return
	}

	// Fetch models before deletion to check for embed_image types and
	// collect IDs for user_selected_models cleanup.
	var groupModels []orm.UserModelProviderGroupModel
	if err := db.WithContext(r.Context()).
		Where("user_model_provider_group_id = ? AND create_user_id = ? AND deleted_at IS NULL", groupID, userID).
		Find(&groupModels).Error; err != nil {
		common.ReplyErr(w, "query group models failed", http.StatusInternalServerError)
		return
	}

	hasMultimodal := false
	modelIDs := make([]string, 0, len(groupModels))
	for i := range groupModels {
		modelIDs = append(modelIDs, groupModels[i].ID)
		if isMultimodalEmbeddingModelType(groupModels[i].ModelType) {
			hasMultimodal = true
		}
	}

	now := time.Now().UTC()
	err = db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if len(modelIDs) > 0 {
			if err := tx.Where("user_model_provider_group_model_id IN ?", modelIDs).
				Delete(&orm.UserSelectedModel{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&orm.UserModelProviderGroupModel{}).
			Where(
				"user_model_provider_group_id = ? AND create_user_id = ? AND deleted_at IS NULL",
				groupID, userID,
			).
			Updates(map[string]interface{}{
				"deleted_at": now,
				"updated_at": now,
			}).Error; err != nil {
			return err
		}
		return tx.Model(&orm.UserModelProviderGroup{}).
			Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", row.ID, userID).
			Updates(map[string]interface{}{
				"deleted_at": now,
				"updated_at": now,
			}).Error
	})
	if err != nil {
		common.ReplyErr(w, "delete group failed", http.StatusInternalServerError)
		return
	}

	if hasMultimodal {
		maybeScheduleImageGroupLazyReset(r.Context(), db)
	}

	common.ReplyOK(w, deleteGroupResponse{ID: groupID})
}

func normalizeBaseURLForCompare(s string) string {
	s = strings.TrimSpace(s)
	for strings.HasSuffix(s, "/") {
		s = strings.TrimSuffix(s, "/")
	}
	return s
}

// seedGroupModelsFromDefaults inserts user_model_provider_group_models from default_models when the group's
// base_url matches the catalog DefaultModelProvider.base_url for parent.DefaultModelProviderID.
func seedGroupModelsFromDefaults(
	tx *gorm.DB,
	ctx context.Context,
	group *orm.UserModelProviderGroup,
	parent *orm.UserModelProvider,
	requestBaseURL, userID, userName string,
	now time.Time,
) error {
	var catalog orm.DefaultModelProvider
	err := tx.WithContext(ctx).
		Where("id = ? AND deleted_at IS NULL", parent.DefaultModelProviderID).
		Take(&catalog).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if normalizeBaseURLForCompare(requestBaseURL) != normalizeBaseURLForCompare(catalog.BaseURL) {
		return nil
	}
	var defs []orm.DefaultModel
	if err := tx.WithContext(ctx).
		Where("default_model_provider_id = ? AND deleted_at IS NULL", parent.DefaultModelProviderID).
		Find(&defs).Error; err != nil {
		return err
	}
	if len(defs) == 0 {
		return nil
	}
	batch := make([]orm.UserModelProviderGroupModel, len(defs))
	for i, d := range defs {
		batch[i] = orm.UserModelProviderGroupModel{
			ID:                       common.GenerateID(),
			UserModelProviderID:      parent.ID,
			UserModelProviderGroupID: group.ID,
			ProviderName:             d.ProviderName,
			Name:                     d.Name,
			ModelType:                d.ModelType,
			IsDefault:                true,
			BaseModel: orm.BaseModel{
				CreateUserID:   userID,
				CreateUserName: userName,
				CreatedAt:      now,
				UpdatedAt:      now,
				DeletedAt:      nil,
			},
		}
	}
	return tx.WithContext(ctx).CreateInBatches(&batch, 100).Error
}
