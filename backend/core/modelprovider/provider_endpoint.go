package modelprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/credentialvault"
	"lazymind/core/store"
)

func hasSharedProviderEndpoint(provider orm.UserModelProvider) bool {
	return strings.EqualFold(strings.TrimSpace(provider.Category), defaultProviderCategory)
}

// Groups retain a mirrored URL for runtime/backward compatibility. Model
// suppliers own the URL; connection instances only own their name and keys.
func connectionBaseURL(provider orm.UserModelProvider, fallback string) string {
	if hasSharedProviderEndpoint(provider) && strings.TrimSpace(provider.BaseURL) != "" {
		return LazyLLMBaseURL(provider.Name, strings.TrimSpace(provider.BaseURL))
	}
	return LazyLLMBaseURL(provider.Name, strings.TrimSpace(fallback))
}

func lockProvider(tx *gorm.DB, provider *orm.UserModelProvider) error {
	return tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", provider.ID, provider.CreateUserID).Take(provider).Error
}

type updateProviderEndpointRequest struct {
	BaseURL string `json:"base_url"`
}
type updateProviderEndpointResponse struct {
	ID      string `json:"id"`
	BaseURL string `json:"base_url"`
}

// UpdateProviderEndpoint changes the shared endpoint for all of a supplier's
// connections atomically. Existing keys, models, and selected-model IDs survive.
func UpdateProviderEndpoint(w http.ResponseWriter, r *http.Request) {
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
	providerID := strings.TrimSpace(mux.Vars(r)["model_provider_id"])
	var req updateProviderEndpointRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	var parent orm.UserModelProvider
	if err := db.WithContext(r.Context()).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", providerID, userID).Take(&parent).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "model provider not found", http.StatusNotFound)
		} else {
			common.ReplyErr(w, "query model provider failed", http.StatusInternalServerError)
		}
		return
	}
	if !hasSharedProviderEndpoint(parent) {
		common.ReplyErr(w, "only model suppliers have a shared endpoint", http.StatusBadRequest)
		return
	}
	baseURL := strings.TrimSpace(req.BaseURL)
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || len(baseURL) > 1024 || hasOpenAIRequestPath(parent.Name, baseURL) {
		common.ReplyErr(w, "base_url must be an HTTP(S) API root without credentials, query, fragment, or request path", http.StatusBadRequest)
		return
	}
	baseURL = LazyLLMBaseURL(parent.Name, baseURL)
	if err := setProviderEndpoint(r.Context(), db, &parent, baseURL, time.Now()); err != nil {
		common.ReplyErr(w, "update model provider endpoint failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, updateProviderEndpointResponse{ID: parent.ID, BaseURL: parent.BaseURL})
}

func setProviderEndpoint(ctx context.Context, db *gorm.DB, parent *orm.UserModelProvider, baseURL string, now time.Time) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockProvider(tx, parent); err != nil {
			return err
		}
		if err := initializeProviderActiveKey(tx, parent); err != nil {
			return err
		}
		changed := normalizeBaseURLForCompare(parent.BaseURL) != normalizeBaseURLForCompare(baseURL)
		var groups []orm.UserModelProviderGroup
		if err := tx.Where("user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", parent.ID, parent.CreateUserID).
			Order("created_at ASC, id ASC").Find(&groups).Error; err != nil {
			return err
		}
		for i := range groups {
			group := &groups[i]
			// Repair a stale compatibility mirror even when the supplier URL is unchanged.
			if !changed && normalizeBaseURLForCompare(group.BaseURL) == normalizeBaseURLForCompare(baseURL) {
				continue
			}
			key, err := apiKeyForGroup(tx, group)
			if err != nil {
				return err
			}
			if err := validateSavedKeyPool(*parent, group.Name, baseURL, key); err != nil {
				return err
			}
			revision := max(group.CredentialRevision, 1) + 1
			updates, err := encryptedAPIKeyUpdates(parent.CreateUserID, group.ID, revision, key, group.CredentialVersion)
			if err != nil {
				return err
			}
			updates["base_url"], updates["is_verified"], updates["updated_at"] = baseURL, false, now
			if err := tx.Model(group).Updates(updates).Error; err != nil {
				return err
			}
			if err := enqueueCredentialBackup(tx, group.ID, revision, credentialvault.BackupUpsert, now); err != nil {
				return err
			}
		}
		if err := tx.Model(parent).Updates(map[string]any{"base_url": baseURL, "updated_at": now}).Error; err != nil {
			return err
		}
		parent.BaseURL = baseURL
		return nil
	})
}

var errConnectionChanged = errors.New("connection changed; reload and retry")

// All connection credential writers share the supplier lock. Optimistic checks
// additionally reject work verified against credentials/URLs that changed while
// a slow upstream check was in flight.
func updateConnectionSafely(tx *gorm.DB, parent *orm.UserModelProvider, row *orm.UserModelProviderGroup, expectedURL string, updates map[string]any) error {
	if hasSharedProviderEndpoint(*parent) {
		if err := lockProvider(tx, parent); err != nil {
			return err
		}
		if normalizeBaseURLForCompare(connectionBaseURL(*parent, expectedURL)) != normalizeBaseURLForCompare(expectedURL) {
			return errConnectionChanged
		}
	}
	result := tx.Model(&orm.UserModelProviderGroup{}).
		Where("id = ? AND user_model_provider_id = ? AND create_user_id = ? AND credential_revision = ? AND base_url = ? AND deleted_at IS NULL", row.ID, parent.ID, row.CreateUserID, row.CredentialRevision, row.BaseURL).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errConnectionChanged
	}
	return nil
}
