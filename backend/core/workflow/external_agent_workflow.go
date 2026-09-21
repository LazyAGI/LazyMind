package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/workflow/graphengine"
	workflowstore "lazymind/core/workflow/store"
)

const (
	externalTaskStatusQueued            = "queued"
	externalTaskStatusConverting        = "converting"
	externalTaskStatusRunning           = "running"
	externalTaskStatusWaitingUserAction = "waiting_user_action"
	externalTaskStatusSucceeded         = "succeeded"
	externalTaskStatusFailed            = "failed"
)

var allowedExternalWorkflowAgents = map[string]bool{
	"codex":            true,
	"trae-work":        true,
	"workbuddy":        true,
	"cursor":           true,
	"raccoon-work":     true,
	"deepseek-harness": true,
}

type externalAgentWorkflowTaskRequest struct {
	AgentType              string                           `json:"agent_type"`
	ExternalConversationID string                           `json:"external_conversation_id"`
	ExternalThreadID       string                           `json:"external_thread_id"`
	Skill                  externalAgentWorkflowSkillInput  `json:"skill"`
	TaskDescription        string                           `json:"task_description"`
	InputBindings          map[string]any                   `json:"input_bindings"`
	InputFiles             []externalAgentWorkflowInputFile `json:"input_files"`
	Config                 externalAgentWorkflowConfig      `json:"config"`
	IdempotencyKey         string                           `json:"idempotency_key"`
}

type externalAgentWorkflowConfig struct {
	ReuseWorkflow *bool `json:"reuse_workflow,omitempty"`
}

type externalAgentWorkflowSkillInput struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	ZipBase64 string `json:"zip_base64"`
	ZipSHA256 string `json:"zip_sha256"`
}

type externalAgentWorkflowInputFile struct {
	MaterialID    string `json:"material_id"`
	Name          string `json:"name"`
	MimeType      string `json:"mime_type"`
	ContentBase64 string `json:"content_base64"`
	ContentHash   string `json:"content_hash"`
}

func GetExternalWorkflowCapabilities(w http.ResponseWriter, r *http.Request) {
	if common.UserID(r) == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	agents := make([]string, 0, len(allowedExternalWorkflowAgents))
	for agent := range allowedExternalWorkflowAgents {
		agents = append(agents, agent)
	}
	sort.Strings(agents)
	model, _ := resolveWorkflowModel(r.Context(), store.DB(), common.UserID(r))
	common.ReplyOK(w, map[string]any{
		"contract_version": "external-workflow.v1", "agent_types": agents,
		"skill_sources": []string{"url", "zip_base64"}, "max_zip_bytes": maxExternalAgentSkillDownloadBytes,
		"max_request_bytes": 32 << 20, "poll_after_seconds": 3,
		"config": map[string]any{"reuse_workflow": true}, "interaction_mode": "handoff_to_lazymind",
		"model_configuration": model.Public,
	})
}

func CreateExternalAgentWorkflowTask(w http.ResponseWriter, r *http.Request) {
	userID := common.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body externalAgentWorkflowTaskRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		common.ReplyErrWithData(w, "invalid body", externalTaskError("INVALID_REQUEST", err.Error(), "Provide agent_type, skill{name,url or zip_base64}, task_description, and only documented optional fields."), http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		common.ReplyErrWithData(w, "invalid body", externalTaskError("INVALID_REQUEST", "Expected exactly one JSON object", "Remove trailing JSON or other content."), http.StatusBadRequest)
		return
	}
	body.AgentType = normalizeExternalAgentType(body.AgentType)
	body.TaskDescription = strings.TrimSpace(body.TaskDescription)
	body.IdempotencyKey = strings.TrimSpace(firstNonEmpty(body.IdempotencyKey, r.Header.Get("Idempotency-Key")))
	body.ExternalThreadID = strings.TrimSpace(body.ExternalThreadID)
	body.ExternalConversationID = strings.TrimSpace(body.ExternalConversationID)
	if body.AgentType == "" {
		common.ReplyErrWithData(w, "invalid body", externalTaskError("INVALID_REQUEST", "agent_type is required", "Provide a supported agent_type."), http.StatusBadRequest)
		return
	}
	if len(body.IdempotencyKey) > 255 || len(body.ExternalThreadID) > 255 || len(body.ExternalConversationID) > 255 {
		common.ReplyErrWithData(w, "invalid body", externalTaskError("INVALID_REQUEST", "Request identifiers must not exceed 255 bytes", "Shorten idempotency_key and external identifiers."), http.StatusBadRequest)
		return
	}
	if !allowedExternalWorkflowAgents[body.AgentType] {
		common.ReplyErrWithData(w, "agent not allowed", externalTaskError("AGENT_NOT_ALLOWED", "agent_type is not in the supported external Agent allowlist", "Use codex, trae-work, workbuddy, cursor, raccoon-work, or deepseek-harness."), http.StatusForbidden)
		return
	}
	skillSource, err := normalizeExternalAgentWorkflowSkillSource(r.Context(), body.Skill)
	if err != nil {
		common.ReplyErrWithData(w, err.Error(), externalTaskError(skillSourceErrorCode(err), err.Error(), skillSourceErrorSuggestion(err)), http.StatusBadRequest)
		return
	}
	body.Skill = skillSource.RequestSkill
	if body.TaskDescription == "" {
		common.ReplyErrWithData(w, "task_description is required", externalTaskError("INVALID_REQUEST", "task_description is required", "Provide the task the external Agent wants LazyMind to complete."), http.StatusBadRequest)
		return
	}
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = uuid.NewString()
	}
	if err := validateExternalTaskInputs(body); err != nil {
		common.ReplyErrWithData(w, "invalid body", externalTaskError("INVALID_REQUEST", err.Error(), "Correct the indicated input before submitting the task again."), http.StatusBadRequest)
		return
	}
	requestHash := externalTaskRequestHash(body)
	request := decodeJSONMap(mustJSON(body))
	request["request_hash"] = requestHash
	requestJSON := mustJSON(request)
	now := time.Now().UTC()
	task := orm.ExternalAgentWorkflowTask{
		ID: uuid.NewString(), OwnerUserID: userID, IdempotencyKey: body.IdempotencyKey,
		AgentType: body.AgentType, ExternalConversationID: strings.TrimSpace(body.ExternalConversationID),
		ExternalThreadID: strings.TrimSpace(body.ExternalThreadID), SkillID: "",
		TaskDescription: body.TaskDescription, Status: externalTaskStatusQueued, Stage: "resolve_skill",
		RequestJSON: requestJSON, ResultSummaryJSON: json.RawMessage(`{}`),
		ResultArtifactsJSON: json.RawMessage(`[]`), CreatedAt: now, UpdatedAt: now,
		LazyMindURL: externalWorkflowURL("", ""),
	}
	db := store.DB()
	if err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		_, err := asyncjob.EnqueueInTransaction(r.Context(), tx, externalTaskJobRequest(task, "start"))
		return err
	}); err != nil {
		var existing orm.ExternalAgentWorkflowTask
		if body.IdempotencyKey != "" && db.Where("owner_user_id=? AND idempotency_key=?", userID, body.IdempotencyKey).First(&existing).Error == nil {
			if stringFromMap(decodeJSONMap(existing.RequestJSON), "request_hash") != requestHash {
				common.ReplyErrWithData(w, "idempotency conflict", externalTaskError("IDEMPOTENCY_CONFLICT", "This idempotency_key belongs to a different request", "Reuse the original arguments for a retry, or use a new key for a new task."), http.StatusConflict)
				return
			}
			common.ReplyOK(w, externalTaskResponse(existing))
			return
		}
		common.ReplyErr(w, "create external workflow task failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, externalTaskResponse(task))
}

func GetExternalAgentWorkflowTask(w http.ResponseWriter, r *http.Request) {
	task, ok := loadExternalAgentWorkflowTask(w, r)
	if !ok {
		return
	}
	common.ReplyOK(w, externalTaskResponse(task))
}

func GetExternalAgentWorkflowTaskResult(w http.ResponseWriter, r *http.Request) {
	GetExternalAgentWorkflowTask(w, r)
}

func loadExternalAgentWorkflowTask(w http.ResponseWriter, r *http.Request) (orm.ExternalAgentWorkflowTask, bool) {
	userID, taskID := common.UserID(r), strings.TrimSpace(common.PathVar(r, "task_id"))
	if userID == "" || taskID == "" {
		common.ReplyErr(w, "not found", http.StatusNotFound)
		return orm.ExternalAgentWorkflowTask{}, false
	}
	var task orm.ExternalAgentWorkflowTask
	if err := store.DB().Where("id=? AND owner_user_id=?", taskID, userID).First(&task).Error; err != nil {
		common.ReplyErr(w, "not found", http.StatusNotFound)
		return orm.ExternalAgentWorkflowTask{}, false
	}
	return task, true
}

func reconcileExternalAgentWorkflowTask(ctx context.Context, db *gorm.DB, task orm.ExternalAgentWorkflowTask) orm.ExternalAgentWorkflowTask {
	if externalTaskTerminal(task.Status) || task.Status == externalTaskStatusWaitingUserAction {
		return task
	}
	if task.SkillID == "" {
		task = ensureExternalWorkflowTaskSkill(ctx, db, task)
		if task.SkillID == "" || externalTaskTerminal(task.Status) || task.Status == externalTaskStatusWaitingUserAction {
			return task
		}
	}
	if task.WorkflowRef == "" {
		task = ensureExternalWorkflowTaskWorkflow(ctx, db, task)
		if task.WorkflowRef == "" || externalTaskTerminal(task.Status) || task.Status == externalTaskStatusWaitingUserAction {
			return task
		}
	}
	if task.SessionID == "" {
		task = ensureExternalWorkflowTaskSession(ctx, db, task)
		if task.SessionID == "" || externalTaskTerminal(task.Status) || task.Status == externalTaskStatusWaitingUserAction {
			return task
		}
	}
	return advanceExternalWorkflowTask(ctx, db, task)
}

func ensureExternalWorkflowTaskSkill(ctx context.Context, db *gorm.DB, task orm.ExternalAgentWorkflowTask) orm.ExternalAgentWorkflowTask {
	request := decodeJSONMap(task.RequestJSON)
	skill, _ := request["skill"].(map[string]any)
	source := externalAgentWorkflowSkillInput{
		Name:      stringFromMap(skill, "name"),
		URL:       stringFromMap(skill, "url"),
		ZipBase64: stringFromMap(skill, "zip_base64"),
		ZipSHA256: stringFromMap(skill, "zip_sha256"),
	}
	resolved, err := resolveExternalAgentSkillSource(ctx, db, task.OwnerUserID, task.OwnerUserID, source)
	if err != nil {
		return failExternalTask(db, task, skillSourceErrorCode(err), err.Error(), skillSourceErrorSuggestion(err))
	}
	skill["source_type"] = resolved.SourceType
	skill["source_key"] = resolved.SourceKey
	skill["resolved"] = true
	skill["install_status"] = resolved.InstallStatus
	if _, ok := skill["zip_base64"]; ok {
		skill["zip_base64"] = ""
	}
	request["skill"] = skill
	requestJSON, _ := json.Marshal(request)
	updates := map[string]any{
		"skill_id":     resolved.SkillID,
		"request_json": requestJSON,
		"stage":        "preflight",
		"updated_at":   time.Now().UTC(),
	}
	return persistExternalTask(db, task, updates)
}

func ensureExternalWorkflowTaskWorkflow(ctx context.Context, db *gorm.DB, task orm.ExternalAgentWorkflowTask) orm.ExternalAgentWorkflowTask {
	if task.DraftID == "" {
		snapshot, err := loadWorkflowSourceSkill(ctx, db, task.OwnerUserID, task.SkillID)
		if err != nil {
			return failExternalTask(db, task, "SKILL_NOT_AVAILABLE", "Skill 不存在、未发布或缺少 SKILL.md。", "请确认 Skill 已发布，且包内包含非空 SKILL.md。")
		}
		checks := preflightSkillSnapshot(snapshot)
		requirements := detectSkillCapabilityRequirementsFromSnapshot(snapshot)
		checks = append(checks, preflightCapabilityChecks(requirements)...)
		checks = append(checks, preflightCapabilityRuntimeChecks(ctx, db, task.OwnerUserID, requirements)...)
		for _, check := range checks {
			if check.Severity == "error" {
				return waitExternalTask(db, task, "preflight", "PREFLIGHT_BLOCKED", check.Message, check.Suggestion)
			}
		}
		config, _ := decodeJSONMap(task.RequestJSON)["config"].(map[string]any)
		reuse, _ := config["reuse_workflow"].(bool)
		if workflow, ok := latestLinkedWorkflowForSkill(ctx, db, task.OwnerUserID, snapshot, task); ok && (config["reuse_workflow"] == nil || reuse) {
			if err := ensureExternalWorkflowRunnable(ctx, db, task.OwnerUserID, workflow.WorkflowRef); err != nil {
				return failExternalTask(db, task, "WORKFLOW_SETTING_UPDATE_FAILED", err.Error(), "请进入 LazyMind 检查 Workflow 调用方式设置后重试。")
			}
			updates := map[string]any{
				"skill_revision_id": workflow.SourceSkillRevisionID, "workflow_ref": workflow.WorkflowRef,
				"workflow_id": workflow.WorkflowID, "workflow_revision_id": workflow.HeadRevisionID,
				"status": externalTaskStatusRunning, "stage": "prepare", "lazymind_url": externalWorkflowURL("", workflow.WorkflowRef),
				"updated_at": time.Now().UTC(),
			}
			return persistExternalTask(db, task, updates)
		}
		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			draft := createExternalWorkflowDraft(ctx, tx, task, snapshot)
			if draft.ID == "" {
				return errors.New("create workflow draft failed")
			}
			if err := enqueueExternalWorkflowGeneration(ctx, tx, task, draft, snapshot); err != nil {
				return err
			}
			return tx.Model(&task).Updates(map[string]any{"draft_id": draft.ID, "skill_revision_id": snapshot.RevisionID, "status": externalTaskStatusConverting, "stage": "generate", "lazymind_url": externalWorkflowURL(draft.ID, ""), "updated_at": time.Now().UTC()}).Error
		}); err != nil {
			return failExternalTask(db, task, "WORKFLOW_GENERATION_ENQUEUE_FAILED", err.Error(), "请稍后重试，或在 LazyMind 中打开草稿继续处理。")
		}
		return persistExternalTask(db, task, nil)
	}
	var draft orm.WorkflowDraft
	if err := db.WithContext(ctx).Where("id=? AND created_by=? AND deleted_at IS NULL", task.DraftID, task.OwnerUserID).First(&draft).Error; err != nil {
		return failExternalTask(db, task, "WORKFLOW_DRAFT_NOT_FOUND", "Workflow 草稿不存在或不可访问。", "请重新发起外部 Agent 任务。")
	}
	switch draft.GenerateStatus {
	case generateStatusNeedsConfirm:
		return waitExternalTask(db, task, "generate", "GENERATION_NEEDS_CONFIRMATION", "Skill 转 Workflow 需要用户选择或确认。", "请进入 LazyMind 继续处理该 Workflow 草稿。")
	case generateStatusRejected:
		return failExternalTask(db, task, "SKILL_NOT_GENERATABLE", firstNonEmpty(draft.GenerateError, "Skill 当前无法自动转换为 Workflow。"), "请修复 Skill 内容，或在 LazyMind 中手动创建 Workflow。")
	case generateStatusFailed:
		failure := decodeJSONMap(json.RawMessage(draft.GenerateError))
		switch code := stringFromMap(failure, "code"); code {
		case "MODEL_NOT_CONFIGURED", "MODEL_CONFIG_LOAD_FAILED", "MODEL_CONFIG_CHECK_FAILED":
			return failExternalTask(db, task, code, stringFromMap(failure, "message"), "请检查当前实例中任务所属用户的“模型与服务”设置，以及算法服务连接。")
		}
		return failExternalTask(db, task, "WORKFLOW_GENERATION_FAILED", firstNonEmpty(draft.GenerateError, "Workflow 生成失败。"), "请进入 LazyMind 查看失败阶段和恢复建议。")
	case generateStatusDone:
		workflowRef, revisionID, err := publishDraftForExternalTask(ctx, db, task.OwnerUserID, task.DraftID)
		if err != nil {
			return failExternalTask(db, task, "WORKFLOW_PUBLISH_FAILED", err.Error(), "请进入 LazyMind 查看发布诊断并修复。")
		}
		workflowID := extractWorkflowID(draft.WorkflowYAMLContent)
		updates := map[string]any{"workflow_ref": workflowRef, "workflow_id": workflowID, "workflow_revision_id": revisionID, "status": externalTaskStatusRunning, "stage": "prepare", "lazymind_url": externalWorkflowURL("", workflowRef), "updated_at": time.Now().UTC()}
		return persistExternalTask(db, task, updates)
	default:
		updates := map[string]any{"status": externalTaskStatusConverting, "stage": "generate", "updated_at": time.Now().UTC()}
		return persistExternalTask(db, task, updates)
	}
}

func ensureExternalWorkflowTaskSession(ctx context.Context, db *gorm.DB, task orm.ExternalAgentWorkflowTask) orm.ExternalAgentWorkflowTask {
	repo := workflowstore.New(db)
	pkg, err := repo.GetWorkflowPackage(ctx, task.OwnerUserID, firstNonEmpty(task.WorkflowRef, task.WorkflowID), task.WorkflowRevisionID)
	if err != nil {
		return failExternalTask(db, task, "WORKFLOW_NOT_FOUND", "Workflow 不存在或不可访问。", "请重新生成或发布 Workflow。")
	}
	conversationID, err := ensureExternalWorkflowConversation(ctx, db, task)
	if err != nil {
		return failExternalTask(db, task, "CONVERSATION_CREATE_FAILED", err.Error(), "请稍后重试，或在 LazyMind 中新建会话后继续。")
	}
	historyID, err := ensureExternalWorkflowChatAnchor(ctx, db, task, conversationID)
	if err != nil {
		return failExternalTask(db, task, "CONVERSATION_HISTORY_CREATE_FAILED", err.Error(), "请稍后重试，或在 LazyMind 中打开会话后继续。")
	}
	request := decodeJSONMap(task.RequestJSON)
	inputBindings, _ := request["input_bindings"].(map[string]any)
	inputFiles, _ := request["input_files"].([]any)
	bindings, err := inputBindingsFromExternalRequest(ctx, db, task.OwnerUserID, inputBindings, inputFiles)
	if err != nil {
		return failExternalTask(db, task, "INPUT_RESOURCE_IMPORT_FAILED", err.Error(), "请检查 input_files 的 material_id、name、mime_type、content_base64 和 content_hash。")
	}
	sessionID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("external-workflow-session:"+task.ID)).String()
	session, _, err := repo.CreateInitializedHostSessionWithTrigger(ctx, task.OwnerUserID, sessionID, conversationID,
		"external-agent", task.AgentType+":"+task.ExternalThreadID, "lazymind", pkg, "auto",
		historyID, string(mustJSON(map[string]any{"text": task.TaskDescription, "agent_type": task.AgentType})), bindings,
		workflowstore.ControlSettings{})
	if err != nil {
		if errors.Is(err, workflowstore.ErrSessionConflict) {
			return waitExternalTask(db, task, "prepare", "WORKFLOW_SESSION_CONFLICT", "当前会话已有未完成 Workflow。", "请进入 LazyMind 处理或关闭已有 Workflow 后重试。")
		}
		return failExternalTask(db, task, "WORKFLOW_SESSION_CREATE_FAILED", err.Error(), "请检查输入绑定和 Workflow 配置。")
	}
	if err := attachExternalWorkflowChatAnchor(ctx, db, task, conversationID, historyID, session.ID); err != nil {
		return failExternalTask(db, task, "CONVERSATION_HISTORY_UPDATE_FAILED", err.Error(), "请进入 LazyMind 查看 Workflow 状态。")
	}
	delete(request, "input_files")
	updates := map[string]any{"session_id": session.ID, "conversation_id": conversationID, "request_json": mustJSON(request), "status": externalTaskStatusRunning, "stage": "execute", "lazymind_url": externalConversationURL(conversationID), "updated_at": time.Now().UTC()}
	return persistExternalTask(db, task, updates)
}

func advanceExternalWorkflowTask(ctx context.Context, db *gorm.DB, task orm.ExternalAgentWorkflowTask) orm.ExternalAgentWorkflowTask {
	var session orm.WorkflowSession
	if err := db.WithContext(ctx).Where("id=? AND create_user_id=?", task.SessionID, task.OwnerUserID).First(&session).Error; err != nil {
		return failExternalTask(db, task, "WORKFLOW_SESSION_NOT_FOUND", "Workflow Session 不存在或不可访问。", "请重新发起任务。")
	}
	projection, err := projectSession(ctx, db, &session)
	if err != nil {
		return failExternalTask(db, task, "WORKFLOW_PROJECTION_FAILED", err.Error(), "请进入 LazyMind 查看 Workflow 状态。")
	}
	request := decodeJSONMap(task.RequestJSON)
	request["progress"] = map[string]any{
		"total_steps": len(projection.Projection.Nodes), "completed_steps": len(projection.Projection.Past),
		"current_steps": projection.Projection.Current, "blocked_steps": projection.Projection.Blocked,
		"skipped_steps": len(projection.Projection.Pruned) + len(projection.Projection.Bypassed),
	}
	task = persistExternalTask(db, task, map[string]any{"request_json": mustJSON(request), "updated_at": time.Now().UTC()})
	if task.ErrorCode == "TASK_STATE_WRITE_FAILED" {
		return task
	}
	if projection.Projection.Completed || session.Status == "completed" {
		return completeExternalTask(ctx, db, task)
	}
	if session.Status == "stopped" || session.Status == "paused" || session.Status == "canceled" {
		return waitExternalTask(db, task, "execute", "WORKFLOW_STOPPED", "Workflow 已停止。", "请进入 LazyMind 查看或继续处理，外部调用不会自动恢复已停止的任务。")
	}
	if session.Status == "failed" || len(projection.Projection.Retryable) > 0 {
		return failExternalTask(db, task, "WORKFLOW_EXECUTION_FAILED", "Workflow 有执行失败的步骤。", "请进入 LazyMind 查看失败步骤和重试选项。")
	}
	for _, stepID := range projection.Projection.Ready {
		node := projection.Projection.Nodes[stepID]
		if node.RequiresApproval || node.Mode == "human" {
			return waitExternalTask(db, task, "execute", "USER_ACTION_REQUIRED", "Workflow 需要用户确认或人工步骤。", "请进入 LazyMind 继续处理该任务。")
		}
		if err := advanceExternalStep(ctx, db, task, session, projection, stepID); err != nil {
			return failExternalTask(db, task, "WORKFLOW_ADVANCE_FAILED", err.Error(), "请进入 LazyMind 查看步骤执行状态。")
		}
		updates := map[string]any{"status": externalTaskStatusRunning, "stage": "execute", "updated_at": time.Now().UTC()}
		return persistExternalTask(db, task, updates)
	}
	active := false
	for _, node := range projection.Projection.Nodes {
		switch node.Execution {
		case "pending", "queued", "claimed", "running", "waiting":
			active = true
		}
	}
	if !active && len(projection.Projection.Ready) == 0 {
		return waitExternalTask(db, task, "execute", "WORKFLOW_BLOCKED", "Workflow 暂无可自动执行的步骤。", "请进入 LazyMind 检查缺失输入、执行条件或需要人工继续的步骤。")
	}
	updates := map[string]any{"status": externalTaskStatusRunning, "stage": "execute", "updated_at": time.Now().UTC()}
	return persistExternalTask(db, task, updates)
}

func createExternalWorkflowDraft(ctx context.Context, db *gorm.DB, task orm.ExternalAgentWorkflowTask, snapshot workflowSourceSkillSnapshot) orm.WorkflowDraft {
	now := time.Now().UTC()
	name := strings.TrimSpace(snapshot.Name)
	if name == "" {
		name = "External Agent Workflow"
	}
	draft := orm.WorkflowDraft{ID: uuid.NewString(), Name: name, SourceType: "skill", SourceSkillID: task.SkillID,
		SourceSkillName: snapshot.Name, SourceSkillRevisionID: snapshot.RevisionID, SourceSkillRevisionNo: snapshot.RevisionNo,
		SourceSkillTreeHash: snapshot.TreeHash, CreatedBy: task.OwnerUserID, Version: 1, ScriptsContent: "{}",
		GenerateStatus: generateStatusAnalyzing, CreatedAt: now, UpdatedAt: now}
	if err := db.WithContext(ctx).Create(&draft).Error; err != nil {
		return orm.WorkflowDraft{}
	}
	return draft
}

func enqueueExternalWorkflowGeneration(ctx context.Context, db *gorm.DB, task orm.ExternalAgentWorkflowTask, draft orm.WorkflowDraft, snapshot workflowSourceSkillSnapshot) error {
	_, err := queueWorkflowDraftGeneration(ctx, db, task.OwnerUserID, draft.ID, workflowGenerationRequest{
		Description: externalTaskGenerationDescription(task), SkillID: task.SkillID,
		StartPhase: generatePhaseDesignBrief, Snapshot: &snapshot,
		IdempotencyKey: "external-agent-workflow:" + task.ID + ":generate",
	})
	if err != nil {
		return err
	}
	return nil
}

func publishDraftForExternalTask(ctx context.Context, db *gorm.DB, userID, draftID string) (string, string, error) {
	result, diagnostics, err := publishAuthoringWorkflow(ctx, db, userID, draftID, true, func() bool {
		return common.UserIsAdmin(ctx, userID)
	})
	if err != nil {
		if diagnostics != nil {
			return "", "", fmt.Errorf("%s: %s", err.Message, mustJSON(diagnostics))
		}
		return "", "", err
	}
	workflowRef := stringFromMap(result, "workflow_ref")
	if err := ensureExternalWorkflowRunnable(ctx, db, userID, workflowRef); err != nil {
		return "", "", err
	}
	return workflowRef, stringFromMap(result, "revision_id"), nil
}

func ensureExternalWorkflowRunnable(ctx context.Context, db *gorm.DB, userID, workflowRef string) error {
	workflowRef = strings.TrimSpace(workflowRef)
	if userID == "" || workflowRef == "" {
		return errors.New("workflow setting scope is incomplete")
	}
	now := time.Now().UTC()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var setting orm.UserWorkflowSetting
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id=? AND plugin_ref=?", userID, workflowRef).First(&setting).Error
		if err == nil {
			mode := normalizeWorkflowCallMode(setting.CallMode, setting.Enabled)
			if setting.Enabled && workflowCallModeEnabled(mode) {
				return nil
			}
			if !workflowCallModeEnabled(mode) {
				mode = WorkflowCallModeManual
			}
			return tx.Model(&orm.UserWorkflowSetting{}).Where("user_id=? AND plugin_ref=?", userID, workflowRef).
				Updates(map[string]any{"enabled": true, "call_mode": mode, "updated_at": now}).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		setting = orm.UserWorkflowSetting{UserID: userID, WorkflowRef: workflowRef, Enabled: true, CallMode: WorkflowCallModeManual, UpdatedAt: now}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "plugin_ref"}}, DoUpdates: clause.AssignmentColumns([]string{"enabled", "call_mode", "updated_at"})}).Create(&setting).Error
	})
}

func advanceExternalStep(ctx context.Context, db *gorm.DB, task orm.ExternalAgentWorkflowTask, session orm.WorkflowSession, projection projectionResponse, stepID string) error {
	commandID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("%s:%d:%s", session.ID, projection.StateVersion, stepID))).String()
	payload := transitionCommandRequest{
		HostedTaskID: task.ID,
		CommandID:    commandID, Operation: "advance", ExpectedStateVersion: projection.StateVersion,
		GraphHash: projection.GraphHash, WorkflowID: session.WorkflowID, WorkflowRef: session.WorkflowRef,
		WorkflowRevisionID: session.WorkflowRevisionID, WorkflowRevisionNo: session.WorkflowRevisionNo,
		WorkflowTreeHash: session.WorkflowTreeHash, WorkflowRemoteRoot: session.WorkflowRemoteRoot,
		ConversationID: session.ConversationID, UserID: session.CreateUserID, WorkflowMode: session.WorkflowMode,
		Targets: []transitionTarget{{TargetStepID: stepID, Objective: externalWorkflowRuntimeObjective(task.TaskDescription), UserInput: task.TaskDescription}},
	}
	response, _ := transitionWorkflowSession(ctx, db, session.ID, payload)
	if !response.Accepted {
		if response.Error != nil {
			return fmt.Errorf("%s: %s", response.Error.Code, response.Error.Message)
		}
		return errors.New("advance workflow step rejected")
	}
	return nil
}

func latestLinkedWorkflowForSkill(ctx context.Context, db *gorm.DB, userID string, snapshot workflowSourceSkillSnapshot, task orm.ExternalAgentWorkflowTask) (orm.WorkflowResource, bool) {
	var workflows []orm.WorkflowResource
	err := db.WithContext(ctx).Where("source_type=? AND source_skill_id=? AND source_skill_tree_hash=? AND status=? AND head_revision_id <> '' AND (owner_user_id=? OR owner_user_id='')",
		"skill", snapshot.SkillID, snapshot.TreeHash, "active", userID).Order("updated_at DESC").Limit(20).Find(&workflows).Error
	if err != nil {
		return orm.WorkflowResource{}, false
	}
	for _, workflow := range workflows {
		if externalWorkflowReusableForTask(ctx, db, workflow, task) {
			return workflow, true
		}
	}
	return orm.WorkflowResource{}, false
}

func externalWorkflowRuntimeObjective(userInput string) string {
	userInput = strings.TrimSpace(userInput)
	if userInput == "" {
		return ""
	}
	return "Complete this external Agent task exactly. Treat the runtime user input as the source of truth for the concrete request, search keywords, filters, output requirements, and final answer. Runtime task:\n" + userInput
}

func externalWorkflowReusableForTask(ctx context.Context, db *gorm.DB, workflow orm.WorkflowResource, task orm.ExternalAgentWorkflowTask) bool {
	return externalWorkflowAcceptsInputs(ctx, db, workflow, task) && externalWorkflowUsesRuntimeInput(ctx, db, workflow)
}

func externalWorkflowUsesRuntimeInput(ctx context.Context, db *gorm.DB, workflow orm.WorkflowResource) bool {
	revisionID := strings.TrimSpace(workflow.HeadRevisionID)
	if revisionID == "" {
		return false
	}
	var revision orm.WorkflowRevision
	if err := db.WithContext(ctx).Select("id", "compiled_graph").First(&revision, "id=?", revisionID).Error; err != nil {
		return false
	}
	graph := string(revision.CompiledGraph)
	return strings.Contains(graph, "{{user_input}}") || strings.Contains(graph, "{{ user_input }}")
}

func externalWorkflowAcceptsInputs(ctx context.Context, db *gorm.DB, workflow orm.WorkflowResource, task orm.ExternalAgentWorkflowTask) bool {
	inputs := externalTaskInputDescriptors(task)
	if len(inputs) == 0 {
		return true
	}
	var revision orm.WorkflowRevision
	if err := db.WithContext(ctx).First(&revision, "id=?", workflow.HeadRevisionID).Error; err != nil {
		return false
	}
	var graph graphengine.CompiledStateGraph
	if json.Unmarshal(revision.CompiledGraph, &graph) != nil {
		return false
	}
	for _, input := range inputs {
		if graph.MaterialProducers[input["material_id"]].Kind != "external" {
			return false
		}
	}
	return true
}

func completeExternalTask(ctx context.Context, db *gorm.DB, task orm.ExternalAgentWorkflowTask) orm.ExternalAgentWorkflowTask {
	repo := workflowstore.New(db)
	artifacts, err := repo.ListArtifacts(ctx, task.OwnerUserID, task.SessionID)
	if err != nil {
		return failExternalTask(db, task, "RESULT_READ_FAILED", err.Error(), "请进入 LazyMind 查看完整执行记录。")
	}
	outputs := make([]workflowstore.Artifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if !artifact.Deleted && artifact.Validity == "effective" {
			outputs = append(outputs, artifact)
		}
	}
	summary := map[string]any{"message": "Workflow completed.", "task_description": task.TaskDescription, "artifact_count": len(outputs)}
	artifactsJSON, _ := json.Marshal(outputs)
	summaryJSON, _ := json.Marshal(summary)
	now := time.Now().UTC()
	updates := map[string]any{"status": externalTaskStatusSucceeded, "stage": "collect_result", "error_code": "", "error_message": "", "suggestion": "", "result_summary_json": summaryJSON, "result_artifacts_json": artifactsJSON, "completed_at": &now, "updated_at": now}
	return persistExternalTask(db, task, updates)
}

func inputBindingsFromExternalRequest(ctx context.Context, db *gorm.DB, owner string, values map[string]any, files []any) ([]workflowstore.InputBinding, error) {
	out := make([]workflowstore.InputBinding, 0, len(values)+len(files))
	repo := workflowstore.New(db)
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, materialID := range keys {
		raw := values[materialID]
		value, _ := raw.(map[string]any)
		resourceID, _ := value["resource_id"].(string)
		hash, _ := value["content_hash"].(string)
		revisionFloat, _ := value["revision"].(float64)
		if strings.TrimSpace(materialID) == "" || resourceID == "" {
			return nil, fmt.Errorf("input_bindings[%q] requires material_id and resource_id", materialID)
		}
		resource, err := repo.GetInputResource(ctx, owner, resourceID)
		if err != nil {
			return nil, fmt.Errorf("input_bindings[%q]: resource is not available to this user", materialID)
		}
		if (revisionFloat != 0 && int64(revisionFloat) != resource.Revision) || (hash != "" && hash != resource.ContentHash) {
			return nil, fmt.Errorf("input_bindings[%q]: resource revision or content_hash mismatch", materialID)
		}
		out = append(out, workflowstore.InputBinding{MaterialID: materialID, ResourceType: "input_resource",
			ResourceID: resourceID, ResourceRevision: resource.Revision, ContentHash: resource.ContentHash,
			CreatedByCommandID: "external-agent-workflow"})
	}
	for index, raw := range files {
		file, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("input_files[%d] must be an object", index)
		}
		materialID, _ := file["material_id"].(string)
		name, _ := file["name"].(string)
		mimeType, _ := file["mime_type"].(string)
		encoded, _ := file["content_base64"].(string)
		expectedHash, _ := file["content_hash"].(string)
		materialID = strings.TrimSpace(materialID)
		name = strings.TrimSpace(name)
		mimeType = strings.TrimSpace(mimeType)
		encoded = strings.TrimSpace(encoded)
		expectedHash = strings.TrimSpace(expectedHash)
		if materialID == "" || name == "" || mimeType == "" || encoded == "" {
			return nil, fmt.Errorf("input_files[%d] requires material_id, name, mime_type, and content_base64", index)
		}
		content, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("input_files[%d] content_base64 is invalid", index)
		}
		if len(content) == 0 {
			return nil, fmt.Errorf("input_files[%d] content is empty", index)
		}
		sum := sha256.Sum256(content)
		hash := "sha256:" + hex.EncodeToString(sum[:])
		if expectedHash != "" && expectedHash != hash {
			return nil, fmt.Errorf("input_files[%d] content_hash mismatch", index)
		}
		resource, _, err := repo.ImportInputResource(ctx, owner, name, mimeType, hash, content)
		if err != nil {
			return nil, err
		}
		out = append(out, workflowstore.InputBinding{MaterialID: materialID, ResourceType: "input_resource",
			ResourceID: resource.ID, ResourceRevision: resource.Revision, ContentHash: resource.ContentHash,
			CreatedByCommandID: "external-agent-workflow"})
	}
	return out, nil
}

func failExternalTask(db *gorm.DB, task orm.ExternalAgentWorkflowTask, code, message, suggestion string) orm.ExternalAgentWorkflowTask {
	now := time.Now().UTC()
	request := decodeJSONMap(task.RequestJSON)
	delete(request, "input_files")
	if skill, ok := request["skill"].(map[string]any); ok {
		delete(skill, "zip_base64")
	}
	updates := map[string]any{"request_json": mustJSON(request), "status": externalTaskStatusFailed, "error_code": code, "error_message": message, "suggestion": suggestion, "completed_at": &now, "updated_at": now}
	return persistExternalTask(db, task, updates)
}

func persistExternalTask(db *gorm.DB, task orm.ExternalAgentWorkflowTask, updates map[string]any) orm.ExternalAgentWorkflowTask {
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&task, "id=?", task.ID).Error; err != nil {
			return err
		}
		if externalTaskTerminal(task.Status) {
			return nil
		}
		if len(updates) > 0 {
			if err := tx.Model(&task).Updates(updates).Error; err != nil {
				return err
			}
			if err := tx.First(&task, "id=?", task.ID).Error; err != nil {
				return err
			}
		}
		return persistExternalWorkflowChat(tx, task)
	})
	if err != nil {
		// The job retries persistence errors; never pretend the transition was stored.
		task.Status = externalTaskStatusFailed
		task.ErrorCode, task.ErrorMessage = "TASK_STATE_WRITE_FAILED", err.Error()
		return task
	}
	return task
}

func waitExternalTask(db *gorm.DB, task orm.ExternalAgentWorkflowTask, stage, code, message, suggestion string) orm.ExternalAgentWorkflowTask {
	updates := map[string]any{"status": externalTaskStatusWaitingUserAction, "stage": stage, "error_code": code, "error_message": message, "suggestion": suggestion, "updated_at": time.Now().UTC()}
	return persistExternalTask(db, task, updates)
}

func externalTaskResponse(task orm.ExternalAgentWorkflowTask) map[string]any {
	done := externalTaskTerminal(task.Status)
	action, pollAfter := "poll", 3
	switch {
	case task.Status == externalTaskStatusSucceeded:
		action, pollAfter = "read_result", 0
	case task.Status == externalTaskStatusWaitingUserAction:
		action, pollAfter = "open_lazymind", 0
	case done:
		action, pollAfter = "review_error", 0
	}
	return map[string]any{
		"contract_version": "external-workflow.v1", "done": done, "result_ready": task.Status == externalTaskStatusSucceeded,
		"next_action": action, "poll_after_seconds": pollAfter, "stage_label": externalWorkflowStageLabel(task.Stage),
		"idempotency_key": task.IdempotencyKey,
		"progress":        decodeJSONMap(task.RequestJSON)["progress"],
		"task_id":         task.ID, "agent_type": task.AgentType, "status": task.Status,
		"display_status": externalTaskDisplayStatus(task.Status), "stage": task.Stage,
		"skill":    externalTaskSkillResponse(task),
		"draft_id": task.DraftID, "workflow_ref": task.WorkflowRef, "workflow_id": task.WorkflowID,
		"workflow_revision_id": task.WorkflowRevisionID, "session_id": task.SessionID,
		"conversation_id": task.ConversationID, "external_conversation_id": task.ExternalConversationID,
		"external_thread_id": task.ExternalThreadID, "error_code": task.ErrorCode,
		"error_message": task.ErrorMessage, "suggestion": task.Suggestion,
		"result_summary":   decodeJSONMap(task.ResultSummaryJSON),
		"result_artifacts": decodeJSONList(task.ResultArtifactsJSON),
		"lazymind_url":     firstNonEmpty(task.LazyMindURL, externalWorkflowURL(task.DraftID, task.WorkflowRef)), "created_at": task.CreatedAt, "updated_at": task.UpdatedAt,
		"completed_at": task.CompletedAt,
	}
}

func externalTaskSkillResponse(task orm.ExternalAgentWorkflowTask) map[string]any {
	request := decodeJSONMap(task.RequestJSON)
	skill, _ := request["skill"].(map[string]any)
	out := map[string]any{
		"name":           stringFromMap(skill, "name"),
		"url":            stringFromMap(skill, "url"),
		"source_type":    stringFromMap(skill, "source_type"),
		"source_key":     stringFromMap(skill, "source_key"),
		"install_status": stringFromMap(skill, "install_status"),
		"resolved":       task.SkillID != "",
	}
	if value, ok := skill["resolved"].(bool); ok {
		out["resolved"] = value
	}
	if stringFromMap(skill, "zip_sha256") != "" && out["source_key"] == "" {
		out["source_key"] = stringFromMap(skill, "zip_sha256")
	}
	return out
}

func externalTaskError(code, message, suggestion string) map[string]any {
	return map[string]any{"error_code": code, "error_message": message, "suggestion": suggestion}
}

func skillSourceErrorCode(err error) string {
	var sourceErr externalSkillSourceError
	if errors.As(err, &sourceErr) && sourceErr.Code != "" {
		return sourceErr.Code
	}
	return "SKILL_INSTALL_FAILED"
}

func skillSourceErrorSuggestion(err error) string {
	var sourceErr externalSkillSourceError
	if errors.As(err, &sourceErr) && sourceErr.Suggestion != "" {
		return sourceErr.Suggestion
	}
	return "Confirm the Skill source is accessible, valid, and contains SKILL.md."
}

func externalTaskTerminal(status string) bool {
	return status == externalTaskStatusSucceeded || status == externalTaskStatusFailed || status == "canceled"
}

func externalTaskDisplayStatus(status string) string {
	switch status {
	case externalTaskStatusQueued:
		return "waiting"
	case externalTaskStatusConverting, externalTaskStatusRunning:
		return "running"
	case externalTaskStatusWaitingUserAction:
		return "waiting_user_action"
	case externalTaskStatusSucceeded:
		return "succeeded"
	case externalTaskStatusFailed:
		return "failed"
	default:
		return status
	}
}

func normalizeExternalAgentType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.ReplaceAll(value, "_", "-")
	switch value {
	case "traework", "trae":
		return "trae-work"
	case "deepseek", "deepseek-harness":
		return "deepseek-harness"
	case "raccoon", "raccoon-work", "xiaohuanxiong", "xiaohuan-xiong", "小浣熊":
		return "raccoon-work"
	default:
		return value
	}
}

func stringFromMap(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func boolPtr(value bool) *bool {
	return &value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func externalWorkflowURL(draftID, workflowRef string) string {
	if draftID != "" {
		return "/memory-management/workflows/" + draftID
	}
	if workflowRef != "" {
		return "/memory-management/workflows?workflow_ref=" + workflowRef
	}
	return "/memory-management/workflows"
}

func externalConversationURL(conversationID string) string {
	if conversationID == "" {
		return "/agent/chat/home"
	}
	return "/agent/chat/home/" + conversationID
}

func decodeJSONMap(raw json.RawMessage) map[string]any {
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func decodeJSONList(raw json.RawMessage) []any {
	out := []any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func mustJSON(value any) json.RawMessage {
	out, _ := json.Marshal(value)
	return out
}
