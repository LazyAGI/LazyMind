package workflow

import (
	"context"
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
	"lazymind/core/workflow/graphengine"
	workflowstore "lazymind/core/workflow/store"
)

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
		Where("ups.user_id=? AND ups.enabled=false AND p.source_type='builtin' AND p.owner_user_id='' AND p.status='active'", userID).
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

func ListUserWorkflowSettings(w http.ResponseWriter, r *http.Request) {
	userID := common.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	type row struct {
		orm.WorkflowResource
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
		enabled := v.SourceType == "builtin" && v.OwnerUserID == ""
		if v.Enabled != nil {
			enabled = *v.Enabled
		}
		items = append(items, map[string]any{"workflow_ref": v.WorkflowRef, "workflow_id": v.WorkflowID, "name": v.Name, "description": v.Description, "when_to_use": v.WhenToUse, "source_type": v.SourceType, "revision_id": v.HeadRevisionID, "revision_no": v.Version, "remote_root": "remote://" + v.RelativeRoot, "enabled": enabled, "status": v.Status})
	}
	common.ReplyOK(w, map[string]any{"workflows": items})
}

func EnabledCatalog(db *gorm.DB, userID string) ([]map[string]any, error) {
	type row struct {
		orm.WorkflowResource
		TreeHash      string          `gorm:"column:tree_hash"`
		CompiledGraph json.RawMessage `gorm:"column:compiled_graph"`
	}
	var rows []row
	err := db.Table("plugins p").Select("p.*, pr.tree_hash, pr.compiled_graph").Joins("LEFT JOIN user_plugin_settings ups ON ups.plugin_ref=p.plugin_ref AND ups.user_id=?", userID).Joins("JOIN plugin_revisions pr ON pr.id=p.head_revision_id").Where("p.status='active' AND (p.owner_user_id=? OR p.owner_user_id='') AND (ups.enabled=true OR (ups.user_id IS NULL AND p.source_type='builtin' AND p.owner_user_id=''))", userID).Order("p.plugin_ref").Scan(&rows).Error
	if err != nil {
		if missingWorkflowTables(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, v := range rows {
		item := map[string]any{"workflow_ref": v.WorkflowRef, "workflow_id": v.WorkflowID, "name": v.Name, "description": v.Description, "when_to_use": v.WhenToUse, "source_type": v.SourceType, "remote_root": "remote://" + v.RelativeRoot, "revision_id": v.HeadRevisionID, "revision_no": v.Version, "tree_hash": v.TreeHash}
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
		Enabled bool `json:"enabled"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	var count int64
	store.DB().Model(&orm.WorkflowResource{}).Where("plugin_ref=? AND status='active' AND (owner_user_id=? OR owner_user_id='')", ref, userID).Count(&count) // workflow-naming: persistence
	if count == 0 {
		common.ReplyErr(w, "plugin not found", http.StatusNotFound)
		return
	}
	setting := orm.UserWorkflowSetting{UserID: userID, WorkflowRef: ref, Enabled: body.Enabled, UpdatedAt: time.Now().UTC()}
	if err := store.DB().Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "plugin_ref"}}, DoUpdates: clause.AssignmentColumns([]string{"enabled", "updated_at"})}).Create(&setting).Error; err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"workflow_ref": ref, "enabled": body.Enabled})
}
