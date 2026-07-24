package chat

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

// GetChatSettings returns the per-user global defaults for plugin/subagent config.
func GetChatSettings(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "db unavailable", http.StatusInternalServerError)
		return
	}
	var s orm.UserChatSettings
	if err := db.WithContext(r.Context()).Where("user_id = ?", userID).First(&s).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			// Return defaults.
			s = orm.UserChatSettings{
				UserID:         userID,
				EnablePlugin:   true,
				PluginMode:     "dynamic",
				EnableSubagent: true,
			}
		} else {
			common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	common.ReplyOK(w, map[string]any{
		"enable_plugin":   s.EnablePlugin,
		"plugin_mode":     s.PluginMode,
		"enable_subagent": s.EnableSubagent,
		"updated_at":      s.UpdatedAt,
	})
}

// PatchConversationPluginSettings updates conversation-level plugin/subagent overrides.
// Supports enable_plugin, plugin_mode, enable_subagent; null clears back to global default.
func PatchConversationPluginSettings(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "db unavailable", http.StatusInternalServerError)
		return
	}
	convID := strings.TrimSpace(mux.Vars(r)["conversation_id"])
	if convID == "" {
		common.ReplyErr(w, "conversation_id required", http.StatusBadRequest)
		return
	}

	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}

	updates := map[string]any{}
	if raw, present := body["enable_plugin"]; present {
		if raw == nil {
			updates["enable_plugin"] = nil
		} else if v, ok := raw.(bool); ok {
			updates["enable_plugin"] = v
		}
	}
	if raw, present := body["enable_subagent"]; present {
		if raw == nil {
			updates["enable_subagent"] = nil
		} else if v, ok := raw.(bool); ok {
			updates["enable_subagent"] = v
		}
	}
	if raw, present := body["plugin_mode"]; present {
		if raw == nil {
			updates["plugin_mode"] = nil
		} else if v, ok := raw.(string); ok {
			v = strings.TrimSpace(v)
			if v != "auto" && v != "dynamic" {
				common.ReplyErr(w, "plugin_mode must be 'auto' or 'dynamic'", http.StatusBadRequest)
				return
			}
			updates["plugin_mode"] = v
		}
	}
	if len(updates) == 0 {
		common.ReplyErr(w, "no valid fields to update", http.StatusBadRequest)
		return
	}

	if err := db.WithContext(r.Context()).Model(&orm.Conversation{}).
		Where("id = ? AND create_user_id = ?", convID, userID).
		Updates(updates).Error; err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, nil)
}
func PatchChatSettings(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "db unavailable", http.StatusInternalServerError)
		return
	}

	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}

	updates := map[string]any{"updated_at": time.Now().UTC()}
	if v, ok := body["enable_plugin"].(bool); ok {
		updates["enable_plugin"] = v
	}
	if v, ok := body["enable_subagent"].(bool); ok {
		updates["enable_subagent"] = v
	}
	if v, ok := body["plugin_mode"].(string); ok {
		v = strings.TrimSpace(v)
		if v != "auto" && v != "dynamic" {
			common.ReplyErr(w, "plugin_mode must be 'auto' or 'dynamic'", http.StatusBadRequest)
			return
		}
		updates["plugin_mode"] = v
	}
	if len(updates) == 1 { // only updated_at
		common.ReplyErr(w, "no valid fields to update", http.StatusBadRequest)
		return
	}

	// Upsert: insert defaults first if not present, then apply updates.
	defaults := orm.UserChatSettings{
		UserID:         userID,
		EnablePlugin:   true,
		PluginMode:     "dynamic",
		EnableSubagent: true,
		UpdatedAt:      time.Now().UTC(),
	}
	if err := db.WithContext(r.Context()).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&defaults).Error; err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := db.WithContext(r.Context()).Model(&orm.UserChatSettings{}).
		Where("user_id = ?", userID).Updates(updates).Error; err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, nil)
}
