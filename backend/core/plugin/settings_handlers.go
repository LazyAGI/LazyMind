package plugin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

const (
	PluginCallModeAuto     = "auto"
	PluginCallModeManual   = "manual"
	PluginCallModeDisabled = "disabled"
)

func normalizePluginCallMode(mode string, enabled bool) string {
	switch strings.TrimSpace(mode) {
	case PluginCallModeAuto, PluginCallModeManual, PluginCallModeDisabled:
		return strings.TrimSpace(mode)
	default:
		if enabled {
			return PluginCallModeAuto
		}
		return PluginCallModeDisabled
	}
}

func pluginCallModeEnabled(mode string) bool {
	return mode != PluginCallModeDisabled
}

func pluginRefPathVar(r *http.Request) string {
	raw := strings.TrimSpace(mux.Vars(r)["plugin_ref"])
	if decoded, err := url.PathUnescape(raw); err == nil {
		return decoded
	}
	return raw
}

func pluginSettingRefPathVar(r *http.Request) string {
	ref := pluginRefPathVar(r)
	if strings.HasPrefix(ref, "builtin/") {
		return "builtin:" + strings.TrimPrefix(ref, "builtin/")
	}
	return ref
}

func DisabledBuiltinPluginIDs(db *gorm.DB, userID string) ([]string, error) {
	var rows []orm.UserPluginSetting
	if err := db.Where("user_id=? AND (enabled=false OR call_mode=?) AND plugin_ref LIKE 'builtin:%'", userID, PluginCallModeDisabled).Find(&rows).Error; err != nil {
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

func ManualBuiltinPluginIDs(db *gorm.DB, userID string) ([]string, error) {
	var rows []orm.UserPluginSetting
	if err := db.Where("user_id=? AND call_mode=? AND plugin_ref LIKE 'builtin:%'", userID, PluginCallModeManual).Find(&rows).Error; err != nil {
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

// UserPluginCallMode returns the effective mode for a plugin. Built-ins default
// to auto for users without an explicit setting; unpublished or unconfigured
// user plugins are not callable.
func UserPluginCallMode(db *gorm.DB, userID, pluginRef string) (string, error) {
	var setting orm.UserPluginSetting
	err := db.Where("user_id=? AND plugin_ref=?", userID, pluginRef).First(&setting).Error
	if err == gorm.ErrRecordNotFound {
		if strings.HasPrefix(pluginRef, "builtin:") {
			return PluginCallModeAuto, nil
		}
		return PluginCallModeDisabled, nil
	}
	if err != nil {
		return "", err
	}
	return normalizePluginCallMode(setting.CallMode, setting.Enabled), nil
}

func ListUserPluginSettings(w http.ResponseWriter, r *http.Request) {
	userID := common.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	type row struct {
		orm.PluginResource
		Enabled  *bool   `gorm:"column:enabled"`
		CallMode *string `gorm:"column:call_mode"`
	}
	var rows []row
	err := store.DB().Table("plugins p").Select("p.*, ups.enabled, ups.call_mode").Joins("LEFT JOIN user_plugin_settings ups ON ups.plugin_ref=p.plugin_ref AND ups.user_id=?", userID).Where("p.status = 'active' AND (p.owner_user_id = ? OR p.owner_user_id = '')", userID).Order("p.name ASC").Scan(&rows).Error
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
		callMode := PluginCallModeDisabled
		if v.CallMode != nil {
			callMode = normalizePluginCallMode(*v.CallMode, enabled)
		} else if enabled {
			callMode = PluginCallModeAuto
		}
		items = append(items, map[string]any{"plugin_ref": v.PluginRef, "plugin_id": v.PluginID, "name": v.Name, "description": v.Description, "when_to_use": v.WhenToUse, "source_type": v.SourceType, "revision_id": v.HeadRevisionID, "revision_no": v.Version, "remote_root": "remote://" + v.RelativeRoot, "enabled": enabled, "call_mode": callMode, "status": v.Status})
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
				values := map[string]orm.UserPluginSetting{}
				for _, s := range settings {
					values[s.PluginRef] = s
				}
				for _, b := range payload.Plugins {
					ref := "builtin:" + b.ID
					enabled := true
					callMode := PluginCallModeAuto
					if setting, ok := values[ref]; ok {
						enabled = setting.Enabled
						callMode = normalizePluginCallMode(setting.CallMode, enabled)
					}
					items = append(items, map[string]any{"plugin_ref": ref, "plugin_id": b.ID, "name": b.Name, "description": b.Description, "source_type": "builtin", "enabled": enabled, "call_mode": callMode, "status": "active"})
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
		CallMode string `gorm:"column:call_mode"`
	}
	var rows []row
	err := db.Table("plugins p").Select("p.*, pr.tree_hash, ups.call_mode").Joins("JOIN user_plugin_settings ups ON ups.plugin_ref=p.plugin_ref AND ups.user_id=? AND ups.enabled=true AND ups.call_mode<>?", userID, PluginCallModeDisabled).Joins("JOIN plugin_revisions pr ON pr.id=p.head_revision_id").Where("p.status='active' AND (p.owner_user_id=? OR p.owner_user_id='')", userID).Order("p.plugin_ref").Scan(&rows).Error
	if err != nil {
		if missingPluginTables(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, v := range rows {
		out = append(out, map[string]any{"plugin_ref": v.PluginRef, "plugin_id": v.PluginID, "name": v.Name, "description": v.Description, "when_to_use": v.WhenToUse, "source_type": v.SourceType, "remote_root": "remote://" + v.RelativeRoot, "revision_id": v.HeadRevisionID, "revision_no": v.Version, "tree_hash": v.TreeHash, "call_mode": normalizePluginCallMode(v.CallMode, true)})
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
	ref := pluginSettingRefPathVar(r)
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body struct {
		Enabled  *bool  `json:"enabled"`
		CallMode string `json:"call_mode"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	callMode := strings.TrimSpace(body.CallMode)
	if callMode == "" && body.Enabled != nil {
		if *body.Enabled {
			callMode = PluginCallModeAuto
		} else {
			callMode = PluginCallModeDisabled
		}
	}
	if callMode != PluginCallModeAuto && callMode != PluginCallModeManual && callMode != PluginCallModeDisabled {
		common.ReplyErr(w, fmt.Sprintf("call_mode must be '%s', '%s' or '%s'", PluginCallModeAuto, PluginCallModeManual, PluginCallModeDisabled), http.StatusBadRequest)
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
	setting := orm.UserPluginSetting{UserID: userID, PluginRef: ref, Enabled: pluginCallModeEnabled(callMode), CallMode: callMode, UpdatedAt: time.Now().UTC()}
	if err := store.DB().Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "plugin_ref"}}, DoUpdates: clause.AssignmentColumns([]string{"enabled", "call_mode", "updated_at"})}).Create(&setting).Error; err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"plugin_ref": ref, "enabled": setting.Enabled, "call_mode": callMode})
}
