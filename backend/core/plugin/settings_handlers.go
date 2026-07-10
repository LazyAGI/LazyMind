package plugin

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func pluginRefPathVar(r *http.Request) string {
	raw := strings.TrimSpace(common.PathVar(r, "plugin_ref"))
	if decoded, err := url.PathUnescape(raw); err == nil {
		return decoded
	}
	return raw
}

func DisabledBuiltinPluginIDs(db *gorm.DB, userID string) ([]string, error) {
	var rows []orm.UserPluginSetting
	if err := db.Where("user_id=? AND enabled=false AND plugin_ref LIKE 'builtin:%'", userID).Find(&rows).Error; err != nil {
		if missingPluginTables(err) {
			return []string{}, nil
		}
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, strings.TrimPrefix(r.PluginRef, "builtin:"))
	}
	return out, nil
}

func ListUserPluginSettings(w http.ResponseWriter, r *http.Request) {
	userID := common.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	type row struct {
		orm.PluginResource
		Enabled *bool `gorm:"column:enabled"`
	}
	var rows []row
	err := store.DB().Table("plugins p").Select("p.*, ups.enabled").Joins("LEFT JOIN user_plugin_settings ups ON ups.plugin_ref=p.plugin_ref AND ups.user_id=?", userID).Where("p.status = 'active' AND (p.owner_user_id = ? OR p.owner_user_id = '')", userID).Order("p.name ASC").Scan(&rows).Error
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, v := range rows {
		enabled := false
		if v.Enabled != nil {
			enabled = *v.Enabled
		}
		items = append(items, map[string]any{"plugin_ref": v.PluginRef, "plugin_id": v.PluginID, "name": v.Name, "description": v.Description, "when_to_use": v.WhenToUse, "source_type": v.SourceType, "revision_id": v.HeadRevisionID, "revision_no": v.Version, "remote_root": "remote://" + v.RelativeRoot, "enabled": enabled, "status": v.Status})
	}
	if req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, common.ChatServiceEndpoint()+"/api/plugins", nil); err == nil {
		if resp, err := http.DefaultClient.Do(req); err == nil {
			defer resp.Body.Close()
			var payload struct {
				Plugins []struct {
					ID          string `json:"id"`
					Name        string `json:"name"`
					Description string `json:"description"`
				} `json:"plugins"`
			}
			if resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&payload) == nil {
				var settings []orm.UserPluginSetting
				_ = store.DB().Where("user_id=? AND plugin_ref LIKE 'builtin:%'", userID).Find(&settings).Error
				values := map[string]bool{}
				for _, s := range settings {
					values[s.PluginRef] = s.Enabled
				}
				for _, b := range payload.Plugins {
					ref := "builtin:" + b.ID
					enabled, exists := values[ref]
					if !exists {
						enabled = true
					}
					items = append(items, map[string]any{"plugin_ref": ref, "plugin_id": b.ID, "name": b.Name, "description": b.Description, "source_type": "builtin", "enabled": enabled, "status": "active"})
				}
			}
		}
	}
	common.ReplyOK(w, map[string]any{"plugins": items})
}

func EnabledCatalog(db *gorm.DB, userID string) ([]map[string]any, error) {
	type row struct {
		orm.PluginResource
		TreeHash string `gorm:"column:tree_hash"`
	}
	var rows []row
	err := db.Table("plugins p").Select("p.*, pr.tree_hash").Joins("JOIN user_plugin_settings ups ON ups.plugin_ref=p.plugin_ref AND ups.user_id=? AND ups.enabled=true", userID).Joins("JOIN plugin_revisions pr ON pr.id=p.head_revision_id").Where("p.status='active' AND (p.owner_user_id=? OR p.owner_user_id='')", userID).Order("p.plugin_ref").Scan(&rows).Error
	if err != nil {
		if missingPluginTables(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, v := range rows {
		out = append(out, map[string]any{"plugin_ref": v.PluginRef, "plugin_id": v.PluginID, "name": v.Name, "description": v.Description, "when_to_use": v.WhenToUse, "source_type": v.SourceType, "remote_root": "remote://" + v.RelativeRoot, "revision_id": v.HeadRevisionID, "revision_no": v.Version, "tree_hash": v.TreeHash})
	}
	return out, nil
}

func missingPluginTables(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "no such table") || strings.Contains(s, "does not exist")
}

func PatchUserPluginSetting(w http.ResponseWriter, r *http.Request) {
	userID := common.UserID(r)
	ref := pluginRefPathVar(r)
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	var count int64
	if strings.HasPrefix(ref, "builtin:") {
		count = 1
	} else {
		store.DB().Model(&orm.PluginResource{}).Where("plugin_ref=? AND status='active' AND (owner_user_id=? OR owner_user_id='')", ref, userID).Count(&count)
	}
	if count == 0 {
		common.ReplyErr(w, "plugin not found", http.StatusNotFound)
		return
	}
	setting := orm.UserPluginSetting{UserID: userID, PluginRef: ref, Enabled: body.Enabled, UpdatedAt: time.Now().UTC()}
	if err := store.DB().Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "plugin_ref"}}, DoUpdates: clause.AssignmentColumns([]string{"enabled", "updated_at"})}).Create(&setting).Error; err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"plugin_ref": ref, "enabled": body.Enabled})
}
