package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/workflow/graphengine"
	workflowstore "lazymind/core/workflow/store"
)

const (
	WorkflowCallModeAuto     = "auto"
	WorkflowCallModeManual   = "manual"
	WorkflowCallModeDisabled = "disabled"
)

func normalizeWorkflowCallMode(mode string, enabled bool) string {
	switch strings.TrimSpace(mode) {
	case WorkflowCallModeAuto, WorkflowCallModeManual, WorkflowCallModeDisabled:
		return strings.TrimSpace(mode)
	default:
		if enabled {
			return WorkflowCallModeAuto
		}
		return WorkflowCallModeDisabled
	}
}

func workflowCallModeEnabled(mode string) bool {
	return mode != WorkflowCallModeDisabled
}

func workflowRefPathVar(r *http.Request) string {
	raw := strings.TrimSpace(common.PathVar(r, "workflow_ref"))
	if decoded, err := url.PathUnescape(raw); err == nil {
		return decoded
	}
	return raw
}

func DisabledBuiltinWorkflowIDs(db *gorm.DB, userID string) ([]string, error) {
	var rows []orm.UserWorkflowSetting
	if err := db.Table("user_plugin_settings ups").
		Joins("JOIN plugins p ON p.plugin_ref=ups.plugin_ref").
		Where("ups.user_id=? AND (ups.enabled=false OR ups.call_mode IN ?) AND p.source_type='builtin' AND p.owner_user_id='' AND p.status='active'", userID, []string{WorkflowCallModeManual, WorkflowCallModeDisabled}).
		Select("ups.*").Scan(&rows).Error; err != nil {
		if missingWorkflowTables(err) {
			return []string{}, nil
		}
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, strings.TrimPrefix(r.WorkflowRef, "builtin:"))
	}
	return out, nil
}

// UserWorkflowCallMode returns the effective mode for a workflow. Built-ins
// default to automatic matching; user workflows require an explicit setting.
func UserWorkflowCallMode(db *gorm.DB, userID, workflowRef string) (string, error) {
	var setting orm.UserWorkflowSetting
	err := db.Where("user_id=? AND plugin_ref=?", userID, workflowRef).First(&setting).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if strings.HasPrefix(workflowRef, "builtin:") {
			return WorkflowCallModeAuto, nil
		}
		return WorkflowCallModeDisabled, nil
	}
	if err != nil {
		return "", err
	}
	return normalizeWorkflowCallMode(setting.CallMode, setting.Enabled), nil
}

func ListUserWorkflowSettings(w http.ResponseWriter, r *http.Request) {
	userID := common.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	type row struct {
		orm.WorkflowResource
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
		callMode := WorkflowCallModeDisabled
		if v.SourceType == "builtin" && v.OwnerUserID == "" {
			callMode = WorkflowCallModeAuto
		}
		if v.CallMode != nil {
			callMode = normalizeWorkflowCallMode(*v.CallMode, v.Enabled != nil && *v.Enabled)
		} else if v.Enabled != nil {
			callMode = normalizeWorkflowCallMode("", *v.Enabled)
		}
		items = append(items, map[string]any{"workflow_ref": v.WorkflowRef, "workflow_id": v.WorkflowID, "name": v.Name, "description": v.Description, "when_to_use": v.WhenToUse, "source_type": v.SourceType, "revision_id": v.HeadRevisionID, "revision_no": v.Version, "remote_root": "remote://" + v.RelativeRoot, "enabled": workflowCallModeEnabled(callMode), "call_mode": callMode, "status": v.Status})
	}
	common.ReplyOK(w, map[string]any{"workflows": items})
}

func EnabledCatalog(db *gorm.DB, userID string) ([]map[string]any, error) {
	type row struct {
		orm.WorkflowResource
		TreeHash      string          `gorm:"column:tree_hash"`
		CompiledGraph json.RawMessage `gorm:"column:compiled_graph"`
		CallMode      string          `gorm:"column:call_mode"`
	}
	var rows []row
	err := db.Table("plugins p").Select("p.*, pr.tree_hash, pr.compiled_graph, ups.call_mode").Joins("LEFT JOIN user_plugin_settings ups ON ups.plugin_ref=p.plugin_ref AND ups.user_id=?", userID).Joins("JOIN plugin_revisions pr ON pr.id=p.head_revision_id").Where("p.status='active' AND (p.owner_user_id=? OR p.owner_user_id='') AND ((ups.enabled=true AND ups.call_mode=?) OR (ups.user_id IS NULL AND p.source_type='builtin' AND p.owner_user_id=''))", userID, WorkflowCallModeAuto).Order("p.plugin_ref").Scan(&rows).Error
	if err != nil {
		if missingWorkflowTables(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, v := range rows {
		callMode := WorkflowCallModeAuto
		if v.CallMode != "" {
			callMode = normalizeWorkflowCallMode(v.CallMode, true)
		}
		item := map[string]any{"workflow_ref": v.WorkflowRef, "workflow_id": v.WorkflowID, "name": v.Name, "description": v.Description, "when_to_use": v.WhenToUse, "source_type": v.SourceType, "remote_root": "remote://" + v.RelativeRoot, "revision_id": v.HeadRevisionID, "revision_no": v.Version, "tree_hash": v.TreeHash, "call_mode": callMode}
		var graph graphengine.CompiledStateGraph
		if json.Unmarshal(v.CompiledGraph, &graph) == nil && !graph.Runtime.IsZero() {
			item["runtime"] = graph.Runtime
		}
		out = append(out, item)
	}
	return out, nil
}

// RuntimePolicyForRevision returns the immutable runtime policy pinned by a
// Workflow session. Chat uses it to avoid applying head-revision behavior to
// an older active session.
func RuntimePolicyForRevision(ctx context.Context, db *gorm.DB, owner, refOrID, revisionID string) (graphengine.RuntimePolicy, bool) {
	pkg, err := workflowstore.New(db).GetWorkflowPackage(ctx, owner, refOrID, revisionID)
	if err != nil {
		return graphengine.RuntimePolicy{}, false
	}
	var graph graphengine.CompiledStateGraph
	if json.Unmarshal(pkg.CompiledGraph, &graph) != nil {
		return graphengine.RuntimePolicy{}, false
	}
	if graph.Runtime.IsZero() {
		return graphengine.RuntimePolicy{}, false
	}
	return graph.Runtime, true
}

func missingWorkflowTables(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "no such table") || strings.Contains(s, "does not exist")
}

func PatchUserWorkflowSetting(w http.ResponseWriter, r *http.Request) {
	userID := common.UserID(r)
	ref := workflowRefPathVar(r)
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
			callMode = WorkflowCallModeAuto
		} else {
			callMode = WorkflowCallModeDisabled
		}
	}
	if callMode != WorkflowCallModeAuto && callMode != WorkflowCallModeManual && callMode != WorkflowCallModeDisabled {
		common.ReplyErr(w, fmt.Sprintf("call_mode must be '%s', '%s' or '%s'", WorkflowCallModeAuto, WorkflowCallModeManual, WorkflowCallModeDisabled), http.StatusBadRequest)
		return
	}
	var count int64
	store.DB().Model(&orm.WorkflowResource{}).Where("plugin_ref=? AND status='active' AND (owner_user_id=? OR owner_user_id='')", ref, userID).Count(&count) // workflow-naming: persistence
	if count == 0 {
		common.ReplyErr(w, "plugin not found", http.StatusNotFound)
		return
	}
	setting := orm.UserWorkflowSetting{UserID: userID, WorkflowRef: ref, Enabled: workflowCallModeEnabled(callMode), CallMode: callMode, UpdatedAt: time.Now().UTC()}
	if err := store.DB().Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "plugin_ref"}}, DoUpdates: clause.AssignmentColumns([]string{"enabled", "call_mode", "updated_at"})}).Create(&setting).Error; err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"workflow_ref": ref, "enabled": setting.Enabled, "call_mode": callMode})
}
