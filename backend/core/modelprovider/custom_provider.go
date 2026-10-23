package modelprovider

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/credentialvault"
	"lazymind/core/store"
)

// RuntimeProviderName separates a supplier's editable display identity from the
// protocol adapter used to check credentials and construct runtime model calls.
func RuntimeProviderName(provider orm.UserModelProvider) string {
	if name := strings.TrimSpace(provider.ProtocolName); name != "" {
		return name
	}
	return provider.Name
}

var errCanonicalGroupExists = errors.New("model supplier already has credential storage")
var errEmbeddingConfirmationRequired = errors.New("removing an embedding model requires confirmation to downgrade all indexed knowledge bases to chunked")

type createCustomProviderRequest struct {
	Name             string `json:"name"`
	SourceProviderID string `json:"source_provider_id"`
	BaseURL          string `json:"base_url"`
}
type customProviderResponse struct {
	ID      string `json:"id"`
	GroupID string `json:"group_id,omitempty"`
	Name    string `json:"name,omitempty"`
}

func validCustomProviderURL(protocol, value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && len(value) <= 1024 && !hasOpenAIRequestPath(protocol, value)
}

func CreateCustomProvider(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	user := strings.TrimSpace(store.UserID(r))
	if db == nil {
		common.ReplyErr(w, "store not initialized", 500)
		return
	}
	if user == "" {
		common.ReplyErr(w, "missing X-User-Id", 400)
		return
	}
	var req createCustomProviderRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	req.Name, req.SourceProviderID, req.BaseURL = strings.TrimSpace(req.Name), strings.TrimSpace(req.SourceProviderID), strings.TrimSpace(req.BaseURL)
	if req.Name == "" || len(req.Name) > 255 {
		common.ReplyErr(w, "name must contain 1 to 255 bytes", 400)
		return
	}
	var source orm.UserModelProvider
	if err := db.WithContext(r.Context()).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL AND category = ?", req.SourceProviderID, user, defaultProviderCategory).Take(&source).Error; err != nil {
		common.ReplyErr(w, "source model provider not found", 404)
		return
	}
	if !validCustomProviderURL(RuntimeProviderName(source), req.BaseURL) {
		common.ReplyErr(w, "base_url must be an HTTP(S) API root", 400)
		return
	}
	now := time.Now().UTC()
	row := orm.UserModelProvider{ID: common.GenerateID(), DefaultModelProviderID: source.DefaultModelProviderID, Name: req.Name, ProtocolName: RuntimeProviderName(source), IsCustom: true, Category: defaultProviderCategory, Capabilities: strings.Join(modelSupplierCapabilities(source.Capabilities), ","), BaseURL: LazyLLMBaseURL(RuntimeProviderName(source), req.BaseURL), BaseModel: orm.BaseModel{CreateUserID: user, CreateUserName: store.UserName(r), CreatedAt: now, UpdatedAt: now}}
	group := orm.UserModelProviderGroup{ID: common.GenerateID(), UserModelProviderID: row.ID, Name: row.Name, BaseURL: row.BaseURL, CredentialRevision: 1, BaseModel: row.BaseModel}
	if err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := lockProvider(tx, &source); err != nil {
			return err
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return tx.Create(&group).Error
	}); err != nil {
		common.ReplyErr(w, "create custom provider failed", 500)
		return
	}
	common.ReplyOK(w, customProviderResponse{ID: row.ID, GroupID: group.ID, Name: row.Name})
}

func customProviderForRequest(w http.ResponseWriter, r *http.Request) (*gorm.DB, orm.UserModelProvider, bool) {
	db := store.DB()
	var row orm.UserModelProvider
	user := strings.TrimSpace(store.UserID(r))
	if db == nil {
		common.ReplyErr(w, "store not initialized", 500)
		return db, row, false
	}
	if user == "" {
		common.ReplyErr(w, "missing X-User-Id", 400)
		return db, row, false
	}
	if err := db.WithContext(r.Context()).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", strings.TrimSpace(mux.Vars(r)["model_provider_id"]), user).Take(&row).Error; err != nil {
		common.ReplyErr(w, "model provider not found", 404)
		return db, row, false
	}
	if !row.IsCustom || !hasSharedProviderEndpoint(row) {
		common.ReplyErr(w, "only custom model suppliers can be renamed or deleted", 400)
		return db, row, false
	}
	return db, row, true
}

func UpdateCustomProvider(w http.ResponseWriter, r *http.Request) {
	db, row, ok := customProviderForRequest(w, r)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 255 {
		common.ReplyErr(w, "name must contain 1 to 255 bytes", 400)
		return
	}
	now := time.Now().UTC()
	err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := lockProvider(tx, &row); err != nil {
			return err
		}
		var groups []orm.UserModelProviderGroup
		if err := tx.Where("user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", row.ID, row.CreateUserID).Find(&groups).Error; err != nil {
			return err
		}
		for i := range groups {
			key, err := apiKeyForGroup(tx, &groups[i])
			if err != nil {
				return err
			}
			if err := validateSavedKeyPool(row, req.Name, row.BaseURL, key); err != nil {
				return err
			}
		}
		if err := tx.Model(&row).Updates(map[string]any{"name": req.Name, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&orm.UserModelProviderGroup{}).Where("user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", row.ID, row.CreateUserID).Updates(map[string]any{"name": req.Name, "updated_at": now}).Error; err != nil {
			return err
		}
		for _, g := range groups {
			if err := enqueueCredentialBackup(tx, g.ID, max(g.CredentialRevision, 1), credentialvault.BackupUpsert, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		common.ReplyErr(w, "rename custom provider failed", 500)
		return
	}
	common.ReplyOK(w, customProviderResponse{ID: row.ID, Name: req.Name})
}

func DeleteCustomProvider(w http.ResponseWriter, r *http.Request) {
	db, row, ok := customProviderForRequest(w, r)
	if !ok {
		return
	}
	hasImage := false
	now := time.Now().UTC()
	var downgraded int64
	err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := lockProvider(tx, &row); err != nil {
			return err
		}
		var models []orm.UserModelProviderGroupModel
		if err := tx.Where("user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", row.ID, row.CreateUserID).Find(&models).Error; err != nil {
			return err
		}
		hasEmbedding := false
		for _, model := range models {
			hasEmbedding = hasEmbedding || isTextEmbeddingModelType(model.ModelType)
			hasImage = hasImage || isMultimodalEmbeddingModelType(model.ModelType)
		}
		if hasEmbedding && !indexedDowngradeConfirmed(r) {
			return errEmbeddingConfirmationRequired
		}
		if hasEmbedding {
			count, err := downgradeIndexedDatasets(tx, row.CreateUserID, now)
			if err != nil {
				return err
			}
			downgraded = count
		}
		var groups []orm.UserModelProviderGroup
		if err := tx.Where("user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", row.ID, row.CreateUserID).Find(&groups).Error; err != nil {
			return err
		}
		for _, g := range groups {
			if err := enqueueCredentialBackup(tx, g.ID, max(g.CredentialRevision, 1)+1, credentialvault.BackupDelete, now); err != nil {
				return err
			}
		}
		if tx.Migrator().HasTable(&orm.UserSelectedModel{}) {
			var ids []string
			if err := tx.Model(&orm.UserModelProviderGroupModel{}).Where("user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", row.ID, row.CreateUserID).Pluck("id", &ids).Error; err != nil {
				return err
			}
			if len(ids) > 0 {
				if err := tx.Where("user_model_provider_group_model_id IN ?", ids).Delete(&orm.UserSelectedModel{}).Error; err != nil {
					return err
				}
			}
		}
		updates := map[string]any{"deleted_at": now, "updated_at": now}
		if err := tx.Model(&orm.UserModelProviderGroupModel{}).Where("user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", row.ID, row.CreateUserID).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Model(&orm.UserModelProviderGroup{}).Where("user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", row.ID, row.CreateUserID).Updates(updates).Error; err != nil {
			return err
		}
		updates["active_key_id"], updates["active_key_group_id"], updates["active_key_initialized"], updates["active_key_revision"] = "", "", true, row.ActiveKeyRevision+1
		return tx.Model(&row).Updates(updates).Error
	})
	if err != nil {
		if errors.Is(err, errEmbeddingConfirmationRequired) {
			common.ReplyErr(w, err.Error(), http.StatusConflict)
			return
		}
		common.ReplyErr(w, "delete custom provider failed", 500)
		return
	}
	if hasImage {
		maybeScheduleImageGroupLazyReset(r.Context(), db)
	}
	common.ReplyOK(w, deleteGroupResponse{ID: row.ID, DowngradedKnowledgeBases: downgraded})
}
