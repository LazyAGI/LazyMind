package product

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/subagent"
	"lazymind/core/workflow/artifactfile"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/publication"
)

type productStageDefinition struct{ ID, Label, Slot, HTMLSlot, MarkdownSlot string }

var productStages = []productStageDefinition{
	{"direction", "产品方向", "direction_document", "direction_document_html", "direction_document"},
	{"competitive", "竞品与生态位", "competitive_analysis", "competitive_analysis", "competitive_analysis_markdown"},
	{"design", "产品方案", "design_document", "design_document_html", "design_document"},
	{"prd", "PRD", "prd_document", "prd_document_html", "prd_document"},
	{"prototype", "交互原型", "prototype", "prototype", "prototype_markdown"},
	{"review", "方案评审", "review_document", "review_document_html", "review_document"},
	{"handoff", "研发交付", "handoff_document", "handoff_document_html", "handoff_document"},
}

func productStageSlot(stage productStageDefinition, format string) string {
	if format == "markdown" {
		return stage.MarkdownSlot
	}
	return stage.HTMLSlot
}

func productStageMaterial(stage productStageDefinition, slot string) string {
	if slot == stage.Slot {
		return "upstream_" + stage.ID
	}
	return "upstream_" + stage.ID + "_view"
}

func productStageFormat(stage productStageDefinition, slot string) string {
	if slot == stage.MarkdownSlot {
		return "markdown"
	}
	return "html"
}

// Must match executor.safeArtifactPathPart used by UploadArtifactFile. The IDs
// here come from the persisted attempt, never from a path supplied by the model.
var productUnsafeArtifactPathPart = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func productArtifactPathPart(value string) string {
	value = productUnsafeArtifactPathPart.ReplaceAllString(strings.TrimSpace(value), "_")
	value = strings.Trim(value, "._-")
	if value == "" {
		return "unknown"
	}
	return value
}

func productPathWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && !filepath.IsAbs(relative)
}

func productArtifactMIME(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown":
		return "text/markdown"
	case ".html", ".htm":
		return "text/html"
	case ".txt":
		return "text/plain"
	case ".json":
		return "application/json"
	}
	if kind := mime.TypeByExtension(filepath.Ext(name)); kind != "" {
		return kind
	}
	return "application/octet-stream"
}

func (r *Repository) resolveProductArtifactPath(ctx context.Context, session orm.WorkflowSession, artifact Artifact, path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", repositoryError("PRODUCT_ARTIFACT_PATH_INVALID")
	}
	for _, component := range strings.Split(path, string(os.PathSeparator)) {
		if component == ".." {
			return "", repositoryError("PRODUCT_ARTIFACT_PATH_INVALID")
		}
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	// A saved editor revision is a first-class project artifact even though the
	// upload service stores its file below the owner's temporary file area. Only
	// trust the exact path persisted by the exact human revision, and still apply
	// owner-root and physical symlink containment checks before reading it.
	var revision orm.WorkflowSlotRevision
	if err := r.db.WithContext(ctx).Where("id = ? AND session_id = ? AND slot_id = ?", artifact.ID, session.ID, artifact.SlotID).First(&revision).Error; err == nil &&
		revision.HumanArtifactID != nil && revision.ChangeSource == "human" && revision.ProducerAttemptID == "" {
		var human orm.WorkflowHumanArtifact
		if err := r.db.WithContext(ctx).Where("id = ? AND session_id = ? AND slot = ?", *revision.HumanArtifactID, session.ID, artifact.Slot).First(&human).Error; err != nil {
			return "", err
		}
		raw, inlineErr := artifactfile.Inline(human.Value)
		if inlineErr != nil {
			return "", repositoryError("PRODUCT_ARTIFACT_PATH_INVALID")
		}
		var stored any
		if json.Unmarshal(raw, &stored) != nil {
			return "", repositoryError("PRODUCT_ARTIFACT_PATH_INVALID")
		}
		storedPath := ""
		switch value := stored.(type) {
		case string:
			if filepath.IsAbs(value) {
				storedPath = value
			}
		case map[string]any:
			storedPath, _ = value["path"].(string)
		}
		if storedPath == "" || filepath.Clean(storedPath) != filepath.Clean(path) {
			return "", repositoryError("PRODUCT_ARTIFACT_PATH_INVALID")
		}
		ownerRoot, absErr := filepath.Abs(doc.TempUserFilesRoot(session.CreateUserID))
		physicalRoot, rootErr := filepath.EvalSymlinks(ownerRoot)
		absolutePath, pathErr := filepath.Abs(path)
		if absErr != nil || rootErr != nil || pathErr != nil || !productPathWithin(ownerRoot, absolutePath) || !productPathWithin(physicalRoot, resolved) {
			return "", repositoryError("PRODUCT_ARTIFACT_PATH_INVALID")
		}
		relative, relErr := filepath.Rel(ownerRoot, absolutePath)
		parts := strings.Split(filepath.ToSlash(relative), "/")
		if relErr != nil || len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", repositoryError("PRODUCT_ARTIFACT_PATH_INVALID")
		}
		var upload orm.UploadSession
		if err := r.db.WithContext(ctx).Where(
			"upload_id = ? AND create_user_id = ? AND upload_state = ?", parts[0], session.CreateUserID, "UPLOADED",
		).First(&upload).Error; err != nil {
			return "", repositoryError("PRODUCT_ARTIFACT_PATH_INVALID")
		}
		var metadata struct {
			StoredName   string `json:"stored_name"`
			FileSize     int64  `json:"file_size"`
			UploadScope  string `json:"upload_scope"`
			CreateUserID string `json:"create_user_id"`
		}
		info, statErr := os.Stat(resolved)
		if json.Unmarshal(upload.Ext, &metadata) != nil || statErr != nil || !info.Mode().IsRegular() ||
			!strings.EqualFold(metadata.UploadScope, "TEMP") || metadata.StoredName != parts[1] ||
			metadata.FileSize != info.Size() || (metadata.CreateUserID != "" && metadata.CreateUserID != session.CreateUserID) {
			return "", repositoryError("PRODUCT_ARTIFACT_PATH_INVALID")
		}
		return resolved, nil
	}

	var attempt orm.WorkflowSessionStep
	if err := r.db.WithContext(ctx).Where("session_id = ? AND step_id = ? AND attempt = ?", session.ID, artifact.StepID, artifact.Attempt).First(&attempt).Error; err != nil {
		return "", err
	}
	if artifact.ProducerAttemptID != "" && artifact.ProducerAttemptID != attempt.ID {
		return "", repositoryError("PRODUCT_ARTIFACT_PATH_INVALID")
	}
	// Hosted uploads have no SubAgent task workspace. Their durable path is
	// scoped by the exact Session and persisted producer attempt instead.
	if artifact.ProducerAttemptID != "" {
		uploadRoot, absErr := filepath.Abs(doc.UploadRoot())
		physicalUploadRoot, rootErr := filepath.EvalSymlinks(uploadRoot)
		if absErr == nil && rootErr == nil {
			parts := []string{"workflow-artifacts", productArtifactPathPart(session.ID), productArtifactPathPart(attempt.ID)}
			root := filepath.Join(append([]string{uploadRoot}, parts...)...)
			expectedPhysicalRoot := filepath.Join(append([]string{physicalUploadRoot}, parts...)...)
			physicalRoot, hostedErr := filepath.EvalSymlinks(root)
			if hostedErr == nil && physicalRoot == expectedPhysicalRoot && productPathWithin(root, path) && productPathWithin(physicalRoot, resolved) {
				return resolved, nil
			}
		}
	}
	if attempt.TaskID != "" {
		root := subagent.WorkspacePath(session.CreateUserID, attempt.TaskID)
		physicalRoot, rootErr := filepath.EvalSymlinks(root)
		if rootErr == nil && productPathWithin(root, path) && productPathWithin(physicalRoot, resolved) {
			return resolved, nil
		}
	}
	return "", repositoryError("PRODUCT_ARTIFACT_PATH_INVALID")
}

type ProductRelayRequest struct {
	Action                string `json:"action"`
	SelectedStage         string `json:"selected_stage,omitempty"`
	IdempotencyKey        string `json:"idempotency_key"`
	ExpectedStateVersion  int64  `json:"expected_state_version"`
	RequestContext        string `json:"request_context,omitempty"`
	UserMessage           string `json:"user_message,omitempty"`
	AcceptCurrentArtifact bool   `json:"accept_current_artifact,omitempty"`
}

func (r *Repository) ProductRelayConversation(ctx context.Context, owner, sessionID string) string {
	if r.AuthorizeSession(ctx, sessionID, owner) != nil {
		return ""
	}
	var session orm.WorkflowSession
	if r.db.WithContext(ctx).First(&session, "id = ?", sessionID).Error != nil {
		return ""
	}
	return session.ConversationID
}

// productObject accepts native {data, caption} artifacts and legacy value/text wrappers.
func productObject(raw json.RawMessage) map[string]any {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	for i := 0; i < 4; i++ {
		switch v := value.(type) {
		case string:
			if json.Unmarshal([]byte(v), &value) != nil {
				return nil
			}
		case map[string]any:
			if len(v) <= 3 {
				if nested, ok := v["data"]; ok {
					value = nested
					continue
				}
				if nested, ok := v["value"]; ok {
					value = nested
					continue
				}
				if nested, ok := v["text"]; ok {
					value = nested
					continue
				}
			}
			return v
		default:
			return nil
		}
	}
	return nil
}

func productStageValid(stage string) bool {
	for _, item := range productStages {
		if item.ID == stage {
			return true
		}
	}
	return false
}

func productStageArtifactType(stage string) string {
	return map[string]string{
		"direction": "direction-brief", "competitive": "competitive-analysis",
		"design": "product-design-spec", "prd": "prd", "prototype": "prototype",
		"review": "review-report", "handoff": "development-handoff",
	}[stage]
}

func productRelayBindingCommand(key, material, resourceID, contentHash string, list bool) string {
	suffix := material
	if list {
		suffix += ":" + resourceID + ":" + contentHash
	}
	return "product-relay:" + requestHash([]byte(key + ":" + suffix))[:32]
}

func productCloneObject(value map[string]any) map[string]any {
	encoded, _ := json.Marshal(value)
	var clone map[string]any
	_ = json.Unmarshal(encoded, &clone)
	return clone
}

func productCurrentRegistration(workspace map[string]any, slot, sessionID string) map[string]any {
	registered, _ := workspace["artifacts"].([]any)
	var fallback map[string]any
	for _, value := range registered {
		entry, _ := value.(map[string]any)
		host, _ := entry["host_artifact"].(map[string]any)
		if host["slot"] != slot || entry["status"] == "superseded" {
			continue
		}
		if host["source_session_id"] == sessionID {
			return entry
		}
		if fallback == nil {
			fallback = entry
		}
	}
	return fallback
}

// productRegisterEditedDraft turns the exact saved editor bytes into a normal
// Workspace version before another stage consumes them. The finalized version
// remains in history, but neither its acceptance nor its checks transfer to the
// changed content. Downstream manifests are marked for revalidation transitively.
func productRegisterEditedDraft(workspace map[string]any, stage productStageDefinition, sessionID string, artifact Artifact, resource InputResource, digest string) {
	registered, _ := workspace["artifacts"].([]any)
	previous := productCurrentRegistration(workspace, artifact.SlotID, sessionID)
	if previous == nil {
		return
	}
	major, minor := 1, -1
	artifactType := productString(previous["artifact_type"])
	if artifactType == "" {
		artifactType = productStageArtifactType(stage.ID)
	}
	for _, value := range registered {
		entry, _ := value.(map[string]any)
		entryType := productString(entry["artifact_type"])
		host, _ := entry["host_artifact"].(map[string]any)
		if entryType != artifactType && host["slot"] != stage.Slot {
			continue
		}
		var entryMajor, entryMinor int
		if _, err := fmt.Sscanf(productString(entry["version"]), "%d.%d", &entryMajor, &entryMinor); err == nil &&
			(entryMajor > major || entryMajor == major && entryMinor > minor) {
			major, minor = entryMajor, entryMinor
		}
	}
	if minor < 0 {
		minor = 0
	} else {
		minor++
	}
	version := fmt.Sprintf("%d.%d", major, minor)
	draft := productCloneObject(previous)
	if draft == nil {
		draft = map[string]any{}
	}
	draftID := "artifact-" + strings.ReplaceAll(uuid.NewSHA1(uuid.NameSpaceURL, []byte(strings.Join([]string{
		productWorkspaceID(workspace, sessionID), stage.ID, version, sessionID, digest,
	}, ":"))).String(), "-", "")
	draft["artifact_id"], draft["artifact_type"], draft["title"] = draftID, artifactType, stage.Label
	draft["version"], draft["status"], draft["supersedes"] = version, "draft", previous["artifact_id"]
	draft["decisions"] = []any{}
	draft["open_questions"] = []any{map[string]any{
		"question_id": "EDIT-REVALIDATE", "blocking": true,
		"question": "该版本包含交付后的人工编辑；内容可供各阶段使用，正式确认前需重新完成本阶段核验。",
	}}
	draft["quality_notes"] = []any{"Saved human edits were registered as a shared draft without inheriting acceptance or checks."}
	draft["checks"] = map[string]any{}
	draft["implementation_readiness"] = "blocked"
	draft["host_artifact"] = map[string]any{
		"slot": artifact.SlotID, "source_session_id": sessionID, "content_sha256": digest,
		"revision_id": artifact.ID, "revision": artifact.Revision, "content_hash": resource.ContentHash,
	}
	for _, key := range []string{"accepted_by", "acceptance_ref", "acceptance_recheck_required", "assessment_record", "assessment_history"} {
		delete(draft, key)
	}
	previous["status"] = "superseded"
	changed := map[string]bool{productString(previous["artifact_id"]): true}
	seen := map[string]bool{}
	for len(changed) > 0 {
		next := map[string]bool{}
		for _, value := range registered {
			entry, _ := value.(map[string]any)
			id := productString(entry["artifact_id"])
			if id == "" || seen[id] || entry["status"] == "superseded" {
				continue
			}
			dependencies, _ := entry["dependencies"].([]any)
			for _, rawDependency := range dependencies {
				dependency, _ := rawDependency.(map[string]any)
				if !changed[productString(dependency["artifact_id"])] {
					continue
				}
				dependency["status"] = "outdated"
				entry["status"], entry["implementation_readiness"] = "needs-update", "blocked"
				next[id] = true
			}
		}
		for id := range changed {
			seen[id] = true
		}
		changed = next
	}
	registered = append(registered, draft)
	workspace["artifacts"] = registered
	versions := []any{}
	for _, value := range registered {
		entry, _ := value.(map[string]any)
		versions = append(versions, map[string]any{
			"artifact_id": entry["artifact_id"], "version": entry["version"], "dependencies": entry["dependencies"],
		})
	}
	workspace["version_dependencies"] = versions
}

func productStageList(raw any) []map[string]string {
	values, _ := raw.([]any)
	result := []map[string]string{}
	for _, value := range values {
		for _, stage := range productStages {
			if value == stage.ID {
				result = append(result, map[string]string{"id": stage.ID, "label": stage.Label})
			}
		}
	}
	return result
}

// Navigation never accepts decisions. Ending or revising the current stage is
// allowed with open decisions; carrying them to another stage requires an
// explicit accept/defer command tied to the displayed decision hash.
func productRelayDecisionGate(workspace map[string]any, action, selectedStage string) error {
	run, _ := workspace["current_run"].(map[string]any)
	if action == "finish" || (action == "switch-stage" && selectedStage == productString(run["selected_stage"])) {
		return nil
	}
	if productPendingHardStops(workspace) > 0 {
		return repositoryError("PRODUCT_RISK_CONFIRMATION_REQUIRED")
	}
	return nil
}

func productPendingHardStops(workspace map[string]any) int {
	run, _ := workspace["current_run"].(map[string]any)
	// Historical workflow revisions did not implement this control. Preserve
	// their in-progress project behavior; newly finalized stages opt in.
	if run["hard_stop_policy"] != "explicit-decision" {
		return 0
	}
	pending := 0
	for _, decision := range productPublicDecisions(workspace) {
		if decision["confirmation_required"] != true || decision["status"] == "accepted" {
			continue
		}
		// Defer is an explicit human choice to carry a draft forward, not approval
		// of the risky decision. Child assessments cannot set this server field.
		if decision["deferred"] == true {
			continue
		}
		pending++
	}
	return pending
}

func (r *Repository) productRelayState(ctx context.Context, owner, sessionID string) (orm.WorkflowSession, map[string]any, []Artifact, error) {
	if err := r.AuthorizeSession(ctx, sessionID, owner); err != nil {
		return orm.WorkflowSession{}, nil, nil, err
	}
	var session orm.WorkflowSession
	if err := r.db.WithContext(ctx).First(&session, "id = ?", sessionID).Error; err != nil {
		return session, nil, nil, err
	}
	if !supportsProject(session, r.db) {
		return session, nil, nil, repositoryError("PRODUCT_RELAY_UNSUPPORTED")
	}
	artifacts, err := r.ListArtifacts(ctx, owner, sessionID)
	if err != nil {
		return session, nil, nil, err
	}
	published, err := r.publishedProductArtifacts(ctx, owner, session)
	if err != nil {
		return session, nil, nil, err
	}
	if published != nil {
		artifacts = published
	}
	for _, artifact := range artifacts {
		if artifact.SlotID == "workspace_state" && artifact.Validity == "effective" {
			workspace := productObject(artifact.Value)
			// Large JSON artifacts are offloaded by the SubAgent runtime and the
			// persisted value becomes a trusted hosted-file descriptor.  Do not
			// mistake that descriptor for the Workspace itself: resolve the exact
			// artifact bytes through the same owner/session/path checks used by
			// product previews, then decode the JSON payload.
			if _, ok := workspace["current_run"].(map[string]any); !ok {
				if _, _, content, readErr := r.productArtifactBytes(ctx, session, artifact); readErr == nil {
					workspace = productObject(content)
				}
			}
			if _, ok := workspace["current_run"].(map[string]any); ok {
				if err := r.restoreProductDecisionEvents(ctx, owner, session, workspace); err != nil {
					return session, nil, nil, err
				}
				return session, workspace, artifacts, nil
			}
		}
	}
	// A successor has shared inputs immediately, before its first output or finalizer.
	// Reading the seed here does not make an unfinished stage relayable.
	bindings, err := r.ListInputBindings(ctx, owner, sessionID)
	if err != nil {
		return session, nil, nil, err
	}
	for _, binding := range bindings {
		if binding.MaterialID != "workspace_seed" {
			continue
		}
		resource, err := r.GetInputResource(ctx, owner, binding.ResourceID)
		if err != nil || resource.Revision != binding.ResourceRevision || resource.ContentHash != binding.ContentHash {
			continue
		}
		if workspace := productObject(resource.Content); workspace != nil {
			productReopenAutomaticDecisions(workspace)
			return session, workspace, artifacts, nil
		}
	}
	return session, nil, artifacts, nil
}

// ProductRelaySummary deliberately excludes Workspace, Manifest, paths and raw input values.
func (r *Repository) ProductRelaySummary(ctx context.Context, owner, sessionID string) (map[string]any, error) {
	session, workspace, artifacts, err := r.productRelayState(ctx, owner, sessionID)
	if err != nil {
		if err.Error() == "PRODUCT_RELAY_UNSUPPORTED" {
			return map[string]any{"supported": false, "can_relay": false, "update_available": false, "can_restart_on_latest": false}, nil
		}
		return nil, err
	}
	run, _ := workspace["current_run"].(map[string]any)
	result := map[string]any{"supported": true, "can_relay": false, "session_id": session.ID,
		"can_accept_current_artifact": false,
		"update_available":            false, "can_restart_on_latest": false,
		"state_version": session.StateVersion, "current_stage": run["selected_stage"], "run_status": run["run_status"],
		"next_stages": []map[string]string{}, "actions": []string{}, "artifacts": []map[string]any{}}
	if latest, latestErr := r.GetWorkflowPackage(ctx, owner, session.WorkflowID, ""); latestErr == nil {
		updateAvailable := latest.RevisionID != session.WorkflowRevisionID
		result["update_available"] = updateAvailable
		result["can_restart_on_latest"] = false
	}
	project, err := r.productProjectSummary(ctx, owner, session, workspace, artifacts)
	if err != nil {
		return nil, err
	}
	drafts, err := r.productDrafts(ctx, owner, session, artifacts)
	if err != nil {
		return nil, err
	}
	project["drafts"] = drafts
	result["project"] = project
	if result["current_stage"] == nil {
		result["current_stage"] = run["current_stage"]
	}
	if nextID, ok := run["next_session_id"].(string); ok && nextID != "" {
		// Only expose an owned successor. Reloading the source must not offer a second relay.
		if r.AuthorizeSession(ctx, nextID, owner) == nil {
			result["next_session_id"] = nextID
		}
	}
	for _, artifact := range artifacts {
		if artifact.SlotID != "stage_manifest" || artifact.Validity != "effective" {
			continue
		}
		manifest := productObject(artifact.Value)
		result["artifacts"] = []map[string]any{{"title": manifest["title"], "version": manifest["version"], "status": manifest["status"]}}
		result["next_stages"] = productStageList(manifest["eligible_next_stages"])
		if result["current_stage"] == nil {
			result["current_stage"] = manifest["stage"]
		}
		host, _ := manifest["host_artifact"].(map[string]any)
		if manifest["status"] == "reviewable" && host["content_sha256"] != nil {
			result["can_accept_current_artifact"] = r.productArtifactMatches(ctx, session, artifacts, host)
		}
	}
	if stage := productString(result["current_stage"]); stage != "" {
		if views, ok := project["artifacts"].([]map[string]any); ok {
			for _, view := range views {
				if view["stage"] == stage && view["available"] == true {
					result["artifacts"] = []map[string]any{{
						"title": view["title"], "version": view["version"], "status": view["status"],
					}}
					break
				}
			}
		}
	}
	if workspace == nil {
		switch session.Status {
		case "active", "waiting":
			result["reason"] = "当前阶段尚未完成，项目记录将在首次交付完成后建立。"
		case "failed", "stopped":
			result["reason"] = "当前阶段尚未完成交付，请从工作流重试或继续；已有内容会保留。"
		default:
			result["reason"] = "当前运行尚未生成可用的项目记录，已有产物仍可查看；继续迭代前需要完成一次阶段交付。"
		}
		return result, nil
	}
	if run["run_status"] != "awaiting-stage-confirmation" || session.Status != "completed" {
		result["reason"] = "当前阶段尚未完成，或已经结束接力。"
		return result, nil
	}
	if session.Dismissed {
		result["reason"] = "该阶段已归档，请从当前阶段继续。"
		return result, nil
	}
	result["can_relay"] = true
	result["actions"] = []string{"continue", "switch-stage", "finish"}
	return result, nil
}

// validateProductCommand runs inside the command transaction. Decisions cannot
// modify a stage that already has a successor; relay keeps its existing policy.
func (r *Repository) validateProductCommand(ctx context.Context, owner string, session orm.WorkflowSession, workspace map[string]any, expectedVersion int64, requireUnrelayed bool) error {
	if session.StateVersion != expectedVersion {
		return repositoryError("STATE_VERSION_CONFLICT")
	}
	run, _ := workspace["current_run"].(map[string]any)
	if workspace == nil || run["run_status"] != "awaiting-stage-confirmation" || session.Status != "completed" || session.Dismissed || (requireUnrelayed && productString(run["next_session_id"]) != "") {
		return repositoryError("PRODUCT_STAGE_NOT_READY")
	}
	if err := r.AuthorizeConversation(ctx, session.ConversationID, owner); err != nil {
		return err
	}
	var active int64
	if err := r.db.WithContext(ctx).Model(&orm.WorkflowSession{}).Where("conversation_id = ? AND dismissed = false AND status NOT IN ?", session.ConversationID, []string{"completed", "failed", "stopped"}).Count(&active).Error; err != nil {
		return err
	}
	if active > 0 {
		return ErrSessionConflict
	}
	return nil
}

func productManifest(artifacts []Artifact) (manifest map[string]any) {
	for _, artifact := range artifacts {
		if artifact.SlotID == "stage_manifest" && artifact.Validity == "effective" {
			manifest = productObject(artifact.Value)
		}
	}
	return manifest
}

func (r *Repository) productArtifactMatches(ctx context.Context, session orm.WorkflowSession, artifacts []Artifact, host map[string]any) (matched bool) {
	for _, artifact := range artifacts {
		if artifact.SlotID == host["slot"] && artifact.Validity == "effective" {
			_, _, content, err := r.productArtifactBytes(ctx, session, artifact)
			matched = err == nil && requestHash(content) == host["content_sha256"]
		}
	}
	return matched
}

// RelayProductStage uses the existing public command ledger and initialized-session path.
// The command, exact input copies, approval and next Session commit in one transaction.
func (r *Repository) RelayProductStage(ctx context.Context, owner, sessionID string, req ProductRelayRequest) (json.RawMessage, error) {
	if err := r.AuthorizeSession(ctx, sessionID, owner); err != nil {
		return nil, err
	}
	if req.IdempotencyKey == "" || len(req.IdempotencyKey) > 128 || req.ExpectedStateVersion < 1 || len([]rune(req.RequestContext)) > 8000 {
		return nil, repositoryError("INVALID_PRODUCT_RELAY")
	}
	if req.Action != "continue" && req.Action != "switch-stage" && req.Action != "finish" {
		return nil, repositoryError("INVALID_PRODUCT_RELAY")
	}
	if req.AcceptCurrentArtifact && req.Action == "finish" {
		return nil, repositoryError("INVALID_PRODUCT_RELAY")
	}
	if req.Action != "finish" && !productStageValid(req.SelectedStage) {
		return nil, repositoryError("INVALID_PRODUCT_STAGE")
	}
	body, _ := json.Marshal(map[string]any{"session_id": sessionID, "request": req})
	// Unlike delegated transitions, this command only writes through its supplied transaction.
	// Retain SQLite serialization while making command persistence atomic with the new Session.
	command, _, err := r.AtomicCommand(ctx, owner, sessionID, req.IdempotencyKey, "workflow.v1", body, func(tx *gorm.DB) (int, json.RawMessage, error) {
		txRepo := New(tx)
		session, workspace, artifacts, err := txRepo.productRelayState(ctx, owner, sessionID)
		if err != nil {
			return 0, nil, err
		}
		if err := txRepo.validateProductCommand(ctx, owner, session, workspace, req.ExpectedStateVersion, false); err != nil {
			return 0, nil, err
		}
		run, _ := workspace["current_run"].(map[string]any)
		if req.AcceptCurrentArtifact || req.Action == "continue" {
			summary, err := txRepo.ProductRelaySummary(ctx, owner, sessionID)
			if err != nil {
				return 0, nil, err
			}
			if req.AcceptCurrentArtifact && summary["can_accept_current_artifact"] != true {
				return 0, nil, repositoryError("PRODUCT_ARTIFACT_NOT_REVIEWABLE")
			}
			if req.Action == "continue" {
				allowed := false
				for _, item := range summary["next_stages"].([]map[string]string) {
					if item["id"] == req.SelectedStage {
						allowed = true
					}
				}
				if !allowed {
					return 0, nil, repositoryError("PRODUCT_STAGE_NOT_RECOMMENDED")
				}
			}
		}
		reference, source := "workflow-command:"+req.IdempotencyKey, "user-interface"
		if req.UserMessage != "" {
			var history orm.ChatHistory
			if err := tx.Where("conversation_id = ?", session.ConversationID).Order("seq DESC").First(&history).Error; err != nil {
				return 0, nil, repositoryError("PRODUCT_APPROVAL_NOT_FOUND")
			}
			if strings.TrimSpace(history.Content) != strings.TrimSpace(req.UserMessage) && strings.TrimSpace(history.RawContent) != strings.TrimSpace(req.UserMessage) {
				return 0, nil, repositoryError("PRODUCT_APPROVAL_NOT_FOUND")
			}
			source, reference = "user-message", "history:"+history.ID
		}
		now := time.Now().UTC()
		if err := productRelayDecisionGate(workspace, req.Action, req.SelectedStage); err != nil {
			return 0, nil, err
		}
		approval := map[string]any{"approval_id": uuid.NewString(), "action": req.Action, "stage": req.SelectedStage,
			"selected_stage": req.SelectedStage, "source": source, "reference": reference, "approved_by": owner,
			"approved_at": now.Format(time.RFC3339Nano), "source_session_id": sessionID,
			"request_context": strings.TrimSpace(req.RequestContext)}
		approvals, _ := workspace["approvals"].([]any)
		if approvals == nil {
			approvals, _ = workspace["approval_events"].([]any)
		}
		approvals = append(approvals, approval)
		workspace["approvals"], workspace["approval_events"] = approvals, approvals
		result := map[string]any{"source_session_id": sessionID, "approval_id": approval["approval_id"], "selected_stage": req.SelectedStage,
			"conversation_id": session.ConversationID, "workflow_id": session.WorkflowID, "workflow_revision_id": session.WorkflowRevisionID}
		if req.Action == "finish" {
			run["run_status"] = "completed"
			workspace["current_run"] = run
			if err := txRepo.saveProductWorkspace(tx, session, workspace, now); err != nil {
				return 0, nil, err
			}
			result["session_id"], result["status"], result["state_version"] = sessionID, "completed", session.StateVersion+1
		} else {
			pkg, err := txRepo.GetWorkflowPackage(ctx, owner, session.WorkflowID, session.WorkflowRevisionID)
			if err != nil {
				return 0, nil, err
			}
			var graph struct {
				MaterialProducers map[string]struct {
					Kind string `json:"kind"`
				} `json:"material_producers"`
				MaterialCardinalities map[string]string `json:"material_cardinalities"`
			}
			if json.Unmarshal(pkg.CompiledGraph, &graph) != nil || graph.MaterialProducers["workspace_seed"].Kind != "external" || graph.MaterialProducers["stage_approval"].Kind != "external" {
				return 0, nil, repositoryError("PRODUCT_RELAY_REVISION_UNSUPPORTED")
			}
			bindings, err := txRepo.ListInputBindings(ctx, owner, sessionID)
			if err != nil {
				return 0, nil, err
			}
			carried := []InputBinding{}
			singleBindings := map[string]InputBinding{}
			listSeen := map[string]bool{}
			preferences := txRepo.productStagePreferences(ctx, owner, workspace, run, bindings)
			for _, binding := range bindings {
				if graph.MaterialProducers[binding.MaterialID].Kind != "external" || binding.MaterialID == "workspace_seed" || binding.MaterialID == "stage_approval" || binding.MaterialID == "requested_stage" || productPreferenceMaterial(binding.MaterialID) {
					continue
				}
				if graph.MaterialCardinalities[binding.MaterialID] == "list" {
					identity := binding.MaterialID + "\x00" + binding.ResourceType + "\x00" + binding.ResourceID + "\x00" + binding.ContentHash
					if listSeen[identity] {
						continue
					}
					listSeen[identity] = true
					binding.ID, binding.WorkflowSessionID = "", ""
					binding.CreatedByCommandID = productRelayBindingCommand(req.IdempotencyKey, binding.MaterialID, binding.ResourceID, binding.ContentHash, true)
					carried = append(carried, binding)
					continue
				}
				if previous, ok := singleBindings[binding.MaterialID]; !ok || binding.CreatedAt.After(previous.CreatedAt) {
					singleBindings[binding.MaterialID] = binding
				}
			}
			for material, binding := range singleBindings {
				binding.ID, binding.WorkflowSessionID = "", ""
				binding.CreatedByCommandID = productRelayBindingCommand(req.IdempotencyKey, material, binding.ResourceID, binding.ContentHash, false)
				carried = append(carried, binding)
			}
			for _, binding := range txRepo.restoreProductStagePreferences(ctx, owner, req.SelectedStage, preferences) {
				if graph.MaterialProducers[binding.MaterialID].Kind == "external" {
					binding.CreatedByCommandID = "product-relay:" + req.IdempotencyKey
					carried = append(carried, binding)
				}
			}
			if req.Action == "switch-stage" && req.SelectedStage == productString(run["selected_stage"]) {
				working, err := txRepo.ListArtifacts(ctx, owner, sessionID)
				if err != nil {
					return 0, nil, err
				}
				for _, draft := range working {
					for _, stage := range productStages {
						if stage.ID != req.SelectedStage || (draft.SlotID != stage.HTMLSlot && draft.SlotID != stage.MarkdownSlot) || draft.Validity != "effective" {
							continue
						}
						for i, published := range artifacts {
							if published.SlotID == draft.SlotID {
								artifacts[i] = draft
							}
						}
					}
				}
			}
			lineage, _ := workspace["host_artifact_bindings"].([]any)
			for _, artifact := range artifacts {
				if artifact.Validity != "effective" {
					continue
				}
				for _, stage := range productStages {
					if artifact.SlotID != stage.Slot && artifact.SlotID != stage.HTMLSlot && artifact.SlotID != stage.MarkdownSlot {
						continue
					}
					material := productStageMaterial(stage, artifact.SlotID)
					if graph.MaterialProducers[material].Kind != "external" {
						return 0, nil, repositoryError("PRODUCT_RELAY_REVISION_UNSUPPORTED")
					}
					name, mimeType, content, err := txRepo.productArtifactBytes(ctx, session, artifact)
					if err != nil {
						return 0, nil, err
					}
					resource, _, err := txRepo.ImportInputResource(ctx, owner, name, mimeType, "sha256:"+requestHash(content), content)
					if err != nil {
						return 0, nil, err
					}
					filtered := carried[:0]
					for _, old := range carried {
						if old.MaterialID != material {
							filtered = append(filtered, old)
						}
					}
					carried = filtered
					carried = append(carried, InputBinding{MaterialID: material, ResourceType: "input_resource", ResourceID: resource.ID, ResourceRevision: resource.Revision, ContentHash: resource.ContentHash, CreatedByCommandID: "product-relay:" + req.IdempotencyKey})
					lineage = append(lineage, map[string]any{"material_id": material, "slot_id": artifact.SlotID, "source_session_id": sessionID, "revision_id": artifact.ID, "revision": artifact.Revision, "content_hash": resource.ContentHash, "resource_id": resource.ID})
					if entry := productCurrentRegistration(workspace, stage.Slot, sessionID); entry != nil {
						host, _ := entry["host_artifact"].(map[string]any)
						if artifact.SlotID == stage.Slot {
							if digest, _ := host["content_sha256"].(string); digest != "" && digest != requestHash(content) {
								// A later editor revision remains usable by every project stage as a
								// shared draft. Keep the finalized manifest immutable until the stage
								// is finalized again; only explicit acceptance requires an exact match.
								if req.AcceptCurrentArtifact {
									return 0, nil, repositoryError("PRODUCT_ARTIFACT_CHANGED")
								}
								productRegisterEditedDraft(workspace, stage, sessionID, artifact, resource, requestHash(content))
								break
							}
							host["revision_id"], host["revision"], host["content_hash"], host["content_sha256"] = artifact.ID, artifact.Revision, resource.ContentHash, requestHash(content)
						}
						representations, _ := entry["representations"].(map[string]any)
						if representations == nil {
							representations = map[string]any{}
							entry["representations"] = representations
						}
						representations[productStageFormat(stage, artifact.SlotID)] = map[string]any{
							"slot": artifact.SlotID, "source_session_id": sessionID,
							"revision_id": artifact.ID, "revision": artifact.Revision,
							"content_hash": resource.ContentHash, "content_sha256": requestHash(content), "present": true,
						}
					}
				}
			}
			workspace["host_artifact_bindings"] = lineage
			nextID := uuid.NewString()
			run["run_status"], run["next_session_id"] = "awaiting-stage-confirmation", nextID
			workspace["current_run"] = run
			if req.AcceptCurrentArtifact {
				var manifest map[string]any
				for _, artifact := range artifacts {
					if artifact.SlotID == "stage_manifest" && artifact.Validity == "effective" {
						manifest = productObject(artifact.Value)
					}
				}
				accepted := false
				registered, _ := workspace["artifacts"].([]any)
				for _, value := range registered {
					entry, _ := value.(map[string]any)
					if entry["artifact_id"] != manifest["artifact_id"] || entry["version"] != manifest["version"] {
						continue
					}
					acceptance := map[string]any{"approval_id": uuid.NewString(), "action": "accept-artifact", "stage": run["selected_stage"], "source": source, "reference": reference + ":accept-artifact", "approved_by": owner, "approved_at": now.Format(time.RFC3339Nano), "artifact_id": entry["artifact_id"], "version": entry["version"], "source_session_id": sessionID, "host_artifact": entry["host_artifact"]}
					entry["status"], entry["accepted_by"], entry["acceptance_ref"] = "accepted", owner, acceptance["reference"]
					approvals = append(approvals, acceptance)
					accepted = true
				}
				if !accepted {
					return 0, nil, repositoryError("PRODUCT_ARTIFACT_NOT_REGISTERED")
				}
				workspace["approvals"], workspace["approval_events"] = approvals, approvals
			}
			if err := txRepo.saveProductWorkspace(tx, session, workspace, now); err != nil {
				return 0, nil, err
			}
			workspace["current_run"] = map[string]any{"run_status": "routing", "selected_stage": req.SelectedStage, "current_stage": req.SelectedStage, "source_session_id": sessionID, "stage_approval": approval, "reference_sample": run["reference_sample"], "stage_chain": run["stage_chain"]}
			seed, _ := json.Marshal(workspace)
			approvalBytes, _ := json.Marshal(approval)
			for _, input := range []struct {
				id, mime string
				data     []byte
			}{{"workspace_seed", "application/json", seed}, {"stage_approval", "application/json", approvalBytes}, {"requested_stage", "text/plain", []byte(req.SelectedStage)}} {
				resource, _, err := txRepo.ImportInputResource(ctx, owner, input.id+".json", input.mime, "sha256:"+requestHash(input.data), input.data)
				if err != nil {
					return 0, nil, err
				}
				carried = append(carried, InputBinding{MaterialID: input.id, ResourceType: "input_resource", ResourceID: resource.ID, ResourceRevision: resource.Revision, ContentHash: resource.ContentHash, CreatedByCommandID: "product-relay:" + req.IdempotencyKey})
			}
			intent, _ := json.Marshal(map[string]any{"text": fmt.Sprintf("用户明确授权%s阶段：%s。%s", req.Action, req.SelectedStage, req.RequestContext), "source_session_id": sessionID})
			control := ControlSettings{}
			if controlstore.Controlled(session) {
				binding, err := controlstore.DecodeBinding(session)
				if err != nil {
					return 0, nil, err
				}
				control = ControlSettings{Protocol: session.ControlProtocol, BindingRequired: binding.Required, Provider: binding.Provider}
			}
			next, _, err := txRepo.CreateInitializedHostSession(ctx, owner, nextID, session.ConversationID, session.OriginHost, session.OriginRef, session.ControllerHost, pkg, session.WorkflowMode, string(intent), carried, control)
			if err != nil {
				return 0, nil, err
			}
			persisted, err := txRepo.ListInputBindings(ctx, owner, next.ID)
			if err != nil {
				return 0, nil, err
			}
			result["session_id"], result["status"], result["state_version"], result["ready_steps"] = next.ID, "prepared", next.StateVersion, initialReady(pkg.CompiledGraph, persisted)
		}
		payload, _ := json.Marshal(result)
		return http.StatusOK, payload, nil
	})
	if err != nil {
		return nil, err
	}
	return command.ResponseJSON, nil
}

func (r *Repository) saveProductWorkspace(tx *gorm.DB, session orm.WorkflowSession, workspace map[string]any, now time.Time) error {
	var previous orm.WorkflowSlotRevision
	if err := tx.Where("session_id = ? AND slot_id = 'workspace_state'", session.ID).Order("revision DESC").First(&previous).Error; err != nil {
		return err
	}
	if err := tx.Model(&orm.WorkflowSlotRevision{}).Where("session_id = ? AND slot_id = 'workspace_state' AND selected = true", session.ID).Update("selected", false).Error; err != nil {
		return err
	}
	content, _ := json.Marshal(workspace)
	row := orm.WorkflowSlotRevision{ID: uuid.NewString(), SessionID: session.ID, SlotID: "workspace_state", Slot: "workspace_state", Revision: previous.Revision + 1, Selected: true, ContentSnapshot: content, ChangeSource: "human", StepID: previous.StepID, Attempt: previous.Attempt, Validity: "effective", CreatedAt: now}
	if err := tx.Create(&row).Error; err != nil {
		return err
	}
	pub, err := publication.Latest(tx, session.ID)
	if err == nil {
		pub.Revisions["workspace_state"] = row.ID
		pub.Hashes["workspace_state"] = requestHash(content)
		if err := publication.Record(tx, session, pub); err != nil {
			return err
		}
	} else if err != gorm.ErrRecordNotFound {
		return err
	}
	return tx.Model(&orm.WorkflowSession{}).Where("id = ?", session.ID).Updates(map[string]any{"state_version": session.StateVersion + 1, "updated_at": now}).Error
}

func (r *Repository) productArtifactBytes(ctx context.Context, session orm.WorkflowSession, artifact Artifact) (string, string, []byte, error) {
	raw, err := artifactfile.Inline(artifact.Value)
	if err != nil {
		return "", "", nil, err
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return "", "", nil, repositoryError("PRODUCT_ARTIFACT_INVALID")
	}
	path, text := "", ""
	switch v := value.(type) {
	case string:
		if filepath.IsAbs(v) {
			path = v
		} else {
			text = v
		}
	case map[string]any:
		if encoded, ok := v["content_base64"].(string); ok {
			if len(encoded) > base64.StdEncoding.EncodedLen(20<<20) {
				return "", "", nil, repositoryError("PRODUCT_ARTIFACT_TOO_LARGE")
			}
			content, err := base64.StdEncoding.DecodeString(encoded)
			name, _ := v["name"].(string)
			return name, productArtifactMIME(name), content, err
		}
		path, _ = v["path"].(string)
		text, _ = v["text"].(string)
	}
	if path != "" {
		resolved, err := r.resolveProductArtifactPath(ctx, session, artifact, path)
		if err != nil {
			return "", "", nil, err
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return "", "", nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > 20<<20 {
			return "", "", nil, repositoryError("PRODUCT_ARTIFACT_TOO_LARGE")
		}
		content, err := doc.ReadLocalArtifactFile(path, 20<<20)
		return filepath.Base(path), productArtifactMIME(path), content, err
	}
	if text != "" {
		kind := strings.TrimSpace(artifact.ContentType)
		if kind == "" || kind == "json" || kind == "application/json" {
			kind = "text/markdown"
		}
		extension := ".md"
		if strings.Contains(strings.ToLower(kind), "html") {
			extension = ".html"
		} else if strings.Contains(strings.ToLower(kind), "plain") {
			extension = ".txt"
		}
		return artifact.SlotID + extension, kind, []byte(text), nil
	}
	if len(raw) == 0 {
		return "", "", nil, repositoryError("PRODUCT_ARTIFACT_INVALID")
	}
	return artifact.SlotID + ".json", "application/json", raw, nil
}
