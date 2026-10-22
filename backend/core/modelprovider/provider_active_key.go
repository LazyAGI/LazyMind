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
	"lazymind/core/credentialvault"
	"lazymind/core/store"
)

var (
	ErrProviderKeyDisabled   = errors.New("model supplier has no enabled API key")
	ErrProviderKeyUnverified = errors.New("model supplier API key requires verification")
)

type activeProviderKeyRequest struct {
	GroupID string `json:"group_id"`
	KeyID   string `json:"key_id"`
}
type activeProviderKeyResponse struct {
	GroupID string `json:"group_id"`
	KeyID   string `json:"key_id"`
}

// SetActiveProviderKey enables exactly one saved key for the whole supplier.
// A failed check leaves the previous choice intact. Empty IDs disable all keys.
func SetActiveProviderKey(w http.ResponseWriter, r *http.Request) {
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
	var req activeProviderKeyRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.GroupID, req.KeyID = strings.TrimSpace(req.GroupID), strings.TrimSpace(req.KeyID)
	if (req.GroupID == "") != (req.KeyID == "") {
		common.ReplyErr(w, "group_id and key_id must both be supplied or both be empty", http.StatusBadRequest)
		return
	}
	providerID := strings.TrimSpace(mux.Vars(r)["model_provider_id"])
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
		common.ReplyErr(w, "only model suppliers have a supplier-wide API key", http.StatusBadRequest)
		return
	}
	preferenceRevision := parent.ActiveKeyRevision
	var checkedGroup orm.UserModelProviderGroup
	var checkedKey, checkedURL string
	if req.KeyID != "" {
		if err := db.WithContext(r.Context()).Where("id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", req.GroupID, parent.ID, userID).Take(&checkedGroup).Error; err != nil {
			common.ReplyErr(w, "group not found", http.StatusNotFound)
			return
		}
		pool, err := apiKeyForGroup(db.WithContext(r.Context()), &checkedGroup)
		if err != nil {
			common.ReplyErr(w, "decrypt api key failed", http.StatusInternalServerError)
			return
		}
		checkedKey = keyMatchingID(checkedGroup.ID, req.KeyID, pool)
		if checkedKey == "" {
			common.ReplyErr(w, "api_key not found", http.StatusNotFound)
			return
		}
		checkedURL = connectionBaseURL(parent, checkedGroup.BaseURL)
		result, err := doProviderGroupCheck(r.Context(), parent.Category, parent.Name, checkedURL, checkedKey, "")
		if err != nil || result == nil || !result.Success {
			msg := "verification failed"
			if result != nil && strings.TrimSpace(result.Message) != "" {
				msg = strings.TrimSpace(result.Message)
			}
			common.ReplyErrWithData(w, "verification failed: "+msg, CheckModelProviderData{Success: false, Message: msg}, http.StatusBadGateway)
			return
		}
	}
	err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := lockProvider(tx, &parent); err != nil {
			return err
		}
		if req.KeyID != "" {
			if parent.ActiveKeyRevision != preferenceRevision {
				return errConnectionChanged
			}
			if normalizeBaseURLForCompare(connectionBaseURL(parent, checkedURL)) != normalizeBaseURLForCompare(checkedURL) {
				return errConnectionChanged
			}
			var current orm.UserModelProviderGroup
			if err := tx.Where("id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", req.GroupID, parent.ID, userID).Take(&current).Error; err != nil {
				return errConnectionChanged
			}
			if current.CredentialRevision != checkedGroup.CredentialRevision {
				return errConnectionChanged
			}
			pool, err := apiKeyForGroup(tx, &current)
			if err != nil {
				return err
			}
			if keyMatchingID(current.ID, req.KeyID, pool) != checkedKey {
				return errConnectionChanged
			}
		}
		return saveActiveProviderKey(tx, &parent, req.GroupID, req.KeyID, req.KeyID != "", time.Now())
	})
	if err != nil {
		if errors.Is(err, errConnectionChanged) {
			common.ReplyErr(w, err.Error(), http.StatusConflict)
		} else {
			common.ReplyErr(w, "update enabled api key failed", http.StatusInternalServerError)
		}
		return
	}
	common.ReplyOK(w, activeProviderKeyResponse{GroupID: parent.ActiveKeyGroupID, KeyID: parent.ActiveKeyID})
}

func keyMatchingID(groupID, keyID, pool string) string {
	for _, key := range splitAPIKeys(pool) {
		if groupKeyID(groupID, key) == keyID {
			return key
		}
	}
	return ""
}

func saveActiveProviderKey(tx *gorm.DB, parent *orm.UserModelProvider, groupID, keyID string, verified bool, now time.Time) error {
	nextRevision := parent.ActiveKeyRevision + 1
	if err := tx.Model(parent).Updates(map[string]any{"active_key_group_id": groupID, "active_key_id": keyID, "active_key_initialized": true, "active_key_revision": nextRevision, "updated_at": now}).Error; err != nil {
		return err
	}
	parent.ActiveKeyGroupID, parent.ActiveKeyID, parent.ActiveKeyInitialized = groupID, keyID, true
	parent.ActiveKeyRevision = nextRevision
	return setSupplierVerification(tx, *parent, verified, now)
}

func setSupplierVerification(tx *gorm.DB, parent orm.UserModelProvider, verified bool, now time.Time) error {
	return tx.Model(&orm.UserModelProviderGroup{}).Where("user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", parent.ID, parent.CreateUserID).
		Updates(map[string]any{"is_verified": verified, "updated_at": now}).Error
}

// initializeProviderActiveKey is called with the supplier locked. SQL cannot
// decrypt existing pools, so their first saved key is promoted once in Go.
// Suppliers without keys remain eligible for first-key setup; explicit disable
// always stores initialized=true and can never trigger this initialization.
func initializeProviderActiveKey(tx *gorm.DB, parent *orm.UserModelProvider) error {
	if !hasSharedProviderEndpoint(*parent) || parent.ActiveKeyInitialized {
		return nil
	}
	var groups []orm.UserModelProviderGroup
	if err := tx.Where("user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", parent.ID, parent.CreateUserID).Order("created_at ASC, id ASC").Find(&groups).Error; err != nil {
		return err
	}
	for i := range groups {
		pool, err := apiKeyForGroup(tx, &groups[i])
		if err != nil {
			return err
		}
		if keys := splitAPIKeys(pool); len(keys) > 0 {
			return saveActiveProviderKey(tx, parent, groups[i].ID, groupKeyID(groups[i].ID, keys[0]), groups[i].IsVerified, time.Now())
		}
	}
	return nil
}

func hasStoredProviderKeys(tx *gorm.DB, parent orm.UserModelProvider) (bool, error) {
	var count int64
	err := tx.Model(&orm.UserModelProviderGroup{}).Where("user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL AND (TRIM(api_key) <> '' OR TRIM(api_key_ciphertext) <> '')", parent.ID, parent.CreateUserID).Count(&count).Error
	return count > 0, err
}

// reconcileProviderActiveKey preserves the current choice after pool edits.
// Removing the selected key disables the supplier; it never chooses a successor.
func reconcileProviderActiveKey(tx *gorm.DB, parent *orm.UserModelProvider) error {
	if !hasSharedProviderEndpoint(*parent) {
		return nil
	}
	if err := initializeProviderActiveKey(tx, parent); err != nil {
		return err
	}
	if parent.ActiveKeyID == "" {
		hasKeys, err := hasStoredProviderKeys(tx, *parent)
		if err != nil {
			return err
		}
		if hasKeys {
			return setSupplierVerification(tx, *parent, false, time.Now())
		}
		return nil
	}
	group, key, err := selectedProviderKey(tx, *parent)
	if errors.Is(err, ErrProviderKeyDisabled) {
		return saveActiveProviderKey(tx, parent, "", "", false, time.Now())
	}
	if err != nil {
		return err
	}
	if key == "" {
		return saveActiveProviderKey(tx, parent, "", "", false, time.Now())
	}
	return setSupplierVerification(tx, *parent, group.IsVerified, time.Now())
}

func selectedProviderKey(tx *gorm.DB, parent orm.UserModelProvider) (orm.UserModelProviderGroup, string, error) {
	var group orm.UserModelProviderGroup
	if parent.ActiveKeyGroupID == "" || parent.ActiveKeyID == "" {
		return group, "", ErrProviderKeyDisabled
	}
	err := tx.Where("id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", parent.ActiveKeyGroupID, parent.ID, parent.CreateUserID).Take(&group).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return group, "", ErrProviderKeyDisabled
	}
	if err != nil {
		return group, "", err
	}
	pool, err := apiKeyForGroup(tx, &group)
	if err != nil {
		return group, "", err
	}
	key := keyMatchingID(group.ID, parent.ActiveKeyID, pool)
	if key == "" {
		return group, "", ErrProviderKeyDisabled
	}
	return group, key, nil
}

// ResolveProviderRuntimeCredential returns one consistent supplier endpoint/key
// snapshot. Model keys never rotate and inactive saved keys are never returned.
func ResolveProviderRuntimeCredential(ctx context.Context, db *gorm.DB, groupID string) (string, string, error) {
	if db == nil {
		return "", "", errors.New("store not initialized")
	}
	var baseURL, key string
	var unavailable error
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var requested orm.UserModelProviderGroup
		if err := tx.Where("id = ? AND deleted_at IS NULL", strings.TrimSpace(groupID)).Take(&requested).Error; err != nil {
			return err
		}
		var parent orm.UserModelProvider
		if err := tx.Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", requested.UserModelProviderID, requested.CreateUserID).Take(&parent).Error; err != nil {
			return err
		}
		baseURL = connectionBaseURL(parent, requested.BaseURL)
		if !hasSharedProviderEndpoint(parent) {
			pool, err := apiKeyForGroup(tx, &requested)
			if err != nil {
				return err
			}
			if keys := splitAPIKeys(pool); len(keys) > 0 {
				key = keys[0]
			}
			return nil
		}
		if err := lockProvider(tx, &parent); err != nil {
			return err
		}
		if err := tx.Where("id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", requested.ID, parent.ID, parent.CreateUserID).Take(&requested).Error; err != nil {
			return err
		}
		baseURL = connectionBaseURL(parent, requested.BaseURL)
		if err := initializeProviderActiveKey(tx, &parent); err != nil {
			return err
		}
		if parent.ActiveKeyID == "" {
			hasKeys, err := hasStoredProviderKeys(tx, parent)
			if err != nil {
				return err
			}
			if hasKeys || isAPIKeyRequiredForBaseURL(ctx, tx, parent.DefaultModelProviderID, baseURL) {
				unavailable = ErrProviderKeyDisabled
				return nil
			}
			if err := tx.Where("id = ? AND deleted_at IS NULL", requested.ID).Take(&requested).Error; err != nil {
				return err
			}
			if !requested.IsVerified {
				unavailable = ErrProviderKeyUnverified
				return nil
			}
			return nil
		}
		selected, selectedKey, err := selectedProviderKey(tx, parent)
		if err != nil {
			if errors.Is(err, ErrProviderKeyDisabled) {
				unavailable = err
				return nil
			}
			return err
		}
		if !selected.IsVerified {
			unavailable = ErrProviderKeyUnverified
			return nil
		}
		key = selectedKey
		return nil
	})
	if err != nil {
		return "", "", err
	}
	if unavailable != nil {
		return "", "", unavailable
	}
	return baseURL, key, nil
}

func ResolveProviderAPIKey(ctx context.Context, db *gorm.DB, groupID string) (string, error) {
	_, key, err := ResolveProviderRuntimeCredential(ctx, db, groupID)
	return key, err
}

func prepareProviderKeySelection(ctx context.Context, db *gorm.DB, parent *orm.UserModelProvider) error {
	if !hasSharedProviderEndpoint(*parent) {
		return nil
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockProvider(tx, parent); err != nil {
			return err
		}
		return initializeProviderActiveKey(tx, parent)
	})
}

// checkSharedProviderGroup may use an unverified selected key to validate it.
// Checking a different explicitly submitted key never activates that key or
// changes verification of the supplier's current credential.
func checkSharedProviderGroup(w http.ResponseWriter, r *http.Request, db *gorm.DB, parent orm.UserModelProvider, group orm.UserModelProviderGroup, req checkModelProviderRequest, explicitKey bool) {
	var selected orm.UserModelProviderGroup
	var selectedKey string
	var storedKeys bool
	err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := lockProvider(tx, &parent); err != nil {
			return err
		}
		if err := initializeProviderActiveKey(tx, &parent); err != nil {
			return err
		}
		var err error
		selected, selectedKey, err = selectedProviderKey(tx, parent)
		if err != nil && !errors.Is(err, ErrProviderKeyDisabled) {
			return err
		}
		storedKeys, err = hasStoredProviderKeys(tx, parent)
		return err
	})
	if err != nil {
		common.ReplyErr(w, "resolve enabled api key failed", http.StatusInternalServerError)
		return
	}
	baseURL := connectionBaseURL(parent, group.BaseURL)
	if baseURL == "" {
		common.ReplyErr(w, "base_url is required", http.StatusBadRequest)
		return
	}
	key := selectedKey
	if explicitKey {
		key = strings.TrimSpace(req.APIKey)
	} else if selectedKey == "" && storedKeys {
		common.ReplyErr(w, ErrProviderKeyDisabled.Error(), http.StatusBadRequest)
		return
	}
	if key == "" && isAPIKeyRequiredForBaseURL(r.Context(), db, parent.DefaultModelProviderID, baseURL) {
		common.ReplyErr(w, "api_key is required", http.StatusBadRequest)
		return
	}
	result, checkErr := doProviderGroupCheck(r.Context(), parent.Category, parent.Name, baseURL, key, strings.TrimSpace(req.Model))
	verifiesCurrent := (selectedKey != "" && key == selectedKey) || (!storedKeys && key == "")
	if !req.DryRun && verifiesCurrent {
		err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
			before := parent
			if err := lockProvider(tx, &parent); err != nil {
				return err
			}
			if parent.ActiveKeyRevision != before.ActiveKeyRevision || parent.ActiveKeyGroupID != before.ActiveKeyGroupID || parent.ActiveKeyID != before.ActiveKeyID || normalizeBaseURLForCompare(connectionBaseURL(parent, baseURL)) != normalizeBaseURLForCompare(baseURL) {
				return errConnectionChanged
			}
			var count int64
			if err := tx.Model(&orm.UserModelProviderGroup{}).Where("id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", group.ID, parent.ID, parent.CreateUserID).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return gorm.ErrRecordNotFound
			}
			if selectedKey != "" {
				current, currentKey, err := selectedProviderKey(tx, parent)
				if err != nil || current.CredentialRevision != selected.CredentialRevision || currentKey != selectedKey {
					return errConnectionChanged
				}
			} else {
				hasKeys, err := hasStoredProviderKeys(tx, parent)
				if err != nil {
					return err
				}
				if hasKeys {
					return errConnectionChanged
				}
			}
			return setSupplierVerification(tx, parent, checkErr == nil && result != nil && result.Success, time.Now())
		})
		if err != nil {
			if errors.Is(err, errConnectionChanged) {
				common.ReplyErr(w, err.Error(), http.StatusConflict)
			} else if errors.Is(err, gorm.ErrRecordNotFound) {
				common.ReplyErr(w, "group not found", http.StatusNotFound)
			} else {
				common.ReplyErr(w, "update group verify status failed", http.StatusInternalServerError)
			}
			return
		}
	}
	if checkErr != nil {
		common.ReplyErrWithData(w, checkErr.Error(), result, http.StatusBadGateway)
		return
	}
	if result == nil {
		common.ReplyErr(w, "verification failed", http.StatusBadGateway)
		return
	}
	if req.DryRun && result.Success {
		recentVerifyCache.Store(verifyCheckCacheKey(group.ID, baseURL, key), time.Now().Add(5*time.Minute))
	}
	common.ReplyOK(w, CheckModelProviderData{Success: result.Success, Message: result.Message})
}

func validateSavedKeyPool(parent orm.UserModelProvider, groupName, baseURL, pool string) error {
	if strings.TrimSpace(pool) == "" {
		return nil
	}
	catalogKey := strings.TrimSpace(parent.DefaultModelProviderID)
	if catalogKey == "" {
		catalogKey = parent.Name
	}
	payload, err := credentialvault.EncodeProviderCredentialPayload(credentialvault.ProviderCredential{
		ProviderCatalogKey: catalogKey, DisplayName: groupName, BaseURL: baseURL, APIKey: []byte(pool),
	})
	clear(payload)
	return err
}
