package modelprovider

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/credentialvault"
	"lazymind/core/store"
)

type createGroupRequest struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Verify  bool   `json:"verify"`
}

type createGroupResponse struct {
	ID                  string                  `json:"id"`
	UserModelProviderID string                  `json:"user_model_provider_id"`
	Name                string                  `json:"name"`
	BaseURL             string                  `json:"base_url"`
	IsVerified          bool                    `json:"is_verified"`
	Check               *CheckModelProviderData `json:"check,omitempty"`
	AutoSelection       *autoModelSelection     `json:"auto_selection,omitempty"`
}

type autoSelectedModel struct {
	ModelKey string `json:"model_key"`
	Name     string `json:"name"`
}

type autoModelSelection struct {
	ProviderName string              `json:"provider_name"`
	Configured   []autoSelectedModel `json:"configured"`
	Missing      []string            `json:"missing"`
}

type groupListItem struct {
	ID                  string         `json:"id"`
	UserModelProviderID string         `json:"user_model_provider_id"`
	Name                string         `json:"name"`
	BaseURL             string         `json:"base_url"`
	HasAPIKey           bool           `json:"has_api_key"`
	ModelCount          int            `json:"model_count"`
	Keys                []groupKeyItem `json:"keys"`
	IsVerified          bool           `json:"is_verified"`
}

type groupKeyItem struct {
	ID      string `json:"id"`
	Masked  string `json:"masked"`
	Enabled bool   `json:"enabled"`
}

func groupKeyID(groupID, key string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(groupID+"\x00"+key)))
}

func groupKeyItems(groupID, value string) []groupKeyItem {
	items := make([]groupKeyItem, 0)
	seen := make(map[string]bool)
	for _, key := range splitAPIKeys(value) {
		id := groupKeyID(groupID, key)
		if seen[id] {
			continue
		}
		seen[id] = true
		masked := "********"
		if len(key) > 8 {
			masked += "..." + key[len(key)-4:]
		}
		items = append(items, groupKeyItem{ID: id, Masked: masked})
	}
	return items
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

	if err := prepareProviderKeySelection(r.Context(), db, &parent); err != nil {
		common.ReplyErr(w, "initialize enabled api key failed", http.StatusInternalServerError)
		return
	}
	var rows []orm.UserModelProviderGroup
	if err := db.WithContext(r.Context()).
		Where("user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", parent.ID, userID).
		Order("created_at ASC, id ASC").
		Find(&rows).Error; err != nil {
		common.ReplyErr(w, "list groups failed", http.StatusInternalServerError)
		return
	}

	modelCounts := make(map[string]int, len(rows))
	// Some isolated credential tests intentionally omit the model table.
	// Production lists count metadata in one aggregate, without loading models.
	if len(rows) > 0 && db.Migrator().HasTable(&orm.UserModelProviderGroupModel{}) {
		groupIDs := make([]string, 0, len(rows))
		for _, group := range rows {
			groupIDs = append(groupIDs, group.ID)
		}
		var counts []struct {
			UserModelProviderGroupID string
			ModelCount               int
		}
		if err := db.WithContext(r.Context()).Model(&orm.UserModelProviderGroupModel{}).
			Select("user_model_provider_group_id, COUNT(*) AS model_count").
			Where("user_model_provider_id = ? AND create_user_id = ? AND user_model_provider_group_id IN ? AND deleted_at IS NULL", parent.ID, userID, groupIDs).
			Group("user_model_provider_group_id").Scan(&counts).Error; err != nil {
			common.ReplyErr(w, "list groups failed", http.StatusInternalServerError)
			return
		}
		for _, count := range counts {
			modelCounts[count.UserModelProviderGroupID] = count.ModelCount
		}
	}
	out := make([]groupListItem, 0, len(rows))
	for i := range rows {
		g := rows[i]
		apiKey, err := apiKeyForGroup(db.WithContext(r.Context()), &g)
		if err != nil {
			common.ReplyErr(w, "decrypt api key failed", http.StatusInternalServerError)
			return
		}
		keys := groupKeyItems(g.ID, apiKey)
		for k := range keys {
			keys[k].Enabled = parent.ActiveKeyGroupID == g.ID && parent.ActiveKeyID == keys[k].ID
		}
		out = append(out, groupListItem{
			ID:                  g.ID,
			UserModelProviderID: g.UserModelProviderID,
			Name:                g.Name,
			BaseURL:             connectionBaseURL(parent, g.BaseURL),
			HasAPIKey:           strings.TrimSpace(g.APIKey) != "" || strings.TrimSpace(g.APIKeyCiphertext) != "",
			ModelCount:          modelCounts[g.ID],
			Keys:                keys,
			IsVerified:          g.IsVerified,
		})
	}
	common.ReplyOK(w, groupListResponse{Groups: out})
}

type updateGroupRequest struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key,omitempty"`
	Verify  bool   `json:"verify"`
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
	if name == "" {
		common.ReplyErr(w, "name is required", http.StatusBadRequest)
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
	if err := prepareProviderKeySelection(r.Context(), db, &parent); err != nil {
		common.ReplyErr(w, "initialize enabled api key failed", http.StatusInternalServerError)
		return
	}
	if hasSharedProviderEndpoint(parent) {
		name = parent.Name
	}
	if hasOpenAIRequestPath(RuntimeProviderName(parent), baseURL) {
		common.ReplyErr(w, "base_url must be an API root without a request path", http.StatusBadRequest)
		return
	}
	baseURL = connectionBaseURL(parent, baseURL)
	if baseURL == "" {
		common.ReplyErr(w, "base_url is required", http.StatusBadRequest)
		return
	}
	if hasOpenAIRequestPath(RuntimeProviderName(parent), baseURL) {
		common.ReplyErr(w, "base_url must be an API root without a request path", http.StatusBadRequest)
		return
	}
	baseURL = LazyLLMBaseURL(RuntimeProviderName(parent), baseURL)

	// Capability: single-group providers only allow one group per user.
	if hasSharedProviderEndpoint(parent) || !parent.HasCapability("multi_group") {
		var count int64
		if err := db.WithContext(r.Context()).Model(&orm.UserModelProviderGroup{}).
			Where("user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", parent.ID, userID).
			Count(&count).Error; err != nil {
			common.ReplyErr(w, "check existing groups failed", http.StatusInternalServerError)
			return
		}
		if count > 0 {
			common.ReplyErr(w, "this provider only allows one group per user", http.StatusConflict)
			return
		}
	}

	if err := validateSavedKeyPool(parent, name, baseURL, apiKey); err != nil {
		common.ReplyErr(w, "api_key pool exceeds credential limits", http.StatusBadRequest)
		return
	}
	groupID := common.GenerateID()
	credentialRevision := int64(1)
	apiKeyCiphertext, err := encodeLegacyModelProviderCiphertext(apiKey)
	if err != nil {
		common.ReplyErr(w, "encrypt api key failed", http.StatusInternalServerError)
		return
	}

	var checkData *CheckModelProviderData
	if apiKey != "" && shouldVerifyCloudServiceOnSave(parent.Category, RuntimeProviderName(parent)) {
		checkResult, checkErr := doProviderGroupCheck(r.Context(), parent.Category, RuntimeProviderName(parent), baseURL, apiKey, "", parent)
		if checkErr != nil || checkResult == nil || !checkResult.Success {
			msg := "verification failed"
			checkMsg := msg
			if checkResult != nil && strings.TrimSpace(checkResult.Message) != "" {
				checkMsg = strings.TrimSpace(checkResult.Message)
				msg = msg + ": " + checkMsg
			}
			common.ReplyErrWithData(
				w,
				msg,
				CheckModelProviderData{Success: false, Message: checkMsg},
				http.StatusBadGateway,
			)
			return
		}
		checkData = &CheckModelProviderData{Success: true, Message: checkResult.Message}
	}
	if req.Verify && !hasSharedProviderEndpoint(parent) && checkData == nil && apiKey != "" {
		checkResult, checkErr := doProviderGroupCheck(r.Context(), parent.Category, RuntimeProviderName(parent), baseURL, apiKey, "", parent)
		if checkErr != nil || checkResult == nil || !checkResult.Success {
			msg := "verification failed"
			checkMsg := msg
			if checkResult != nil && strings.TrimSpace(checkResult.Message) != "" {
				checkMsg = strings.TrimSpace(checkResult.Message)
				msg = msg + ": " + checkMsg
			}
			common.ReplyErrWithData(
				w,
				msg,
				CheckModelProviderData{Success: false, Message: checkMsg},
				http.StatusBadGateway,
			)
			return
		}
		checkData = &CheckModelProviderData{Success: true, Message: checkResult.Message}
	}

	now := time.Now()
	row := orm.UserModelProviderGroup{
		ID:                  groupID,
		UserModelProviderID: parent.ID,
		Name:                name,
		BaseURL:             baseURL,
		APIKey:              "",
		APIKeyCiphertext:    apiKeyCiphertext,
		CredentialVersion:   legacyModelProviderCredentialVersion,
		CredentialRevision:  credentialRevision,
		IsVerified:          checkData != nil,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userName,
			CreatedAt:      now,
			UpdatedAt:      now,
			DeletedAt:      nil,
		},
	}
	var autoSelection *autoModelSelection
	err = db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if hasSharedProviderEndpoint(parent) {
			if err := lockProvider(tx, &parent); err != nil {
				return err
			}
			var existing int64
			if err := tx.Model(&orm.UserModelProviderGroup{}).Where("user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", parent.ID, userID).Count(&existing).Error; err != nil {
				return err
			}
			if existing > 0 {
				return errCanonicalGroupExists
			}
			if strings.TrimSpace(parent.BaseURL) == "" {
				if err := tx.Model(&parent).Update("base_url", baseURL).Error; err != nil {
					return err
				}
				parent.BaseURL = baseURL
			}
			latestURL := connectionBaseURL(parent, baseURL)
			if normalizeBaseURLForCompare(latestURL) != normalizeBaseURLForCompare(baseURL) {
				checkData = nil
				row.IsVerified = false
			}
			row.BaseURL, baseURL = latestURL, latestURL
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		seededModels, err := seedGroupModelsFromDefaults(tx, r.Context(), &row, &parent, baseURL, userID, userName, now)
		if err != nil {
			return err
		}
		if strings.EqualFold(strings.TrimSpace(parent.Category), defaultProviderCategory) {
			autoSelection, err = autoSelectUnconfiguredProviderModels(
				tx, userID, userName, RuntimeProviderName(parent), baseURL, seededModels, now,
			)
			if err != nil {
				return err
			}
		}
		if err := reconcileProviderActiveKey(tx, &parent); err != nil {
			return err
		}
		return enqueueCredentialBackup(tx, row.ID, row.CredentialRevision, credentialvault.BackupUpsert, now)
	})
	if err != nil {
		if errors.Is(err, errCanonicalGroupExists) {
			common.ReplyErr(w, err.Error(), http.StatusConflict)
			return
		}
		common.ReplyErr(w, "create group failed", http.StatusInternalServerError)
		return
	}
	if hasSharedProviderEndpoint(parent) {
		if err := db.WithContext(r.Context()).Where("id = ? AND deleted_at IS NULL", row.ID).Take(&row).Error; err != nil {
			common.ReplyErr(w, "reload group failed", http.StatusInternalServerError)
			return
		}
	}
	common.ReplyOK(w, createGroupResponse{
		ID:                  row.ID,
		UserModelProviderID: row.UserModelProviderID,
		Name:                row.Name,
		BaseURL:             row.BaseURL,
		IsVerified:          row.IsVerified,
		Check:               checkData,
		AutoSelection:       autoSelection,
	})
}

func autoSelectUnconfiguredProviderModels(
	tx *gorm.DB,
	userID, userName, providerName, baseURL string,
	models []orm.UserModelProviderGroupModel,
	now time.Time,
) (*autoModelSelection, error) {
	result := &autoModelSelection{
		ProviderName: providerName,
		Configured:   make([]autoSelectedModel, 0, len(autoModelSlots)),
		Missing:      make([]string, 0, len(autoModelSlots)),
	}

	for _, slot := range autoModelSlots {
		selectionKeys := []string{slot.ModelKey}
		if slot.ModelKey == "image_generator" {
			selectionKeys = []string{"text2image", "image_editing", "image_generator"}
		}
		var selected orm.UserSelectedModel
		err := tx.Where("user_id = ? AND model_type IN ?", userID, selectionKeys).Take(&selected).Error
		if err == nil {
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		model, ok := preferredAutoModel(
			baseURL, slot.CatalogTypes, models,
		)
		if !ok {
			result.Missing = append(result.Missing, slot.ModelKey)
			continue
		}
		selectionKey := slot.ModelKey
		if slot.ModelKey == "image_generator" {
			selectionKey = "text2image"
			if strings.EqualFold(strings.TrimSpace(model.ModelType), "image_editing") {
				selectionKey = "image_editing"
			}
		}
		selected = orm.UserSelectedModel{
			UserID:                        userID,
			UserName:                      userName,
			ModelKey:                      selectionKey,
			UserModelProviderGroupModelID: model.ID,
			CreatedAt:                     now,
			UpdatedAt:                     now,
		}
		if err := tx.Create(&selected).Error; err != nil {
			return nil, err
		}
		result.Configured = append(result.Configured, autoSelectedModel{ModelKey: slot.ModelKey, Name: model.Name})
	}
	return result, nil
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
	if hasSharedProviderEndpoint(parent) {
		name = parent.Name
	}
	if hasOpenAIRequestPath(RuntimeProviderName(parent), baseURL) {
		common.ReplyErr(w, "base_url must be an API root without a request path", http.StatusBadRequest)
		return
	}
	baseURL = connectionBaseURL(parent, baseURL)
	if baseURL == "" {
		common.ReplyErr(w, "base_url is required", http.StatusBadRequest)
		return
	}
	if hasOpenAIRequestPath(RuntimeProviderName(parent), baseURL) {
		common.ReplyErr(w, "base_url must be an API root without a request path", http.StatusBadRequest)
		return
	}
	baseURL = LazyLLMBaseURL(RuntimeProviderName(parent), baseURL)

	if err := prepareProviderKeySelection(r.Context(), db, &parent); err != nil {
		common.ReplyErr(w, "initialize enabled api key failed", http.StatusInternalServerError)
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
	storedAPIKey, err := apiKeyForGroup(db.WithContext(r.Context()), &row)
	if err != nil {
		common.ReplyErr(w, "decrypt api key failed", http.StatusInternalServerError)
		return
	}

	if name == "" {
		name = row.Name
	}

	savedPool := apiKey
	if savedPool == "" {
		savedPool = storedAPIKey
	}
	if err := validateSavedKeyPool(parent, name, baseURL, savedPool); err != nil {
		common.ReplyErr(w, "api_key pool exceeds credential limits", http.StatusBadRequest)
		return
	}
	now := time.Now()
	updates := map[string]interface{}{
		"name":       name,
		"base_url":   baseURL,
		"updated_at": now,
	}

	// Capability: providers without custom_base_url must keep the original base_url.
	if !hasSharedProviderEndpoint(parent) && !parent.HasCapability("custom_base_url") {
		baseURL = row.BaseURL
		updates["base_url"] = row.BaseURL
	}

	baseURLChanged := normalizeBaseURLForCompare(baseURL) != normalizeBaseURLForCompare(row.BaseURL)
	credentialChanged := baseURLChanged || apiKey != ""
	nextCredentialRevision := row.CredentialRevision
	if nextCredentialRevision < 1 {
		nextCredentialRevision = 1
	}
	if credentialChanged {
		nextCredentialRevision++
		updates["credential_revision"] = nextCredentialRevision
	}

	skipVerify := false
	if baseURLChanged {
		updates["is_verified"] = false
		// Address edits invalidate verification, never the saved key pool.
		// Re-encrypt for the new revision so V2 AAD stays bound to this row.
		preserved, err := encryptedAPIKeyUpdates(userID, row.ID, nextCredentialRevision, storedAPIKey, row.CredentialVersion)
		if err != nil {
			common.ReplyErr(w, "encrypt api key failed", http.StatusInternalServerError)
			return
		}
		for key, value := range preserved {
			updates[key] = value
		}
		skipVerify = true
	}
	if apiKey != "" {
		encryptedUpdates, encryptErr := encryptedAPIKeyUpdates(userID, row.ID, nextCredentialRevision, apiKey, row.CredentialVersion)
		if encryptErr != nil {
			common.ReplyErr(w, "encrypt api key failed", http.StatusInternalServerError)
			return
		}
		for key, value := range encryptedUpdates {
			updates[key] = value
		}
		if apiKey != storedAPIKey && !hasSharedProviderEndpoint(parent) {
			updates["is_verified"] = false
		}
	}

	var checkData *CheckModelProviderData
	effectiveAPIKey := apiKey
	if effectiveAPIKey == "" {
		effectiveAPIKey = storedAPIKey
	}
	if !skipVerify && effectiveAPIKey != "" && shouldVerifyCloudServiceOnSave(parent.Category, RuntimeProviderName(parent)) {
		checkResult, checkErr := doProviderGroupCheck(r.Context(), parent.Category, RuntimeProviderName(parent), baseURL, effectiveAPIKey, "", parent)
		if checkErr != nil || checkResult == nil || !checkResult.Success {
			msg := "verification failed"
			checkMsg := msg
			if checkResult != nil && strings.TrimSpace(checkResult.Message) != "" {
				checkMsg = strings.TrimSpace(checkResult.Message)
				msg = msg + ": " + checkMsg
			}
			common.ReplyErrWithData(
				w,
				msg,
				CheckModelProviderData{Success: false, Message: checkMsg},
				http.StatusBadGateway,
			)
			return
		}
		checkData = &CheckModelProviderData{Success: true, Message: checkResult.Message}
		updates["is_verified"] = true
	}

	// Shared model suppliers save independently of connection checks. The
	// legacy verify flag remains meaningful only for auxiliary services.
	if req.Verify && !hasSharedProviderEndpoint(parent) && checkData == nil {
		if effectiveAPIKey == "" && providerAPIKeyRequiredForBaseURL(r.Context(), db, parent, baseURL) {
			common.ReplyErr(w, "api_key is required", http.StatusBadRequest)
			return
		}
		checkResult, checkErr := doProviderGroupCheck(r.Context(), parent.Category, RuntimeProviderName(parent), baseURL, effectiveAPIKey, "", parent)
		if checkErr != nil || checkResult == nil || !checkResult.Success {
			msg := "verification failed"
			checkMsg := msg
			if checkResult != nil {
				checkMsg = strings.TrimSpace(checkResult.Message)
				if checkMsg != "" {
					msg = "verification failed: " + checkMsg
				}
			}
			common.ReplyErrWithData(
				w,
				msg,
				CheckModelProviderData{Success: false, Message: checkMsg},
				http.StatusBadGateway,
			)
			return
		}
		checkData = &CheckModelProviderData{Success: true, Message: checkResult.Message}
		updates["is_verified"] = true
	}
	if err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := updateConnectionSafely(tx, &parent, &row, baseURL, updates); err != nil {
			return err
		}
		if err := reconcileProviderActiveKey(tx, &parent); err != nil {
			return err
		}
		if credentialChanged {
			return enqueueCredentialBackup(tx, row.ID, nextCredentialRevision, credentialvault.BackupUpsert, now)
		}
		return nil
	}); err != nil {
		if errors.Is(err, errConnectionChanged) {
			common.ReplyErr(w, err.Error(), http.StatusConflict)
			return
		}
		common.ReplyErr(w, "update group failed", http.StatusInternalServerError)
		return
	}
	if hasSharedProviderEndpoint(parent) {
		if err := db.WithContext(r.Context()).Where("id = ? AND deleted_at IS NULL", row.ID).Take(&row).Error; err != nil {
			common.ReplyErr(w, "reload group failed", http.StatusInternalServerError)
			return
		}
	} else if isVerified, ok := updates["is_verified"].(bool); ok {
		row.IsVerified = isVerified
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
		IsVerified:          row.IsVerified,
		Check:               checkData,
	})
}

type deleteGroupResponse struct {
	ID                       string `json:"id"`
	DowngradedKnowledgeBases int64  `json:"downgraded_knowledge_bases"`
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

	if err := prepareProviderKeySelection(r.Context(), db, &parent); err != nil {
		common.ReplyErr(w, "initialize enabled api key failed", http.StatusInternalServerError)
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

	hasMultimodal := false

	now := time.Now().UTC()
	var downgradedKnowledgeBases int64
	err = db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if hasSharedProviderEndpoint(parent) {
			if err := lockProvider(tx, &parent); err != nil {
				return err
			}
		}
		// Re-read model types under the supplier lock, so concurrent edits
		// cannot turn this route into a bypass of individual-model protection.
		var groupModels []orm.UserModelProviderGroupModel
		if err := tx.Where("user_model_provider_group_id = ? AND create_user_id = ? AND deleted_at IS NULL", groupID, userID).Find(&groupModels).Error; err != nil {
			return err
		}
		modelIDs := make([]string, 0, len(groupModels))
		for _, model := range groupModels {
			modelIDs = append(modelIDs, model.ID)
			hasMultimodal = hasMultimodal || isMultimodalEmbeddingModelType(model.ModelType)
			if isTextEmbeddingModelType(model.ModelType) {
				if err := guardEmbeddingRemoval(tx, userID); err != nil {
					return err
				}
			}
		}

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
		if err := tx.Model(&orm.UserModelProviderGroup{}).
			Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", row.ID, userID).
			Updates(map[string]interface{}{
				"deleted_at": now,
				"updated_at": now,
			}).Error; err != nil {
			return err
		}
		return reconcileProviderActiveKey(tx, &parent)
	})
	if err != nil {
		if errors.Is(err, errEmbeddingInUse) {
			common.ReplyErr(w, err.Error(), http.StatusConflict)
			return
		}
		common.ReplyErr(w, "delete group failed", http.StatusInternalServerError)
		return
	}

	if hasMultimodal {
		maybeScheduleImageGroupLazyReset(r.Context(), db)
	}

	common.ReplyOK(w, deleteGroupResponse{ID: groupID, DowngradedKnowledgeBases: downgradedKnowledgeBases})
}

func normalizeBaseURLForCompare(s string) string {
	s = canonicalSenseNovaTokenPlanURL(strings.TrimSpace(s))
	for strings.HasSuffix(s, "/") {
		s = strings.TrimSuffix(s, "/")
	}
	return s
}

func defaultBaseURL(ctx context.Context, db *gorm.DB, defaultProviderID string) (string, bool) {
	if db == nil || strings.TrimSpace(defaultProviderID) == "" {
		return "", false
	}
	var catalog orm.DefaultModelProvider
	if err := db.WithContext(ctx).
		Where("id = ? AND deleted_at IS NULL", defaultProviderID).
		Take(&catalog).Error; err != nil {
		return "", false
	}
	return catalog.BaseURL, true
}

func isCustomBaseURL(ctx context.Context, db *gorm.DB, defaultProviderID, baseURL string) bool {
	defaultURL, ok := defaultBaseURL(ctx, db, defaultProviderID)
	return ok && normalizeBaseURLForCompare(baseURL) != normalizeBaseURLForCompare(defaultURL)
}

// Custom suppliers can point to unauthenticated self-hosted services even when
// their original catalog entry no longer exists. Official catalog endpoints
// still require credentials, and an empty/invalid URL is never keyless.
func providerAPIKeyRequiredForBaseURL(ctx context.Context, db *gorm.DB, parent orm.UserModelProvider, baseURL string) bool {
	if strings.TrimSpace(baseURL) == "" {
		return true
	}
	if isLocalOpenAISupplier(ctx, db, parent) && hasSharedProviderEndpoint(parent) {
		return !validCustomProviderURL(RuntimeProviderName(parent), baseURL)
	}
	defaultURL, known := defaultBaseURL(ctx, db, parent.DefaultModelProviderID)
	if !known {
		return !parent.IsCustom || !hasSharedProviderEndpoint(parent) || !validCustomProviderURL(RuntimeProviderName(parent), baseURL)
	}
	return normalizeBaseURLForCompare(baseURL) == normalizeBaseURLForCompare(LazyLLMBaseURL(RuntimeProviderName(parent), defaultURL))
}

// isVLLMSupplier retains the supplier identity after its active request protocol
// has been saved as OpenAI. Custom suppliers inherit identity from their template.
func isVLLMSupplier(ctx context.Context, db *gorm.DB, parent orm.UserModelProvider) bool {
	if normalizeProviderName(RuntimeProviderName(parent)) == "vllm" || (!parent.IsCustom && normalizeProviderName(parent.Name) == "vllm") {
		return true
	}
	if db == nil || parent.DefaultModelProviderID == "" || !db.Migrator().HasTable(&orm.DefaultModelProvider{}) {
		return false
	}
	var template orm.DefaultModelProvider
	if db.WithContext(ctx).Select("name").Where("id = ? AND deleted_at IS NULL", parent.DefaultModelProviderID).Take(&template).Error != nil {
		return false
	}
	return normalizeProviderName(template.Name) == "vllm"
}

// sensenovaNewPlatformBaseURL is the alternative SenseNova base URL.
const sensenovaNewPlatformBaseURL = "https://token.sensenova.cn/v1/"

// sensenovaNewPlatformModelNames lists the model names (from default_models seeding) that are
// specific to the new SenseNova platform URL. Model metadata comes from the catalog / DB.
var sensenovaNewPlatformModelNames = map[string]bool{
	"deepseek-flash":           true,
	"kimi-k3":                  true,
	"deepseek-v4-flash":        true,
	"glm-5.2":                  true,
	"sensenova-6.8-flash-lite": true,
	"sensenova-u1.5-fast":      true,
	"sensenova-u1.5-lite":      true,
}

func shouldSeedSenseNovaModel(modelName string, useNewPlatform bool) bool {
	return sensenovaNewPlatformModelNames[modelName] == useNewPlatform
}

// seedGroupModelsFromDefaults preserves catalog seeding for auxiliary services.
// Model suppliers discover their available models only after key verification;
// copying catalog rows here would bypass the user's choice for larger lists.
func seedGroupModelsFromDefaults(
	tx *gorm.DB,
	ctx context.Context,
	group *orm.UserModelProviderGroup,
	parent *orm.UserModelProvider,
	requestBaseURL, userID, userName string,
	now time.Time,
) ([]orm.UserModelProviderGroupModel, error) {
	if hasSharedProviderEndpoint(*parent) {
		return nil, nil
	}
	// Providers without has_models capability (e.g. OCR, search) have no model list.
	if !parent.HasCapability("has_models") {
		return nil, nil
	}

	// Determine whether we use the new-platform subset or the full catalog.
	useNewPlatform := strings.EqualFold(RuntimeProviderName(*parent), "SenseNova") &&
		normalizeBaseURLForCompare(requestBaseURL) == normalizeBaseURLForCompare(sensenovaNewPlatformBaseURL)

	var catalog orm.DefaultModelProvider
	err := tx.WithContext(ctx).
		Where("id = ? AND deleted_at IS NULL", parent.DefaultModelProviderID).
		Take(&catalog).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	// For the new SenseNova platform, seed only the Token Plan model subset (loaded from DB).
	// For all other providers / URLs, match the base URL against the catalog default.
	if useNewPlatform {
		// seed only the new-platform-specific models from default_models
	} else if normalizeBaseURLForCompare(requestBaseURL) != normalizeBaseURLForCompare(catalog.BaseURL) {
		return nil, nil
	}

	var defs []orm.DefaultModel
	if err := tx.WithContext(ctx).
		Where("default_model_provider_id = ? AND deleted_at IS NULL", parent.DefaultModelProviderID).
		Find(&defs).Error; err != nil {
		return nil, err
	}
	if len(defs) == 0 {
		return nil, nil
	}

	batch := make([]orm.UserModelProviderGroupModel, 0, len(defs))
	for _, d := range defs {
		if strings.EqualFold(RuntimeProviderName(*parent), "SenseNova") &&
			!shouldSeedSenseNovaModel(d.Name, useNewPlatform) {
			continue
		}
		batch = append(batch, orm.UserModelProviderGroupModel{
			ID:                       common.GenerateID(),
			UserModelProviderID:      parent.ID,
			UserModelProviderGroupID: group.ID,
			ProviderName:             d.ProviderName,
			Name:                     d.Name,
			ModelType:                d.ModelType,
			Vision:                   d.Vision,
			MaxInputTokens:           d.MaxInputTokens,
			FreeAutoSelectPriority:   d.FreeAutoSelectPriority,
			FreeAutoSelectBaseURLs:   d.FreeAutoSelectBaseURLs,
			IsDefault:                true,
			BaseModel: orm.BaseModel{
				CreateUserID:   userID,
				CreateUserName: userName,
				CreatedAt:      now,
				UpdatedAt:      now,
				DeletedAt:      nil,
			},
		})
	}
	if len(batch) == 0 {
		return nil, nil
	}
	if err := tx.WithContext(ctx).CreateInBatches(&batch, 100).Error; err != nil {
		return nil, err
	}
	return batch, nil
}

// isDefaultBaseURL reports whether the given base_url matches the catalog default for the provider.
// When true, the user is using the official hosted service and api_key is required.
func isDefaultBaseURL(ctx context.Context, db *gorm.DB, defaultProviderID, baseURL string) bool {
	defaultURL, ok := defaultBaseURL(ctx, db, defaultProviderID)
	if !ok {
		return false
	}
	return normalizeBaseURLForCompare(baseURL) == normalizeBaseURLForCompare(defaultURL)
}

type addKeyRequest struct {
	APIKey string `json:"api_key"`
}

type addKeyResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

type removeKeyRequest struct {
	APIKey string `json:"api_key,omitempty"`
	KeyID  string `json:"key_id,omitempty"`
}

// AddKey saves one key. Shared suppliers validate connectivity separately.
// POST /model_providers/{model_provider_id}/groups/{group_id}/keys
func AddKey(w http.ResponseWriter, r *http.Request) {
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

	var req addKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	newKey := strings.TrimSpace(req.APIKey)
	if newKey == "" || len(splitAPIKeys(newKey)) != 1 || len(newKey) > credentialvault.MaximumAPIKeyLength {
		common.ReplyErr(w, "api_key is required", http.StatusBadRequest)
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

	if err := prepareProviderKeySelection(r.Context(), db, &parent); err != nil {
		common.ReplyErr(w, "initialize enabled api key failed", http.StatusInternalServerError)
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

	storedAPIKeys, err := apiKeyForGroup(db.WithContext(r.Context()), &row)
	if err != nil {
		common.ReplyErr(w, "decrypt api key failed", http.StatusInternalServerError)
		return
	}

	// Check for duplicate.
	existing := splitAPIKeys(storedAPIKeys)
	for _, k := range existing {
		if k == newKey {
			common.ReplyErr(w, "api_key already exists", http.StatusConflict)
			return
		}
	}

	prospectiveKeys := append(append([]string(nil), existing...), newKey)
	if err := validateSavedKeyPool(parent, row.Name, connectionBaseURL(parent, row.BaseURL), strings.Join(prospectiveKeys, "\n")); err != nil {
		common.ReplyErr(w, "api_key pool exceeds credential limits", http.StatusBadRequest)
		return
	}
	checkResult := &modelCheckResponse{Success: true, Message: "saved"}
	var checkErr error
	if !hasSharedProviderEndpoint(parent) {
		checkResult, checkErr = doProviderGroupCheck(r.Context(), parent.Category, RuntimeProviderName(parent), connectionBaseURL(parent, row.BaseURL), newKey, "", parent)
	}
	if checkErr != nil || checkResult == nil || !checkResult.Success {
		msg := "verification failed"
		if checkResult != nil && strings.TrimSpace(checkResult.Message) != "" {
			msg = strings.TrimSpace(checkResult.Message)
		}
		common.ReplyErrWithData(w, "verification failed: "+msg, CheckModelProviderData{Success: false, Message: msg}, http.StatusBadGateway)
		return
	}

	// Append the new key.
	existing = append(existing, newKey)
	updatedKeys := strings.Join(existing, "\n")
	nextCredentialRevision := max(row.CredentialRevision, 0) + 1
	encryptedUpdates, err := encryptedAPIKeyUpdates(userID, row.ID, nextCredentialRevision, updatedKeys, row.CredentialVersion)
	if err != nil {
		common.ReplyErr(w, "encrypt api key failed", http.StatusInternalServerError)
		return
	}
	now := time.Now()
	encryptedUpdates["is_verified"] = row.IsVerified || !hasSharedProviderEndpoint(parent)
	encryptedUpdates["updated_at"] = now
	if err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := updateConnectionSafely(tx, &parent, &row, connectionBaseURL(parent, row.BaseURL), encryptedUpdates); err != nil {
			return err
		}
		if err := reconcileProviderActiveKey(tx, &parent); err != nil {
			return err
		}
		return enqueueCredentialBackup(tx, row.ID, nextCredentialRevision, credentialvault.BackupUpsert, now)
	}); err != nil {
		if errors.Is(err, errConnectionChanged) {
			common.ReplyErr(w, err.Error(), http.StatusConflict)
			return
		}
		common.ReplyErr(w, "update api_key failed", http.StatusInternalServerError)
		return
	}

	common.ReplyOK(w, addKeyResponse{Success: true, Message: checkResult.Message})
}

// RemoveKey removes a specific API key by exact match from the group.
// DELETE /model_providers/{model_provider_id}/groups/{group_id}/keys
func RemoveKey(w http.ResponseWriter, r *http.Request) {
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

	var req removeKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	targetKey := strings.TrimSpace(req.APIKey)
	targetID := strings.TrimSpace(req.KeyID)
	if targetKey == "" && targetID == "" {
		common.ReplyErr(w, "api_key is required", http.StatusBadRequest)
		return
	}
	if targetKey != "" && targetID != "" {
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

	if err := prepareProviderKeySelection(r.Context(), db, &parent); err != nil {
		common.ReplyErr(w, "initialize enabled api key failed", http.StatusInternalServerError)
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

	storedAPIKeys, err := apiKeyForGroup(db.WithContext(r.Context()), &row)
	if err != nil {
		common.ReplyErr(w, "decrypt api key failed", http.StatusInternalServerError)
		return
	}
	existing := splitAPIKeys(storedAPIKeys)
	found := false
	filtered := make([]string, 0, len(existing))
	for _, k := range existing {
		if (targetID != "" && groupKeyID(groupID, k) == targetID) || (targetID == "" && k == targetKey) {
			found = true
		} else {
			filtered = append(filtered, k)
		}
	}
	if !found {
		common.ReplyErr(w, "api_key not found", http.StatusNotFound)
		return
	}

	now := time.Now()
	updatedKeys := strings.Join(filtered, "\n")
	nextCredentialRevision := max(row.CredentialRevision, 0) + 1
	encryptedUpdates, err := encryptedAPIKeyUpdates(userID, row.ID, nextCredentialRevision, updatedKeys, row.CredentialVersion)
	if err != nil {
		common.ReplyErr(w, "encrypt api key failed", http.StatusInternalServerError)
		return
	}
	encryptedUpdates["is_verified"] = row.IsVerified && len(filtered) > 0
	encryptedUpdates["updated_at"] = now
	if err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := updateConnectionSafely(tx, &parent, &row, connectionBaseURL(parent, row.BaseURL), encryptedUpdates); err != nil {
			return err
		}
		if err := reconcileProviderActiveKey(tx, &parent); err != nil {
			return err
		}
		return enqueueCredentialBackup(tx, row.ID, nextCredentialRevision, credentialvault.BackupUpsert, now)
	}); err != nil {
		if errors.Is(err, errConnectionChanged) {
			common.ReplyErr(w, err.Error(), http.StatusConflict)
			return
		}
		common.ReplyErr(w, "update api_key failed", http.StatusInternalServerError)
		return
	}

	common.ReplyOK(w, map[string]bool{"success": true})
}

func splitAPIKeys(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, "\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func enqueueCredentialBackup(tx *gorm.DB, groupID string, revision int64, operation credentialvault.BackupOperation, now time.Time) error {
	return credentialvault.EnqueueCredentialBackup(tx, groupID, revision, operation, now)
}
