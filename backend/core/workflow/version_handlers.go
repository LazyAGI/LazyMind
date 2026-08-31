package workflow

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func ownedWorkflowResource(r *http.Request) (orm.WorkflowResource, bool) {
	var p orm.WorkflowResource
	err := store.DB().Where("plugin_ref=? AND owner_user_id=?", workflowRefPathVar(r), common.UserID(r)).First(&p).Error
	return p, err == nil
}

func ListWorkflowVersions(w http.ResponseWriter, r *http.Request) {
	p, ok := ownedWorkflowResource(r)
	if !ok {
		common.ReplyErr(w, "plugin not found", http.StatusNotFound)
		return
	}
	var rows []orm.WorkflowRevision
	if err := store.DB().Where("plugin_resource_id=?", p.ID).Order("revision_no DESC").Find(&rows).Error; err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, v := range rows {
		items = append(items, map[string]any{"revision_id": v.ID, "revision_no": v.RevisionNo, "tree_hash": v.TreeHash, "message": v.Message, "created_by": v.CreatedBy, "created_at": v.CreatedAt, "current": v.ID == p.HeadRevisionID})
	}
	common.ReplyOK(w, map[string]any{"workflow_ref": p.WorkflowRef, "current_revision_id": p.HeadRevisionID, "current_revision_no": p.Version, "versions": items})
}

func revisionFiles(p orm.WorkflowResource, revisionID string) (map[string]string, error) {
	var rev orm.WorkflowRevision
	if err := store.DB().Where("id=? AND plugin_resource_id=?", revisionID, p.ID).First(&rev).Error; err != nil {
		return nil, err
	}
	var entries []orm.WorkflowRevisionEntry
	if err := store.DB().Where("revision_id=?", revisionID).Find(&entries).Error; err != nil {
		return nil, err
	}
	files := map[string]string{}
	for _, e := range entries {
		if e.BlobHash == nil {
			continue
		}
		var b orm.WorkflowBlob
		if err := store.DB().Where("hash=?", *e.BlobHash).First(&b).Error; err != nil {
			return nil, err
		}
		files[e.Path] = string(b.Content)
	}
	return files, nil
}

func workflowVersionPayload(p orm.WorkflowResource, revisionID string) (map[string]any, error) {
	files, err := revisionFiles(p, revisionID)
	if err != nil {
		return nil, err
	}
	scripts := map[string]string{}
	keys := make([]string, 0)
	for path, body := range files {
		if strings.HasPrefix(path, "scripts/") {
			scripts[path] = body
			keys = append(keys, path)
		}
	}
	sort.Strings(keys)
	scriptsJSON, _ := json.Marshal(scripts)
	var rev orm.WorkflowRevision
	_ = store.DB().Where("id=?", revisionID).First(&rev).Error
	workflowYAML := files["workflow.yaml"]
	if workflowYAML == "" {
		workflowYAML = files["workflow.yaml"]
	}
	return map[string]any{"workflow_ref": p.WorkflowRef, "revision_id": rev.ID, "revision_no": rev.RevisionNo, "tree_hash": rev.TreeHash, "workflow_yaml_content": workflowYAML, "state_yaml_content": files["scenario/state.yml"], "scenario_content": files["scenario/scenario.md"], "driver_content": files["scenario/driver.md"], "scripts_content": string(scriptsJSON), "readonly": true}, nil
}

func GetWorkflowVersion(w http.ResponseWriter, r *http.Request) {
	p, ok := ownedWorkflowResource(r)
	if !ok {
		common.ReplyErr(w, "plugin not found", http.StatusNotFound)
		return
	}
	payload, err := workflowVersionPayload(p, common.PathVar(r, "revision_id"))
	if err != nil {
		common.ReplyErr(w, "version not found", http.StatusNotFound)
		return
	}
	common.ReplyOK(w, payload)
}

func ReplaceDraftFromWorkflowVersion(w http.ResponseWriter, r *http.Request) {
	p, ok := ownedWorkflowResource(r)
	if !ok {
		common.ReplyErr(w, "plugin not found", http.StatusNotFound)
		return
	}
	files, err := revisionFiles(p, common.PathVar(r, "revision_id"))
	if err != nil {
		common.ReplyErr(w, "version not found", http.StatusNotFound)
		return
	}
	var draft orm.WorkflowDraft
	if err := store.DB().Where("created_by=? AND plugin_id=? AND deleted_at IS NULL", common.UserID(r), p.WorkflowID).First(&draft).Error; err != nil {
		common.ReplyErr(w, "plugin draft not found", http.StatusNotFound)
		return
	}
	scripts := map[string]string{}
	for path, body := range files {
		if strings.HasPrefix(path, "scripts/") {
			scripts[path] = body
		}
	}
	scriptsJSON, _ := json.Marshal(scripts)
	workflowYAML := files["workflow.yaml"]
	if workflowYAML == "" {
		workflowYAML = files["workflow.yaml"]
	}
	updates := map[string]any{"state_yaml_content": files["scenario/state.yml"], "scenario_content": files["scenario/scenario.md"], "driver_content": files["scenario/driver.md"], "scripts_content": string(scriptsJSON), "state_layout_content": "", "base_revision_id": common.PathVar(r, "revision_id"), "version": draft.Version + 1, "updated_at": time.Now().UTC()}
	setWorkflowYAMLUpdate(updates, workflowYAML)
	if err := store.DB().Model(&draft).Updates(updates).Error; err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = store.DB().Where("id=? AND deleted_at IS NULL", draft.ID).First(&draft).Error
	common.ReplyOK(w, toEnrichedDraftResponse(store.DB(), draft))
}
