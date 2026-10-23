package modelprovider

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/credentialvault"
	"lazymind/core/store"
)

func savedKeyForRequest(w http.ResponseWriter, r *http.Request) (*gorm.DB, orm.UserModelProvider, orm.UserModelProviderGroup, string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	var parent orm.UserModelProvider
	var group orm.UserModelProviderGroup
	db, userID := store.DB(), strings.TrimSpace(store.UserID(r))
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return nil, parent, group, "", false
	}
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return nil, parent, group, "", false
	}
	vars := mux.Vars(r)
	if err := db.WithContext(r.Context()).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", strings.TrimSpace(vars["model_provider_id"]), userID).Take(&parent).Error; err != nil {
		common.ReplyErr(w, "model provider not found", http.StatusNotFound)
		return nil, parent, group, "", false
	}
	if err := db.WithContext(r.Context()).Where("id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", strings.TrimSpace(vars["group_id"]), parent.ID, userID).Take(&group).Error; err != nil {
		common.ReplyErr(w, "group not found", http.StatusNotFound)
		return nil, parent, group, "", false
	}
	pool, err := apiKeyForGroup(db.WithContext(r.Context()), &group)
	if err != nil {
		common.ReplyErr(w, "decrypt api key failed", http.StatusInternalServerError)
		return nil, parent, group, "", false
	}
	key := keyMatchingID(group.ID, strings.TrimSpace(vars["key_id"]), pool)
	if key == "" {
		common.ReplyErr(w, "api_key not found", http.StatusNotFound)
		return nil, parent, group, "", false
	}
	return db, parent, group, pool, true
}

// RevealKey returns one saved secret only after an explicit, owner-scoped action.
func RevealKey(w http.ResponseWriter, r *http.Request) {
	_, _, group, pool, ok := savedKeyForRequest(w, r)
	if !ok {
		return
	}
	common.ReplyOK(w, map[string]string{"api_key": keyMatchingID(group.ID, strings.TrimSpace(mux.Vars(r)["key_id"]), pool)})
}

// ReplaceKey atomically swaps its encrypted
// pool entry. Replacing the enabled slot retains that slot; other slots stay off.
func ReplaceKey(w http.ResponseWriter, r *http.Request) {
	db, parent, group, pool, ok := savedKeyForRequest(w, r)
	if !ok {
		return
	}
	var req addKeyRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	newKey := strings.TrimSpace(req.APIKey)
	if newKey == "" || len(newKey) > credentialvault.MaximumAPIKeyLength || strings.ContainsFunc(newKey, unicode.IsSpace) {
		common.ReplyErr(w, "api_key must be a single key of at most 512 bytes", http.StatusBadRequest)
		return
	}
	if err := prepareProviderKeySelection(r.Context(), db, &parent); err != nil {
		common.ReplyErr(w, "initialize enabled api key failed", http.StatusInternalServerError)
		return
	}
	oldID := strings.TrimSpace(mux.Vars(r)["key_id"])
	oldKey := keyMatchingID(group.ID, oldID, pool)
	newID := groupKeyID(group.ID, newKey)
	wasEnabled := parent.ActiveKeyGroupID == group.ID && parent.ActiveKeyID == oldID
	if oldKey == newKey {
		common.ReplyOK(w, map[string]any{"key_id": newID, "enabled": wasEnabled})
		return
	}
	keys := splitAPIKeys(pool)
	for i, key := range keys {
		if key == newKey {
			common.ReplyErr(w, "api_key already exists", http.StatusConflict)
			return
		}
		if groupKeyID(group.ID, key) == oldID {
			keys[i] = newKey
		}
	}
	updatedPool, expectedURL := strings.Join(keys, "\n"), connectionBaseURL(parent, group.BaseURL)
	if err := validateSavedKeyPool(parent, group.Name, expectedURL, updatedPool); err != nil {
		common.ReplyErr(w, "api_key pool exceeds credential limits", http.StatusBadRequest)
		return
	}
	result := &modelCheckResponse{Success: true}
	var checkErr error
	if !hasSharedProviderEndpoint(parent) {
		result, checkErr = doProviderGroupCheck(r.Context(), parent.Category, RuntimeProviderName(parent), expectedURL, newKey, "")
	}
	if checkErr != nil || result == nil || !result.Success {
		// An upstream error may echo request credentials. Keep the public mutation
		// response generic; the original saved secret and selection remain intact.
		common.ReplyErr(w, "API key verification failed", http.StatusBadGateway)
		return
	}
	revision, preferenceRevision, now := max(group.CredentialRevision, 0)+1, parent.ActiveKeyRevision, time.Now()
	updates, err := encryptedAPIKeyUpdates(parent.CreateUserID, group.ID, revision, updatedPool, group.CredentialVersion)
	if err != nil {
		common.ReplyErr(w, "encrypt api key failed", http.StatusInternalServerError)
		return
	}
	updates["updated_at"] = now
	if !hasSharedProviderEndpoint(parent) {
		updates["is_verified"] = true
	}
	err = db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := updateConnectionSafely(tx, &parent, &group, expectedURL, updates); err != nil {
			return err
		}
		if hasSharedProviderEndpoint(parent) {
			if parent.ActiveKeyRevision != preferenceRevision {
				return errConnectionChanged
			}
			if wasEnabled {
				if err := saveActiveProviderKey(tx, &parent, group.ID, newID, false, now); err != nil {
					return err
				}
			}
		}
		return enqueueCredentialBackup(tx, group.ID, revision, credentialvault.BackupUpsert, now)
	})
	if err != nil {
		if errors.Is(err, errConnectionChanged) {
			common.ReplyErr(w, err.Error(), http.StatusConflict)
		} else {
			common.ReplyErr(w, "update api key failed", http.StatusInternalServerError)
		}
		return
	}
	common.ReplyOK(w, map[string]any{"key_id": newID, "enabled": wasEnabled})
}
