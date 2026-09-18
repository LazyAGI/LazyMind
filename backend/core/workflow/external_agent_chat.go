package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common/orm"
	workflowstore "lazymind/core/workflow/store"
)

func ensureExternalWorkflowConversation(ctx context.Context, db *gorm.DB, task orm.ExternalAgentWorkflowTask) (string, error) {
	if task.ConversationID != "" {
		return task.ConversationID, nil
	}
	now := time.Now().UTC()
	convID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("external-workflow-conversation:"+task.ID)).String()
	ext := mustJSON(map[string]any{"external_agent_workflow_task_id": task.ID, "agent_type": task.AgentType, "external_conversation_id": task.ExternalConversationID, "external_thread_id": task.ExternalThreadID})
	conv := orm.Conversation{
		ID: convID, DisplayName: "[" + task.AgentType + "] " + stringFromMap(externalTaskSkillResponse(task), "name"),
		ChatExecutor: "lazymind", SourceType: "external_agent_workflow", Ext: ext,
		BaseModel: orm.BaseModel{CreateUserID: task.OwnerUserID, CreateUserName: "", CreatedAt: now, UpdatedAt: now},
	}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&conv).Error; err != nil {
		return "", err
	}
	if err := db.Model(&task).Updates(map[string]any{"conversation_id": convID, "updated_at": now}).Error; err != nil {
		return "", err
	}
	return convID, nil
}

func externalWorkflowHistoryID(taskID string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("external-workflow-history:"+taskID)).String()
}

func externalWorkflowRunID(taskID string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("external-workflow-run:"+taskID)).String()
}

func ensureExternalWorkflowChatAnchor(ctx context.Context, db *gorm.DB, task orm.ExternalAgentWorkflowTask, conversationID string) (string, error) {
	historyID := externalWorkflowHistoryID(task.ID)
	now := time.Now().UTC()
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing orm.ChatHistory
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", historyID).First(&existing).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var maxSeq int
		if err := tx.Model(&orm.ChatHistory{}).Where("conversation_id = ?", conversationID).
			Select("COALESCE(MAX(seq), 0)").Scan(&maxSeq).Error; err != nil {
			return err
		}
		ext := mustJSON(map[string]any{
			"external_agent_workflow_task_id": task.ID,
			"agent_type":                      task.AgentType,
			"external_conversation_id":        task.ExternalConversationID,
			"external_thread_id":              task.ExternalThreadID,
			"source_type":                     "external_agent_workflow",
			"model_route":                     externalWorkflowModelRoute(ctx, tx, task.OwnerUserID),
		})
		history := orm.ChatHistory{
			ID: historyID, Seq: maxSeq + 1, ConversationID: conversationID,
			AlgorithmID: "workflow:external-agent", RawContent: task.TaskDescription, Content: task.TaskDescription,
			Result: externalWorkflowChatResult(task, ""),
			RunID:  externalWorkflowRunID(task.ID), RunStatus: "generating", Ext: ext,
			ThinkingDurationS: externalWorkflowThinkingSeconds(task, now),
			TimeMixin:         orm.TimeMixin{CreateTime: now, UpdateTime: now},
		}
		if err := tx.Create(&history).Error; err != nil {
			return err
		}
		if err := tx.Model(&orm.Conversation{}).Where("id = ?", conversationID).
			Updates(map[string]any{"updated_at": now, "chat_times": gorm.Expr("chat_times + ?", 1)}).Error; err != nil {
			return err
		}
		return nil
	})
	return historyID, err
}

func attachExternalWorkflowChatAnchor(ctx context.Context, db *gorm.DB, task orm.ExternalAgentWorkflowTask, conversationID, historyID, sessionID string) error {
	ext := mustJSON(map[string]any{
		"external_agent_workflow_task_id": task.ID,
		"agent_type":                      task.AgentType,
		"external_conversation_id":        task.ExternalConversationID,
		"external_thread_id":              task.ExternalThreadID,
		"workflow_session_id":             sessionID,
		"source_type":                     "external_agent_workflow",
		"model_route":                     externalWorkflowModelRoute(ctx, db, task.OwnerUserID),
	})
	now := time.Now().UTC()
	result := db.WithContext(ctx).Model(&orm.ChatHistory{}).
		Where("id = ? AND conversation_id = ?", historyID, conversationID).
		Updates(map[string]any{"ext": ext, "update_time": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("external workflow chat history not found")
	}
	return nil
}

func externalWorkflowResultText(artifacts []workflowstore.Artifact) string {
	var builder strings.Builder
	builder.WriteString("LazyMind Workflow 已完成。")
	if len(artifacts) == 0 {
		return builder.String()
	}
	builder.WriteString("\n\n")
	builder.WriteString(fmt.Sprintf("产物数量：%d", len(artifacts)))
	textCount := 0
	for _, artifact := range artifacts {
		if artifact.Deleted || artifact.Validity != "effective" {
			continue
		}
		title := artifact.Slot
		if artifact.Caption != nil && strings.TrimSpace(*artifact.Caption) != "" {
			title = strings.TrimSpace(*artifact.Caption)
		}
		if title == "" {
			title = artifact.ID
		}
		if text := externalWorkflowArtifactText(artifact); text != "" {
			textCount++
			builder.WriteString("\n\n")
			builder.WriteString("### ")
			builder.WriteString(title)
			builder.WriteString("\n")
			builder.WriteString(limitRunes(text, 12000))
			continue
		}
		builder.WriteString("\n- ")
		builder.WriteString(title)
		builder.WriteString("（")
		builder.WriteString(firstNonEmpty(artifact.ContentType, "artifact"))
		builder.WriteString("）")
	}
	if textCount == 0 {
		builder.WriteString("\n\n请在右侧 Workflow 面板查看完整产物。")
	}
	return builder.String()
}

func externalWorkflowArtifactText(artifact workflowstore.Artifact) string {
	switch artifact.ContentType {
	case "text", "markdown", "md", "text/markdown", "text/plain":
	default:
		if !strings.HasPrefix(artifact.ContentType, "text/") {
			return ""
		}
	}
	var value any
	if json.Unmarshal(artifact.Value, &value) != nil {
		return strings.TrimSpace(string(artifact.Value))
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		for _, key := range []string{"text", "markdown", "content", "result"} {
			if text, _ := typed[key].(string); strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}

func limitRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "\n\n[内容已截断，请在 Workflow 面板查看完整产物。]"
}

func externalWorkflowRunTerminal(status, reason string, partialOutput bool) json.RawMessage {
	return mustJSON(map[string]any{"status": status, "reason": reason, "partial_output": partialOutput, "model_invoked": false})
}

func externalWorkflowStageLabel(stage string) string {
	labels := map[string]string{
		"resolve_skill":  "安装或复用 Skill",
		"preflight":      "检查执行条件",
		"generate":       "生成 Workflow",
		"prepare":        "准备输入和会话",
		"execute":        "执行 Workflow",
		"collect_result": "整理结果",
	}
	if label := labels[stage]; label != "" {
		return label
	}
	if strings.TrimSpace(stage) == "" {
		return "等待调度"
	}
	return stage
}

func externalWorkflowThinkingSeconds(task orm.ExternalAgentWorkflowTask, now time.Time) int64 {
	if task.CreatedAt.IsZero() || now.Before(task.CreatedAt) {
		return 0
	}
	return int64(now.Sub(task.CreatedAt).Seconds())
}

func externalWorkflowModelRoute(ctx context.Context, db *gorm.DB, userID string) map[string]any {
	resolution, _ := resolveWorkflowModel(ctx, db, userID)
	public := resolution.Public
	provider := stringFromMap(public, "provider")
	model := stringFromMap(public, "model")
	if provider == "" && model == "" {
		return nil
	}
	return map[string]any{
		"mode":          "auto",
		"strategy":      "external_agent_workflow",
		"provider_name": provider,
		"model_name":    model,
		"source":        stringFromMap(public, "source"),
	}
}

func externalWorkflowChatResult(task orm.ExternalAgentWorkflowTask, finalText string) string {
	think := sanitizeExternalWorkflowThinkText(externalWorkflowProgressText(task))
	if strings.TrimSpace(finalText) == "" {
		return "<think>\n" + think + "\n</think>"
	}
	return "<think>\n" + think + "\n</think>\n\n" + strings.TrimSpace(finalText)
}

func sanitizeExternalWorkflowThinkText(value string) string {
	value = strings.ReplaceAll(value, "</think>", "<\\/think>")
	value = strings.ReplaceAll(value, "<think>", "<think >")
	return strings.TrimSpace(value)
}

func externalWorkflowProgressText(task orm.ExternalAgentWorkflowTask) string {
	lines := []string{
		"我正在通过 LazyMind 执行外部 Agent Skill → Workflow 流程。",
		"当前阶段：" + externalWorkflowStageLabel(task.Stage),
	}
	if task.Status != "" {
		lines = append(lines, "任务状态："+externalTaskDisplayStatus(task.Status))
	}
	skill := externalTaskSkillResponse(task)
	if name := stringFromMap(skill, "name"); name != "" {
		status := stringFromMap(skill, "install_status")
		if status == "" {
			status = "等待安装或复用"
		}
		lines = append(lines, fmt.Sprintf("Skill：%s（%s）", name, status))
	}
	if task.DraftID != "" {
		lines = append(lines, "Workflow 草稿已创建："+task.DraftID)
	}
	if task.WorkflowRef != "" {
		lines = append(lines, "Workflow 已发布："+task.WorkflowRef)
	}
	if task.SessionID != "" {
		lines = append(lines, "Workflow 会话已启动："+task.SessionID)
	}
	if task.ErrorCode != "" {
		lines = append(lines, "当前提示："+task.ErrorCode+"，"+firstNonEmpty(task.ErrorMessage, task.Suggestion))
	}
	progress := decodeJSONMap(task.RequestJSON)["progress"]
	if p, ok := progress.(map[string]any); ok {
		completed := intFromAny(p["completed_steps"])
		total := intFromAny(p["total_steps"])
		if total > 0 {
			lines = append(lines, fmt.Sprintf("执行进度：%d/%d 步完成。", completed, total))
		}
		if current := externalWorkflowStringSliceFromAny(p["current_steps"]); len(current) > 0 {
			lines = append(lines, "当前步骤："+strings.Join(current, "、"))
		}
		if blocked := externalWorkflowStringSliceFromAny(p["blocked_steps"]); len(blocked) > 0 {
			lines = append(lines, "阻塞步骤："+strings.Join(blocked, "、"))
		}
	}
	switch task.Status {
	case externalTaskStatusQueued, externalTaskStatusConverting:
		lines = append(lines, "正在等待后台任务完成当前阶段。")
	case externalTaskStatusRunning:
		lines = append(lines, "正在自动推进可执行步骤，并持续收集产物。")
	case externalTaskStatusSucceeded:
		lines = append(lines, "Workflow 已完成，正在展示最终产物。")
	case externalTaskStatusFailed:
		lines = append(lines, "Workflow 执行失败，已停止自动推进。")
	case externalTaskStatusWaitingUserAction:
		lines = append(lines, "Workflow 需要用户在 LazyMind 中继续处理。")
	}
	return strings.Join(lines, "\n")
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		i, _ := typed.Int64()
		return int(i)
	default:
		return 0
	}
}

func externalWorkflowStringSliceFromAny(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, item := range values {
		if text := strings.TrimSpace(fmt.Sprint(item)); text != "" && text != "<nil>" {
			out = append(out, text)
		}
	}
	return out
}

// Called in the task checkpoint transaction. Lock the same history row as
// native step feedback so progress and completion cannot overwrite each other.
func persistExternalWorkflowChat(db *gorm.DB, task orm.ExternalAgentWorkflowTask) error {
	if task.ConversationID == "" {
		return nil
	}
	var history orm.ChatHistory
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND conversation_id=?", externalWorkflowHistoryID(task.ID), task.ConversationID).First(&history).Error
	if errors.Is(err, gorm.ErrRecordNotFound) && task.SessionID == "" {
		return nil
	}
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	runStatus, finalText := "generating", ""
	var terminal json.RawMessage
	switch task.Status {
	case externalTaskStatusSucceeded:
		var artifacts []workflowstore.Artifact
		if err := json.Unmarshal(task.ResultArtifactsJSON, &artifacts); err != nil {
			return err
		}
		runStatus, finalText = "completed", externalWorkflowResultText(artifacts)
		terminal = externalWorkflowRunTerminal(runStatus, "normal", len(artifacts) > 0)
	case externalTaskStatusFailed:
		runStatus = "failed"
		finalText = fmt.Sprintf("LazyMind Workflow 执行失败：%s\n\n%s", firstNonEmpty(task.ErrorMessage, task.ErrorCode), task.Suggestion)
		terminal = externalWorkflowRunTerminal(runStatus, "runtime_failure", strings.Contains(history.Result, "<!-- workflow-step-feedback:"))
	case externalTaskStatusWaitingUserAction:
		runStatus = "interrupted"
		finalText = fmt.Sprintf("LazyMind Workflow 需要你继续处理：%s\n\n%s", firstNonEmpty(task.ErrorMessage, task.ErrorCode), task.Suggestion)
		terminal = externalWorkflowRunTerminal(runStatus, "waiting_user_action", true)
	}
	if err := db.Model(&history).Updates(map[string]any{
		"result": mergeExternalWorkflowChatResult(history.Result, task, finalText),
		"run_id": externalWorkflowRunID(task.ID), "run_status": runStatus, "run_terminal": terminal,
		"thinking_duration_s": externalWorkflowThinkingSeconds(task, now), "update_time": now,
	}).Error; err != nil {
		return err
	}
	return db.Model(&orm.Conversation{}).Where("id=?", task.ConversationID).Update("updated_at", now).Error
}

func mergeExternalWorkflowChatResult(existing string, task orm.ExternalAgentWorkflowTask, finalText string) string {
	const start = "<!-- external-workflow-result -->"
	const end = "<!-- /external-workflow-result -->"
	remainder := strings.TrimSpace(existing)
	if strings.HasPrefix(remainder, "<think>") {
		if _, tail, ok := strings.Cut(remainder, "</think>"); ok {
			remainder = strings.TrimSpace(tail)
		}
	}
	if before, after, ok := strings.Cut(remainder, start); ok {
		if _, tail, closed := strings.Cut(after, end); closed {
			remainder = strings.TrimSpace(before + tail)
		}
	}
	result := externalWorkflowChatResult(task, "")
	if remainder != "" {
		result += "\n\n" + remainder
	}
	if finalText != "" {
		result += "\n\n" + start + "\n" + finalText + "\n" + end
	}
	return result
}
