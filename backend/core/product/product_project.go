package product

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"lazymind/core/common/orm"
)

// Product project views are projections of existing versioned outputs and immutable
// input bindings. They do not create a second document store or a second conversation.
type productProjectView struct {
	metadata map[string]any
	content  []byte
	mimeType string
}

func productString(value any) string { text, _ := value.(string); return text }

func productWorkspaceID(workspace map[string]any, sessionID string) string {
	if id := productString(workspace["workspace_id"]); id != "" {
		return id
	}
	return "workspace-" + strings.ReplaceAll(uuid.NewSHA1(uuid.NameSpaceURL, []byte(sessionID)).String(), "-", "")
}

func productBusinessRecords(raw any, keys ...string) []map[string]any {
	items, _ := raw.([]any)
	result := []map[string]any{}
	latest := map[string]int{}
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, map[string]any{"question": text})
			continue
		}
		object, _ := item.(map[string]any)
		entry := map[string]any{}
		for _, key := range keys {
			switch value := object[key].(type) {
			case string, bool:
				entry[key] = value
			}
		}
		if len(entry) > 0 {
			id := productString(entry["decision_id"])
			if id != "" {
				if index, exists := latest[id]; exists {
					result[index] = entry
					continue
				}
				latest[id] = len(result)
			}
			result = append(result, entry)
		}
	}
	filtered := result[:0]
	for _, entry := range result {
		if entry["status"] != "superseded" {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func productRegisteredView(workspace map[string]any, slot string) map[string]any {
	entries, _ := workspace["artifacts"].([]any)
	var selected map[string]any
	var major, minor int
	for _, value := range entries {
		entry, _ := value.(map[string]any)
		host, _ := entry["host_artifact"].(map[string]any)
		if host["slot"] != slot || entry["status"] == "superseded" {
			continue
		}
		var entryMajor, entryMinor int
		_, _ = fmt.Sscanf(productString(entry["version"]), "%d.%d", &entryMajor, &entryMinor)
		if selected == nil || entryMajor > major || entryMajor == major && entryMinor >= minor {
			selected, major, minor = entry, entryMajor, entryMinor
		}
	}
	return selected
}

func (r *Repository) productProjectViews(ctx context.Context, owner string, session orm.WorkflowSession, workspace map[string]any, artifacts []Artifact, requested ...string) ([]productProjectView, error) {
	bindings, err := r.ListInputBindings(ctx, owner, session.ID)
	if err != nil {
		return nil, err
	}
	workspaceID := productWorkspaceID(workspace, session.ID)
	lineage, _ := workspace["host_artifact_bindings"].([]any)
	format := "html"
	if len(requested) > 0 && requested[0] == "markdown" {
		format = "markdown"
	}
	result := []productProjectView{}
	for _, stage := range productStages {
		registered := productRegisteredView(workspace, stage.Slot)
		targetSlot := productStageSlot(stage, format)
		availableSlot := func(slot string) bool {
			for _, artifact := range artifacts {
				if artifact.SlotID == slot && artifact.Validity == "effective" {
					return true
				}
			}
			material := productStageMaterial(stage, slot)
			for _, binding := range bindings {
				if binding.MaterialID == material && binding.ResourceType == "input_resource" {
					return true
				}
			}
			return false
		}
		availableFormats := []string{}
		if availableSlot(stage.HTMLSlot) {
			availableFormats = append(availableFormats, "html")
		}
		if availableSlot(stage.MarkdownSlot) {
			availableFormats = append(availableFormats, "markdown")
		}
		actualFormat := format
		// Existing projects created before dual-format publication remain readable.
		if !availableSlot(targetSlot) && availableSlot(stage.Slot) {
			targetSlot, actualFormat = stage.Slot, productStageFormat(stage, stage.Slot)
		}
		meta := map[string]any{"artifact_id": workspaceID + ":" + stage.ID, "stage": stage.ID,
			"title": stage.Label, "version": "", "status": "draft", "source_session_id": "",
			"slot_id": targetSlot, "slot_key": targetSlot, "revision_id": "", "revision": 0,
			"available": false, "editable": false, "stale": false, "content_format": actualFormat,
			"available_formats": availableFormats}
		var content []byte
		mimeType := "text/markdown"
		found := false
		// Current output wins over the inherited baseline, including a draft which has
		// not reached the finalizer yet. Acceptance never transfers to changed bytes.
		for _, artifact := range artifacts {
			if artifact.SlotID != targetSlot || artifact.Validity != "effective" {
				continue
			}
			found = true
			meta["source_session_id"], meta["revision_id"], meta["revision"] = session.ID, artifact.ID, artifact.Revision
			_, kind, data, readErr := r.productArtifactBytes(ctx, session, artifact)
			if readErr == nil {
				content, mimeType = data, kind
			} else {
				meta["reason"] = "当前产物暂时无法读取，请检查本阶段输出。"
			}
		}
		if !found {
			for _, binding := range bindings {
				if binding.MaterialID != productStageMaterial(stage, targetSlot) || binding.ResourceType != "input_resource" {
					continue
				}
				found = true
				resource, readErr := r.GetInputResource(ctx, owner, binding.ResourceID)
				if readErr != nil || resource.Revision != binding.ResourceRevision || resource.ContentHash != binding.ContentHash || len(resource.Content) > 20<<20 {
					meta["reason"] = "共享版本的输入绑定已失效，请检查该阶段的版本。"
					continue
				}
				content, mimeType = resource.Content, resource.MimeType
				for index := len(lineage) - 1; index >= 0; index-- {
					entry, _ := lineage[index].(map[string]any)
					if entry["material_id"] != binding.MaterialID || entry["resource_id"] != resource.ID || entry["content_hash"] != resource.ContentHash {
						continue
					}
					// Lineage is not an authorization grant. Validate both the source owner
					// and its conversation before exposing even revision identifiers.
					var source orm.WorkflowSession
					sourceID := productString(entry["source_session_id"])
					if r.AuthorizeSession(ctx, sourceID, owner) != nil || r.db.WithContext(ctx).Where("id = ? AND conversation_id = ?", sourceID, session.ConversationID).First(&source).Error != nil {
						break
					}
					artifact, readErr := r.ReadArtifact(ctx, owner, productString(entry["revision_id"]))
					if readErr != nil || artifact.SessionID != sourceID || artifact.SlotID != targetSlot {
						break
					}
					meta["source_session_id"], meta["revision_id"], meta["revision"] = sourceID, artifact.ID, artifact.Revision
					if !artifact.Selected || artifact.Validity != "effective" {
						meta["stale"], meta["reason"] = true, "源阶段已有更新；此处保留当前项目绑定的版本，不会静默替换。"
					}
					break
				}
			}
		}
		if !found && registered == nil {
			continue
		}
		meta["available"] = len(content) > 0 && len(content) <= 20<<20 && utf8.Valid(content)
		if len(content) > 0 {
			meta["content_hash"] = "sha256:" + requestHash(content)
		}
		if meta["available"] != true && meta["reason"] == nil {
			meta["reason"] = "该版本尚未绑定可读取的文本或原型。"
		}
		run, _ := workspace["current_run"].(map[string]any)
		currentStageRunning := session.Status == "active" && productString(run["selected_stage"]) == stage.ID
		meta["editable"] = actualFormat == "markdown" && meta["available"] == true && productString(meta["revision_id"]) != "" && !currentStageRunning
		if registered != nil {
			meta["title"], meta["version"], meta["version_id"] = registered["title"], registered["version"], registered["artifact_id"]
			host, _ := registered["host_artifact"].(map[string]any)
			representation := host
			if values, ok := registered["representations"].(map[string]any); ok {
				if selected, ok := values[actualFormat].(map[string]any); ok {
					representation = selected
				}
			}
			if len(content) > 0 && representation["content_sha256"] == requestHash(content) {
				meta["status"] = registered["status"]
			} else if len(content) > 0 {
				meta["stale"], meta["reason"] = true, "内容已有修改，当前为待核验草稿；已接受的历史版本仍保留。"
				meta["version"] = fmt.Sprintf("working-r%v", meta["revision"])
			}
			if registered["status"] == "needs-update" {
				meta["stale"], meta["reason"] = true, "上游产物已更新，此视图需要同步核验。"
			}
		} else if found {
			meta["version"] = fmt.Sprintf("working-r%v", meta["revision"])
		}
		result = append(result, productProjectView{metadata: meta, content: content, mimeType: mimeType})
	}
	return result, nil
}

func (r *Repository) productProjectSummary(ctx context.Context, owner string, session orm.WorkflowSession, workspace map[string]any, artifacts []Artifact) (map[string]any, error) {
	views, err := r.productProjectViews(ctx, owner, session, workspace, artifacts)
	if err != nil {
		return nil, err
	}
	// The HTML tab is the default projection, but a newer Markdown companion is
	// still a change to the same logical product artifact. Surface that draft on
	// the project card instead of incorrectly presenting the old HTML as accepted.
	markdownViews, err := r.productProjectViews(ctx, owner, session, workspace, artifacts, "markdown")
	if err != nil {
		return nil, err
	}
	markdownByStage := map[string]productProjectView{}
	for _, view := range markdownViews {
		markdownByStage[productString(view.metadata["stage"])] = view
	}
	public := []map[string]any{}
	questions := []any{}
	for _, stage := range productStages {
		object := productRegisteredView(workspace, stage.Slot)
		items, _ := object["open_questions"].([]any)
		questions = append(questions, items...)
	}
	for _, view := range views {
		meta := view.metadata
		if markdown, ok := markdownByStage[productString(meta["stage"])]; ok && markdown.metadata["stale"] == true {
			meta["stale"], meta["status"] = true, "draft"
			meta["reason"] = "Markdown 内容已有修改；当前逻辑产物需要重新核验。"
		}
		public = append(public, meta)
	}
	workspaceID := productWorkspaceID(workspace, session.ID)
	run, _ := workspace["current_run"].(map[string]any)
	return map[string]any{"workspace_id": workspaceID, "conversation_id": session.ConversationID,
		"current_session_id": session.ID, "name": productString(workspace["name"]), "artifacts": public,
		"can_update_decisions": workspace != nil && session.Status == "completed" && !session.Dismissed && run["run_status"] == "awaiting-stage-confirmation" && productString(run["next_session_id"]) == "",
		"decisions":            productPublicDecisions(workspace),
		"open_questions":       productBusinessRecords(questions, "question_id", "question", "title", "description", "blocking", "owner", "handling", "status")}, nil
}

func (r *Repository) ProductProjectArtifact(ctx context.Context, owner, sessionID, stage string, requested ...string) (map[string]any, error) {
	if !productStageValid(stage) {
		return nil, repositoryError("INVALID_PRODUCT_STAGE")
	}
	session, workspace, artifacts, err := r.productRelayState(ctx, owner, sessionID)
	if err != nil {
		return nil, err
	}
	views, err := r.productProjectViews(ctx, owner, session, workspace, artifacts, requested...)
	if err != nil {
		return nil, err
	}
	for _, view := range views {
		if view.metadata["stage"] != stage || view.metadata["available"] != true {
			continue
		}
		format := "markdown"
		trimmed := strings.ToLower(strings.TrimSpace(string(view.content)))
		if strings.Contains(view.mimeType, "html") || strings.HasPrefix(trimmed, "<!doctype html") || strings.HasPrefix(trimmed, "<html") {
			format = "html"
		} else if strings.Contains(view.mimeType, "json") {
			format = "text"
		}
		view.metadata["content"], view.metadata["content_format"], view.metadata["mime_type"] = string(view.content), format, view.mimeType
		return view.metadata, nil
	}
	return nil, ErrNotFound
}

func productPreferenceMaterial(material string) bool {
	return material == "execution_depth" || material == "word_target" || material == "reference_sample" || material == "reference_sample_choice"
}

// Preferences belong to a stage/artifact type, never all future documents. Store
// only owner-scoped immutable resource references and revalidate them on restore.
func (r *Repository) productStagePreferences(ctx context.Context, owner string, workspace, run map[string]any, bindings []InputBinding) map[string]any {
	preferences, _ := workspace["stage_preferences"].(map[string]any)
	if preferences == nil {
		preferences = map[string]any{}
	}
	current := productString(run["selected_stage"])
	if !productStageValid(current) {
		current = productString(run["current_stage"])
	}
	if productStageValid(current) {
		stage := map[string]any{}
		for _, binding := range bindings {
			if !productPreferenceMaterial(binding.MaterialID) {
				continue
			}
			resource, err := r.GetInputResource(ctx, owner, binding.ResourceID)
			if err == nil && resource.Revision == binding.ResourceRevision && resource.ContentHash == binding.ContentHash {
				stage[binding.MaterialID] = map[string]any{"resource_id": resource.ID, "content_hash": resource.ContentHash}
			}
		}
		preferences[current] = stage
	}
	workspace["stage_preferences"] = preferences
	return preferences
}

func (r *Repository) restoreProductStagePreferences(ctx context.Context, owner, stage string, preferences map[string]any) []InputBinding {
	entry, _ := preferences[stage].(map[string]any)
	result := []InputBinding{}
	for _, material := range []string{"execution_depth", "word_target", "reference_sample_choice", "reference_sample"} {
		value, _ := entry[material].(map[string]any)
		resourceID := productString(value["resource_id"])
		if resourceID == "" {
			continue
		}
		resource, err := r.GetInputResource(ctx, owner, resourceID)
		if err != nil || resource.ContentHash != value["content_hash"] {
			continue
		}
		result = append(result, InputBinding{MaterialID: material, ResourceType: "input_resource", ResourceID: resource.ID, ResourceRevision: resource.Revision, ContentHash: resource.ContentHash})
	}
	return result
}
