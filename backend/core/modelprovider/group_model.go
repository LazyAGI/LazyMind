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
	Vision          bool    `json:"vision"`
	Name            string  `json:"name"`
	ModelType       string  `json:"model_type"`
	MaxInputTokens  *string `json:"max_input_tokens"`
	InputTokenLimit *string `json:"input_token_limit"`
	MaxOutputTokens *string `json:"max_output_tokens"`
}

type addGroupModelResponse struct {
	Vision                   bool    `json:"vision"`
	ID                       string  `json:"id"`
	UserModelProviderID      string  `json:"user_model_provider_id"`
	UserModelProviderGroupID string  `json:"user_model_provider_group_id"`
	Name                     string  `json:"name"`
	ModelType                string  `json:"model_type"`
	ProviderName             string  `json:"provider_name"`
	GroupName                string  `json:"group_name"`
	BaseURL                  string  `json:"base_url"`
	IsDefault                bool    `json:"is_default"`
	MaxInputTokens           *string `json:"max_input_tokens,omitempty"`
	InputTokenLimit          *string `json:"input_token_limit"`
	MaxOutputTokens          *string `json:"max_output_tokens"`
}

type updateGroupModelRequest struct {
	Name            *string `json:"name"`
	ModelType       *string `json:"model_type"`
	Vision          *bool   `json:"vision"`
	MaxInputTokens  *string `json:"max_input_tokens"`
	InputTokenLimit *string `json:"input_token_limit"`
	MaxOutputTokens *string `json:"max_output_tokens"`
	Customize       bool    `json:"customize"`
}

type groupModelListItem struct {
	Vision                   bool     `json:"vision"`
	ID                       string   `json:"id"`
	Source                   string   `json:"source"`
	ProviderID               string   `json:"provider_id"`
	ProviderGroupID          string   `json:"provider_group_id,omitempty"`
	UserModelProviderID      string   `json:"user_model_provider_id,omitempty"`
	UserModelProviderGroupID string   `json:"user_model_provider_group_id,omitempty"`
	Name                     string   `json:"name"`
	ModelType                string   `json:"model_type"`
	ProviderName             string   `json:"provider_name"`
	GroupName                string   `json:"group_name,omitempty"`
	BaseURL                  string   `json:"base_url,omitempty"`
	IsDefault                bool     `json:"is_default"`
	IsEditable               bool     `json:"is_editable"`
	MaxInputTokens           *string  `json:"max_input_tokens"`
	InputTokenLimit          *string  `json:"input_token_limit"`
	MaxOutputTokens          *string  `json:"max_output_tokens"`
	Availability             string   `json:"availability"`
	Lifecycle                string   `json:"lifecycle"`
	ReadOnly                 bool     `json:"read_only"`
	Capabilities             []string `json:"capabilities"`
}

type groupModelListResponse struct {
	Models []groupModelListItem `json:"models"`
}

func compatibleDBModelTypes(modelType string) []string {
	if modelType == EvoModelKey {
		return []string{"llm", "vlm"}
	}
	switch modelType {
	case "embed", "embedding", "embed_main":
		return []string{"embed", "embedding", "embed_main"}
	case "cross_modal_embed", "multimodal_embedding", "embed_image":
		return []string{"cross_modal_embed", "multimodal_embedding", "embed_image"}
	case "vlm", "VLM":
		return []string{"vlm", "VLM"}
	}
	return []string{modelType}
}

func normalizeGroupModelType(modelType string) string {
	switch strings.ToLower(strings.TrimSpace(modelType)) {
	case "embedding", "embed_main":
		return "embed"
	case "vlm":
		return "vlm"
	default:
		return strings.ToLower(strings.TrimSpace(modelType))
	}
}

// AddGroupModel inserts a user-defined model row under a connection group (custom model name and model_type).
var errModelAlreadyExists = errors.New("model name already exists in this supplier")

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
	modelType := normalizeGroupModelType(strings.TrimSpace(req.ModelType))
	if name == "" {
		common.ReplyErr(w, "name is required", http.StatusBadRequest)
		return
	}
	if err := validateModelName(name); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	maxInputTokens, err := resolveAddModelMaxInputTokens(modelType, name, req.MaxInputTokens)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}

	inputTokenLimit, maxOutputTokens, err := resolveModelTokenLimits(modelType, maxInputTokens, req.InputTokenLimit, req.MaxOutputTokens)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}

	var parent orm.UserModelProvider
	err = db.WithContext(r.Context()).
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
	err = db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := lockProvider(tx, &parent); err != nil {
			return errConnectionChanged
		}
		if err := tx.Where("id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", group.ID, parent.ID, userID).Take(&group).Error; err != nil {
			return errConnectionChanged
		}
		if err := validateSupplierModelType(parent, modelType); err != nil {
			return err
		}
		err := tx.Where("user_model_provider_group_id = ? AND create_user_id = ? AND name = ?", group.ID, userID, name).Take(&row).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && row.DeletedAt == nil {
			return errModelAlreadyExists
		}
		now := time.Now()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			row = orm.UserModelProviderGroupModel{
				ID: common.GenerateID(), UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID,
				ProviderName: RuntimeProviderName(parent), Name: name, ModelType: modelType, Vision: req.Vision && isChatTechnicalType(modelType),
				MaxInputTokens: maxInputTokens, InputTokenLimit: inputTokenLimit, MaxOutputTokens: maxOutputTokens, IsDefault: false,
				BaseModel: orm.BaseModel{CreateUserID: userID, CreateUserName: userName, CreatedAt: now, UpdatedAt: now},
			}
			return tx.Create(&row).Error
		}
		result := tx.Model(&orm.UserModelProviderGroupModel{}).Where("id = ? AND create_user_id = ? AND deleted_at IS NOT NULL", row.ID, userID).Updates(map[string]interface{}{
			"user_model_provider_id": parent.ID, "provider_name": RuntimeProviderName(parent), "model_type": modelType,
			"vision": req.Vision && isChatTechnicalType(modelType), "max_input_tokens": maxInputTokens, "input_token_limit": inputTokenLimit, "max_output_tokens": maxOutputTokens, "is_default": false, "updated_at": now, "deleted_at": nil,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errModelAlreadyExists
		}
		row.UserModelProviderID, row.ProviderName, row.ModelType = parent.ID, RuntimeProviderName(parent), modelType
		row.Vision, row.MaxInputTokens, row.IsDefault, row.UpdatedAt, row.DeletedAt = req.Vision && isChatTechnicalType(modelType), maxInputTokens, false, now, nil
		row.InputTokenLimit, row.MaxOutputTokens = inputTokenLimit, maxOutputTokens
		return nil
	})
	if err != nil {
		if errors.Is(err, errUnsupportedModelType) || errors.Is(err, errSupplierModelType) {
			common.ReplyErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		if errors.Is(err, errConnectionChanged) || errors.Is(err, errModelAlreadyExists) {
			common.ReplyErr(w, err.Error(), http.StatusConflict)
			return
		}
		common.ReplyErr(w, "save model failed", http.StatusInternalServerError)
		return
	}

	common.ReplyOK(w, addGroupModelResponse{
		ID:                       row.ID,
		UserModelProviderID:      row.UserModelProviderID,
		UserModelProviderGroupID: row.UserModelProviderGroupID,
		Name:                     row.Name,
		ModelType:                row.ModelType,
		Vision:                   row.Vision,
		ProviderName:             row.ProviderName,
		GroupName:                group.Name,
		BaseURL:                  group.BaseURL,
		IsDefault:                row.IsDefault,
		MaxInputTokens:           row.MaxInputTokens,
		InputTokenLimit:          row.InputTokenLimit, MaxOutputTokens: row.MaxOutputTokens,
	})
}

// UpdateGroupModel patches model metadata while keeping its referenced UUID.
func UpdateGroupModel(w http.ResponseWriter, r *http.Request) {
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

	var req updateGroupModelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
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

	if req.Name == nil && req.ModelType == nil && req.Vision == nil && req.MaxInputTokens == nil && req.InputTokenLimit == nil && req.MaxOutputTokens == nil && !req.Customize {
		common.ReplyErr(w, "max_input_tokens is required", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	err = db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := lockProvider(tx, &parent); err != nil {
			return errConnectionChanged
		}
		if err := tx.Where("id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", group.ID, parent.ID, userID).Take(&group).Error; err != nil {
			return errConnectionChanged
		}
		if err := tx.Where("id = ? AND user_model_provider_group_id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", row.ID, group.ID, parent.ID, userID).Take(&row).Error; err != nil {
			return errConnectionChanged
		}
		before := row
		next := row
		if req.Name != nil {
			next.Name = strings.TrimSpace(*req.Name)
		}
		if req.ModelType != nil {
			next.ModelType = normalizeGroupModelType(*req.ModelType)
		}
		if err := validateModelName(next.Name); err != nil {
			return err
		}
		if err := validateSupplierModelType(parent, next.ModelType); err != nil {
			return err
		}
		if req.Vision != nil {
			next.Vision = *req.Vision
		}
		if !isChatTechnicalType(next.ModelType) {
			next.Vision = false
		}
		if next.ModelType == "" {
			if req.MaxInputTokens != nil {
				value, err := resolveAddModelMaxInputTokens("", next.Name, req.MaxInputTokens)
				if err != nil {
					return modelMetadataValidationError{err}
				}
				next.MaxInputTokens = value
			}
		} else if supportsUserMaxInputTokens(next.ModelType) {
			if req.MaxInputTokens != nil {
				value, err := resolveRequiredUserMaxInputTokens(next.ModelType, req.MaxInputTokens)
				if err != nil {
					return modelMetadataValidationError{err}
				}
				next.MaxInputTokens = value
			} else if (next.MaxInputTokens == nil || strings.TrimSpace(*next.MaxInputTokens) == "") && (next.Name != before.Name || equivalentTechnicalType(next.ModelType) != equivalentTechnicalType(before.ModelType)) {
				value, err := resolveAddModelMaxInputTokens(next.ModelType, next.Name, nil)
				if err != nil {
					return err
				}
				next.MaxInputTokens = value
			}
		} else {
			if req.MaxInputTokens != nil && strings.TrimSpace(*req.MaxInputTokens) != "" {
				return modelMetadataValidationError{errors.New("model max_input_tokens is only supported for llm or vlm models")}
			}
			if next.Name != before.Name || equivalentTechnicalType(next.ModelType) != equivalentTechnicalType(before.ModelType) {
				value, err := resolveSeededMaxInputTokens(next.ModelType, next.Name)
				if err != nil {
					return err
				}
				next.MaxInputTokens = value
			}
		}
		if req.InputTokenLimit != nil {
			next.InputTokenLimit = req.InputTokenLimit
		}
		if req.MaxOutputTokens != nil {
			next.MaxOutputTokens = req.MaxOutputTokens
		}
		if !supportsUserMaxInputTokens(next.ModelType) {
			if nonemptyTokenLimit(req.InputTokenLimit) || nonemptyTokenLimit(req.MaxOutputTokens) {
				return modelMetadataValidationError{errors.New("model token limits are only supported for llm or vlm models")}
			}
			next.InputTokenLimit, next.MaxOutputTokens = nil, nil
		}
		inputLimit, outputLimit, err := resolveModelTokenLimits(next.ModelType, next.MaxInputTokens, next.InputTokenLimit, next.MaxOutputTokens)
		if err != nil {
			return modelMetadataValidationError{err}
		}
		next.InputTokenLimit, next.MaxOutputTokens = inputLimit, outputLimit
		beforeTokens, nextTokens := "", ""
		if before.MaxInputTokens != nil {
			beforeTokens = *before.MaxInputTokens
		}
		if next.MaxInputTokens != nil {
			nextTokens = *next.MaxInputTokens
		}
		changed := next.Name != before.Name || next.ModelType != before.ModelType || next.Vision != before.Vision || nextTokens != beforeTokens || tokenLimitString(next.InputTokenLimit) != tokenLimitString(before.InputTokenLimit) || tokenLimitString(next.MaxOutputTokens) != tokenLimitString(before.MaxOutputTokens)
		if !changed {
			return nil
		}
		if row.IsDefault && !req.Customize {
			return modelMetadataValidationError{errors.New("catalog model max_input_tokens cannot be updated")}
		}
		if req.Customize {
			next.IsDefault = false
		}
		if err := validateModelEditReferences(tx, before, next, connectionBaseURL(parent, group.BaseURL)); err != nil {
			return err
		}
		if err := ensureEditedModelNameAvailable(tx, before, next.Name); err != nil {
			return err
		}
		updates := map[string]any{"name": next.Name, "model_type": next.ModelType, "vision": next.Vision, "max_input_tokens": next.MaxInputTokens, "input_token_limit": next.InputTokenLimit, "max_output_tokens": next.MaxOutputTokens, "is_default": next.IsDefault, "updated_at": now}
		if before.IsDefault && !next.IsDefault {
			updates["free_auto_select_priority"] = 0
			updates["free_auto_select_base_urls"] = ""
		}
		result := tx.Model(&orm.UserModelProviderGroupModel{}).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", before.ID, userID).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errConnectionChanged
		}
		if err := retainRenamedCatalogTombstone(tx, parent, before, next, now); err != nil {
			return err
		}
		next.UpdatedAt = now
		row = next
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errModelAlreadyExists), errors.Is(err, errConnectionChanged), errors.Is(err, errModelDefaultReference), errors.Is(err, errModelConversationReference), errors.Is(err, errEmbeddingIdentityInUse):
			common.ReplyErr(w, err.Error(), http.StatusConflict)
		case errors.Is(err, errUnsupportedModelType), errors.Is(err, errSupplierModelType), errors.Is(err, errInvalidModelName):
			common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		default:
			var invalid modelMetadataValidationError
			if errors.As(err, &invalid) {
				common.ReplyErr(w, err.Error(), http.StatusBadRequest)
			} else {
				common.ReplyErr(w, "update model failed", http.StatusInternalServerError)
			}
		}
		return
	}
	availability := "available"
	if !RuntimeSupportsModelType(RuntimeProviderName(parent), row.ModelType) {
		availability = "unavailable"
	}
	common.ReplyOK(w, groupModelListItem{
		ID: row.ID, Source: "own", ProviderID: row.UserModelProviderID, ProviderGroupID: row.UserModelProviderGroupID,
		UserModelProviderID: row.UserModelProviderID, UserModelProviderGroupID: row.UserModelProviderGroupID,
		Name: row.Name, ModelType: row.ModelType, Vision: row.Vision, ProviderName: row.ProviderName, GroupName: group.Name,
		BaseURL: connectionBaseURL(parent, group.BaseURL), IsDefault: row.IsDefault, IsEditable: strings.EqualFold(row.ModelType, "image_editing"), MaxInputTokens: row.MaxInputTokens, InputTokenLimit: row.InputTokenLimit, MaxOutputTokens: row.MaxOutputTokens, Availability: availability, Lifecycle: "active", Capabilities: []string{},
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
		availability := "available"
		if !RuntimeSupportsModelType(RuntimeProviderName(parent), m.ModelType) {
			availability = "unavailable"
		}
		out = append(out, groupModelListItem{
			ID:                       m.ID,
			Source:                   "own",
			ProviderID:               m.UserModelProviderID,
			ProviderGroupID:          m.UserModelProviderGroupID,
			UserModelProviderID:      m.UserModelProviderID,
			UserModelProviderGroupID: m.UserModelProviderGroupID,
			Name:                     m.Name,
			ModelType:                m.ModelType,
			Vision:                   m.Vision,
			ProviderName:             m.ProviderName,
			GroupName:                group.Name,
			BaseURL:                  group.BaseURL,
			IsDefault:                m.IsDefault,
			IsEditable:               strings.EqualFold(strings.TrimSpace(m.ModelType), "image_editing"),
			MaxInputTokens:           m.MaxInputTokens,
			InputTokenLimit:          m.InputTokenLimit, MaxOutputTokens: m.MaxOutputTokens,
			Availability: availability,
			Lifecycle:    "active",
			Capabilities: []string{},
		})
	}
	common.ReplyOK(w, groupModelListResponse{Models: out})
}

// ListUserModelsByModelType lists the current user's models across all user_model_providers.
// When model_type is provided, results are filtered to that type. Response shape matches ListGroupModels.
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
	keyword := strings.TrimSpace(r.URL.Query().Get("keyword"))
	q := db.WithContext(r.Context()).
		Joins("JOIN user_model_providers ON user_model_providers.id = user_model_provider_group_models.user_model_provider_id AND user_model_providers.create_user_id = user_model_provider_group_models.create_user_id AND user_model_providers.deleted_at IS NULL AND user_model_providers.capabilities LIKE '%has_models%'").
		Joins("JOIN user_model_provider_groups ON user_model_provider_groups.id = user_model_provider_group_models.user_model_provider_group_id AND user_model_provider_groups.user_model_provider_id = user_model_provider_group_models.user_model_provider_id AND user_model_provider_groups.create_user_id = user_model_provider_group_models.create_user_id AND user_model_provider_groups.deleted_at IS NULL").
		Where("user_model_provider_group_models.create_user_id = ? AND user_model_provider_group_models.deleted_at IS NULL", userID)
	if modelType != "" {
		// Translate runtime_models.yaml role key (e.g. "evo_llm") to the lazyllm
		// technical type (e.g. "llm") stored in user_model_provider_group_models.
		dbModelType := modelType
		if modelType != EvoModelKey {
			dbModelType = resolveModelType(r.Context(), modelType)
		}
		if strings.EqualFold(dbModelType, "vlm") {
			q = q.Where("(user_model_provider_group_models.model_type IN ? OR (user_model_provider_group_models.model_type = ? AND user_model_provider_group_models.vision = ?))", compatibleDBModelTypes(dbModelType), "llm", true)
		} else {
			q = q.Where("user_model_provider_group_models.model_type IN ?", compatibleDBModelTypes(dbModelType))
		}
	}
	if keyword != "" {
		like := "%" + keyword + "%"
		q = q.Where(
			"user_model_provider_group_models.name LIKE ? OR user_model_provider_group_models.provider_name LIKE ? OR user_model_provider_groups.name LIKE ?",
			like,
			like,
			like,
		)
	}

	var rows []orm.UserModelProviderGroupModel
	if err := q.Order("user_model_provider_group_models.user_model_provider_id ASC, user_model_provider_group_models.user_model_provider_group_id ASC, user_model_provider_group_models.name ASC").
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
		name      string
		baseURL   string
		available bool
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
			baseURL, _, err := ResolveProviderRuntimeCredential(r.Context(), db, grps[i].ID)
			groupByID[grps[i].ID] = groupInfo{name: grps[i].Name, baseURL: baseURL, available: err == nil}
		}
	}

	out := make([]groupModelListItem, 0, len(rows))
	for i := range rows {
		m := rows[i]
		grp, ok := groupByID[m.UserModelProviderGroupID]
		if !ok || !grp.available {
			continue
		}
		if !RuntimeSupportsModelType(m.ProviderName, m.ModelType) {
			continue
		}
		if modelType == EvoModelKey {
			if _, eligible := ResolveOpenCodeModel(m.ProviderName, m.Name, grp.baseURL, m.ModelType, m.IsDefault); !eligible {
				continue
			}
		}
		out = append(out, groupModelListItem{
			ID:                       m.ID,
			Source:                   "own",
			ProviderID:               m.UserModelProviderID,
			ProviderGroupID:          m.UserModelProviderGroupID,
			UserModelProviderID:      m.UserModelProviderID,
			UserModelProviderGroupID: m.UserModelProviderGroupID,
			Name:                     m.Name,
			ModelType:                m.ModelType,
			Vision:                   m.Vision,
			ProviderName:             m.ProviderName,
			GroupName:                grp.name,
			BaseURL:                  grp.baseURL,
			IsDefault:                m.IsDefault,
			IsEditable:               strings.EqualFold(m.ModelType, "image_editing"),
			MaxInputTokens:           m.MaxInputTokens,
			InputTokenLimit:          m.InputTokenLimit, MaxOutputTokens: m.MaxOutputTokens,
			Availability: "available",
			Lifecycle:    "active",
			Capabilities: []string{},
		})
	}
	if catalog, catalogErr := ResolveCloudModelCatalog(r.Context()); catalogErr == nil && catalog.Known {
		queryType := strings.ToLower(modelType)
		queryKeyword := strings.ToLower(keyword)
		for _, model := range catalog.Models {
			if queryType != "" && model.ModelType != queryType {
				continue
			}
			if queryKeyword != "" && !strings.Contains(strings.ToLower(model.DisplayName+" "+model.ModelKey+" "+catalog.ProviderName), queryKeyword) {
				continue
			}
			out = append(out, groupModelListItem{
				ID: model.ModelKey, Source: "cloud", ProviderID: catalog.ProviderID,
				Name: model.DisplayName, ModelType: model.ModelType, ProviderName: catalog.ProviderName,
				IsDefault: model.DefaultForType, IsEditable: model.ModelType == "image_editing",
				Availability: model.Status, Lifecycle: model.Lifecycle, ReadOnly: true,
				Capabilities: append([]string(nil), model.Capabilities...),
			})
		}
	}
	common.ReplyOK(w, groupModelListResponse{Models: out})
}

type deleteGroupModelResponse struct {
	ID                       string `json:"id"`
	DowngradedKnowledgeBases int64  `json:"downgraded_knowledge_bases"`
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

	var clearMultimodalSelection bool
	confirmationRequired := errors.New("removing an embedding model requires confirmation to downgrade all indexed knowledge bases to chunked")
	now := time.Now().UTC()
	var downgradedKnowledgeBases int64
	if err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		// Model editing and durable reference changes serialize on the supplier.
		// Re-read all configuration before choosing destructive type-specific work.
		if err := lockProvider(tx, &parent); err != nil {
			return errConnectionChanged
		}
		if !parent.HasCapability("has_models") {
			return errConnectionChanged
		}
		if err := tx.Where("id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", group.ID, parent.ID, userID).Take(&group).Error; err != nil {
			return errConnectionChanged
		}
		if err := tx.Where("id = ? AND user_model_provider_group_id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", row.ID, group.ID, parent.ID, userID).Take(&row).Error; err != nil {
			return errConnectionChanged
		}
		clearMultimodalSelection = isMultimodalEmbeddingModelType(row.ModelType)
		removeTextEmbedding := isTextEmbeddingModelType(row.ModelType)
		if removeTextEmbedding && !indexedDowngradeConfirmed(r) {
			return confirmationRequired
		}
		if removeTextEmbedding {
			count, err := downgradeIndexedDatasets(tx, userID, now)
			if err != nil {
				return err
			}
			downgradedKnowledgeBases = count
		}
		deleted := tx.Model(&orm.UserModelProviderGroupModel{}).
			Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", row.ID, userID).
			Updates(map[string]interface{}{
				"deleted_at": now,
				"updated_at": now,
			})
		if deleted.Error != nil {
			return deleted.Error
		}
		if deleted.RowsAffected != 1 {
			return errConnectionChanged
		}
		// Drop any default-model rows pointing at this model (avoids stale share=true).
		if err := tx.Where("user_model_provider_group_model_id = ?", row.ID).
			Delete(&orm.UserSelectedModel{}).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		if errors.Is(err, confirmationRequired) || errors.Is(err, errConnectionChanged) {
			common.ReplyErr(w, err.Error(), http.StatusConflict)
		} else {
			common.ReplyErr(w, "delete model failed", http.StatusInternalServerError)
		}
		return
	}

	if clearMultimodalSelection {
		maybeScheduleImageGroupLazyReset(r.Context(), db)
	}

	common.ReplyOK(w, deleteGroupModelResponse{ID: modelID, DowngradedKnowledgeBases: downgradedKnowledgeBases})
}
