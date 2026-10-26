package modelprovider

import (
	"errors"
	"net/http"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
)

func prepareGroupModelAddition(req addGroupModelRequest) (orm.UserModelProviderGroupModel, error) {
	row := orm.UserModelProviderGroupModel{Name: strings.TrimSpace(req.Name), ModelType: normalizeGroupModelType(req.ModelType)}
	if row.Name == "" {
		return row, errors.New("name is required")
	}
	if err := validateModelName(row.Name); err != nil {
		return row, err
	}
	var err error
	row.MaxInputTokens, err = resolveAddModelMaxInputTokens(row.ModelType, row.Name, req.MaxInputTokens)
	if err != nil {
		return row, err
	}
	row.InputTokenLimit, row.MaxOutputTokens, err = resolveModelTokenLimits(row.ModelType, row.MaxInputTokens, req.InputTokenLimit, req.MaxOutputTokens)
	row.Vision = req.Vision && isChatTechnicalType(row.ModelType)
	if err == nil {
		err = normalizeSavedModelSettings(&row, req.ModelSettings)
	}
	return row, err
}

func loadModelWriteGroup(w http.ResponseWriter, r *http.Request, db *gorm.DB, userID, parentID, groupID string) (orm.UserModelProvider, orm.UserModelProviderGroup, bool) {
	var parent orm.UserModelProvider
	var group orm.UserModelProviderGroup
	if err := db.WithContext(r.Context()).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", parentID, userID).Take(&parent).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "model provider not found", http.StatusNotFound)
		} else {
			common.ReplyErr(w, "query model provider failed", http.StatusInternalServerError)
		}
		return parent, group, false
	}
	if !parent.HasCapability("has_models") {
		common.ReplyErr(w, "this provider does not support models", http.StatusBadRequest)
		return parent, group, false
	}
	if err := db.WithContext(r.Context()).Where("id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", groupID, parent.ID, userID).Take(&group).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "group not found", http.StatusNotFound)
		} else {
			common.ReplyErr(w, "query group failed", http.StatusInternalServerError)
		}
		return parent, group, false
	}
	return parent, group, true
}

func groupModelAdditionResponse(row orm.UserModelProviderGroupModel, group orm.UserModelProviderGroup) addGroupModelResponse {
	return addGroupModelResponse{
		ID: row.ID, UserModelProviderID: row.UserModelProviderID, UserModelProviderGroupID: row.UserModelProviderGroupID,
		Name: row.Name, ModelType: row.ModelType, Vision: row.Vision, ModelSettings: modelSettingsResponse(row.ModelSettings), ProviderName: row.ProviderName, GroupName: group.Name,
		BaseURL: group.BaseURL, IsDefault: row.IsDefault, Enabled: row.Enabled, MaxInputTokens: row.MaxInputTokens,
		InputTokenLimit: row.InputTokenLimit, MaxOutputTokens: row.MaxOutputTokens,
	}
}
