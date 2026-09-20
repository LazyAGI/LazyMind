package taskcenter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/modelconfig"
)

type artifactManifestItem struct {
	ArtifactID   string `json:"artifact_id"`
	Name         string `json:"name"`
	MIMEType     string `json:"mime_type"`
	SourceTaskID string `json:"source_task_id"`
	Revision     int    `json:"revision"`
}

const scheduledSummaryRuneLimit = 100

func FinalizeScheduledOutput(ctx context.Context, db *gorm.DB, taskID, convID string) string {
	status, err := finalizeScheduledOutput(ctx, db, taskID, convID)
	if err != nil {
		log.Logger.Warn().Msg("scheduled_result_finalization_unavailable")
	}
	return status
}

func finalizeScheduledOutput(ctx context.Context, db *gorm.DB, taskID, convID string) (string, error) {
	if db == nil {
		return "", nil
	}
	var history orm.ChatHistory
	if err := db.WithContext(ctx).Where("conversation_id = ?", convID).Order("seq DESC").First(&history).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "failed", errors.Join(err, UpdateTaskFailure(ctx, db, taskID, "最终结果保存失败，请重试任务"))
	}
	if history.RunStatus == "failed" || history.RunStatus == "interrupted" {
		return "failed", UpdateTaskFailure(ctx, db, taskID, "任务执行失败，请打开任务查看详情。")
	}
	if history.RunStatus == "cancelled" {
		return "canceled", UpdateTaskStatus(ctx, db, taskID, "canceled")
	}
	var ext struct {
		Answered bool `json:"ask_answered"`
		Pending  struct {
			ID        string `json:"ask_id"`
			Title     string `json:"title"`
			Questions []struct {
				Text string `json:"text"`
			} `json:"questions"`
		} `json:"ask_pending"`
	}
	if json.Unmarshal(history.Ext, &ext) == nil && ext.Pending.ID != "" && !ext.Answered {
		action := ext.Pending.Title
		if len(ext.Pending.Questions) > 0 {
			action = ext.Pending.Questions[0].Text
		}
		if action == "" {
			action = "请打开任务，确认待处理事项。"
		}
		if err := WaitScheduledTask(ctx, db, taskID, ext.Pending.ID, action); err != nil {
			return "", err
		}
		return "waiting", nil
	}
	var task orm.TaskCenterTask
	if err := db.WithContext(ctx).First(&task, "id = ?", taskID).Error; err != nil {
		return "", err
	}
	if task.WorkflowSessionID != nil && *task.WorkflowSessionID != "" {
		var session orm.WorkflowSession
		if err := db.WithContext(ctx).First(&session, "id = ?", *task.WorkflowSessionID).Error; err != nil {
			return task.Status, err
		}
		if session.Status == "active" || session.Status == "waiting" {
			return session.Status, nil
		}
		if session.Status == "failed" {
			return "failed", UpdateTaskFailure(ctx, db, taskID, "任务执行失败，请打开任务查看详情。")
		}
	}
	manifest := make([]artifactManifestItem, 0)
	var convArts []orm.ConversationArtifact
	if err := db.WithContext(ctx).Where("conversation_id = ?", convID).Order("created_at ASC").Find(&convArts).Error; err != nil {
		return "failed", errors.Join(err, UpdateTaskFailure(ctx, db, taskID, "最终结果保存失败，请重试任务"))
	}
	for _, a := range convArts {
		manifest = append(manifest, artifactManifestItem{ArtifactID: a.ID, Name: a.Filename, MIMEType: a.ContentType, SourceTaskID: taskID, Revision: 1})
	}
	var subArts []struct {
		ID, Slot, ContentType string
		Seq                   int
	}
	if err := db.WithContext(ctx).Table("sub_agent_artifacts sa").Select("sa.id, sa.slot, sa.content_type, sa.seq").Joins("JOIN sub_agent_tasks st ON st.id = sa.task_id").Where("st.conversation_id = ? AND sa.hidden = false", convID).Order("sa.created_at ASC").Scan(&subArts).Error; err != nil {
		return "failed", errors.Join(err, UpdateTaskFailure(ctx, db, taskID, "最终结果保存失败，请重试任务"))
	}
	for _, a := range subArts {
		manifest = append(manifest, artifactManifestItem{ArtifactID: a.ID, Name: a.Slot, MIMEType: a.ContentType, SourceTaskID: taskID, Revision: a.Seq})
	}
	manifestJSON, _ := json.Marshal(manifest)
	answer := TaskOutputBody(history.Result)
	status := "ready"
	if answer == "" && len(manifest) == 0 {
		status = "empty"
	}
	h := sha256.Sum256(append([]byte(answer), manifestJSON...))
	now := time.Now().UTC()
	summary := scheduledResultSummary(answer)
	if shouldGenerateScheduledResultModelSummary(task) {
		if generated, err := scheduledResultModelSummary(ctx, db, task, answer); err == nil && generated != "" {
			summary = generated
		} else if err != nil {
			log.Logger.Warn().Err(err).Msg("scheduled_result_summary_generation_unavailable")
		}
	}
	out := orm.TaskRunOutput{ID: common.GeneratePrefixedID("out_", 36), TaskID: taskID, ConversationID: convID, FinalAnswerText: answer, SummaryText: summary, ArtifactManifestJSON: manifestJSON, OutputStatus: status, ContentHash: hex.EncodeToString(h[:]), CreatedAt: now, UpdatedAt: now}
	err := notificationTx(ctx, db, func(tx *gorm.DB) error {
		var current orm.TaskCenterTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id = ?", taskID).Error; err != nil {
			return err
		}
		if current.ArchivedAt != nil {
			return nil
		}
		if isTerminal(current.Status) {
			if current.Status == "succeeded" {
				// Legacy dependency materialization must not replay notifications or
				// replace an output that has already been finalized.
				return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&out).Error
			}
			return nil
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "task_id"}}, DoUpdates: clause.AssignmentColumns([]string{
			"conversation_id", "final_answer_text", "summary_text", "artifact_manifest_json", "output_status", "content_hash", "updated_at",
		})}).Create(&out).Error; err != nil {
			return err
		}
		if status == "ready" {
			return UpdateTaskStatus(ctx, tx, taskID, "succeeded")
		}
		return UpdateTaskFailure(ctx, tx, taskID, "聊天服务未生成可用结果")
	})
	if err != nil {
		var notificationErr *notificationPersistenceError
		if errors.As(err, &notificationErr) {
			// History is already durable. Leave this run recoverable without
			// turning a notification storage outage into a business failure.
			return task.Status, err
		}
		failureErr := UpdateTaskFailure(ctx, db, taskID, "最终结果保存失败，请重试任务")
		return "failed", errors.Join(err, failureErr)
	}
	return status, nil
}

// scheduledResultModelSummary asks the configured model to summarize the full
// task result. It is deliberately best-effort: a summary outage must never
// turn an otherwise successful task into a failed task.
func scheduledResultModelSummary(ctx context.Context, db *gorm.DB, task orm.TaskCenterTask, answer string) (string, error) {
	if strings.TrimSpace(answer) == "" || db == nil || strings.TrimSpace(task.UserID) == "" {
		return "", nil
	}
	prefs, err := LoadNotificationPreferences(ctx, db, task.UserID)
	if err != nil {
		return "", err
	}
	if !prefs.Enabled {
		return "", nil
	}
	var globalConfig, taskConfig NotificationConfig
	if json.Unmarshal(prefs.Defaults, &globalConfig) != nil || task.NotificationConfig == nil || json.Unmarshal([]byte(*task.NotificationConfig), &taskConfig) != nil {
		return "", nil
	}
	hasDeliverableChannel := false
	for channel, target := range taskConfig.Channels {
		globalChannel, ok := globalConfig.Channels[channel]
		if target.Enabled && ok && globalChannel.Enabled {
			hasDeliverableChannel = true
			break
		}
	}
	if !hasDeliverableChannel {
		return "", nil
	}
	config, err := modelconfig.LoadLLMConfig(ctx, db, task.UserID)
	if err != nil {
		return "", err
	}
	requestCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return requestScheduledResultModelSummary(requestCtx, answer, config, algo.GeneratePolish)
}

type scheduledResultSummaryGenerator func(context.Context, algo.PolishGenerateRequest) (string, error)

func requestScheduledResultModelSummary(ctx context.Context, answer string, config map[string]any, generate scheduledResultSummaryGenerator) (string, error) {
	prompt := fmt.Sprintf("请根据任务完整结果生成一条中文通知摘要。只输出摘要正文，不要标题、引号、Markdown 或解释；必须不超过%d个汉字，只保留最重要的结论、状态或异常，不要复述执行过程。", scheduledSummaryRuneLimit)
	raw, err := generate(ctx, algo.PolishGenerateRequest{
		Content:      answer,
		UserInstruct: prompt,
		LLMConfig:    config,
	})
	if err != nil {
		return "", err
	}
	return normalizeScheduledModelSummary(raw), nil
}

func shouldGenerateScheduledResultModelSummary(task orm.TaskCenterTask) bool {
	if task.NotificationConfig == nil || strings.TrimSpace(*task.NotificationConfig) == "" {
		return false
	}
	var config NotificationConfig
	if json.Unmarshal([]byte(*task.NotificationConfig), &config) != nil {
		return false
	}
	rule, ok := config.Events["succeeded"]
	if !ok || !rule.Enabled || rule.Content != "summary" {
		return false
	}
	for _, channel := range config.Channels {
		if channel.Enabled {
			return true
		}
	}
	return false
}

func normalizeScheduledModelSummary(raw string) string {
	value := strings.TrimSpace(raw)
	if strings.HasPrefix(value, "```") {
		if firstLine := strings.IndexByte(value, '\n'); firstLine >= 0 {
			value = value[firstLine+1:]
		} else {
			value = strings.TrimPrefix(value, "```")
		}
		value = strings.TrimSuffix(strings.TrimSpace(value), "```")
	}
	value = strings.TrimSpace(value)
	var object struct {
		Summary string `json:"summary"`
	}
	if json.Unmarshal([]byte(value), &object) == nil && strings.TrimSpace(object.Summary) != "" {
		value = object.Summary
	}
	for _, prefix := range []string{"摘要：", "摘要:", "summary:", "Summary:"} {
		value = strings.TrimSpace(strings.TrimPrefix(value, prefix))
	}
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return ""
	}
	if len([]rune(value)) > scheduledSummaryRuneLimit {
		return string([]rune(value)[:scheduledSummaryRuneLimit-1]) + "…"
	}
	return value
}

// scheduledResultSummary creates the short, structured notification summary.
// It intentionally never copies an arbitrary paragraph from the model output:
// the full answer remains available in the task detail view.
func scheduledResultSummary(answer string) string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return ""
	}
	var status, executionTime, duration, success, failed string
	foundStructured := false
	for _, line := range strings.Split(answer, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "任务执行") || strings.HasPrefix(line, "任务状态"):
			foundStructured = true
			if strings.Contains(line, "失败") {
				status = "任务失败"
			} else if strings.Contains(line, "成功") {
				status = "任务成功"
			} else if status == "" {
				prefix := "任务执行"
				if strings.HasPrefix(line, "任务状态") {
					prefix = "任务状态"
				}
				status = compactValue(line, prefix)
			}
		case strings.HasPrefix(line, "执行时间"):
			foundStructured = true
			executionTime = compactExecutionTime(compactValue(line, "执行时间"))
		case strings.HasPrefix(line, "执行耗时") || strings.HasPrefix(line, "耗时"):
			foundStructured = true
			duration = compactValue(line, "执行耗时")
			if duration == line {
				duration = compactValue(line, "耗时")
			}
		case strings.HasPrefix(line, "成功步骤"):
			foundStructured = true
			success = compactValue(line, "成功步骤")
		case strings.HasPrefix(line, "失败步骤"):
			foundStructured = true
			failed = compactValue(line, "失败步骤")
		}
	}
	if !foundStructured {
		return limitScheduledSummary(strings.Join(strings.Fields(answer), " "))
	}
	if status == "" {
		status = "任务完成"
	}
	parts := []string{status}
	if executionTime != "" {
		parts = append(parts, "时间："+executionTime)
	}
	if duration != "" {
		parts = append(parts, "耗时："+duration)
	}
	if success != "" || failed != "" {
		steps := "步骤："
		if success != "" {
			steps += "成功" + success
		}
		if failed != "" {
			if success != "" {
				steps += "/"
			}
			steps += "失败" + failed
		}
		parts = append(parts, steps)
	}
	result := strings.Join(parts, "；")
	return limitScheduledSummary(result)
}

func limitScheduledSummary(value string) string {
	if len([]rune(value)) > scheduledSummaryRuneLimit {
		return string([]rune(value)[:scheduledSummaryRuneLimit-1]) + "…"
	}
	return value
}

func compactValue(line, prefix string) string {
	value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	value = strings.TrimSpace(strings.TrimLeft(value, ":："))
	return value
}

func compactExecutionTime(value string) string {
	value = strings.ReplaceAll(value, "T", " ")
	if len(value) >= 16 {
		return value[:16]
	}
	return value
}

// Recover committed history/terminal workflow results after interrupted callbacks.
// Keyset scanning bounds each pass and keeps an old active task from starving others.
func ReconcileScheduledNotifications(ctx context.Context, db *gorm.DB, after string, now time.Time) (string, error) {
	var tasks []orm.TaskCenterTask
	if err := db.WithContext(ctx).Where("task_type = 'scheduled' AND status IN ('running','waiting') AND archived_at IS NULL AND id > ?", after).Order("id").Limit(100).Find(&tasks).Error; err != nil {
		return after, err
	}
	var recoveryErr error
	for _, task := range tasks {
		if task.WorkflowSessionID != nil && *task.WorkflowSessionID != "" {
			var session orm.WorkflowSession
			if err := db.WithContext(ctx).First(&session, "id = ?", *task.WorkflowSessionID).Error; err != nil {
				recoveryErr = errors.Join(recoveryErr, err)
				continue
			}
			switch session.Status {
			case "failed":
				if err := UpdateTaskFailure(ctx, db, task.ID, "任务执行失败，请打开任务查看详情。"); err != nil {
					recoveryErr = errors.Join(recoveryErr, err)
				}
				continue
			case "completed":
				_, err := finalizeScheduledOutput(ctx, db, task.ID, task.ConversationID)
				recoveryErr = errors.Join(recoveryErr, err)
				continue
			case "waiting":
				var step orm.WorkflowSessionStep
				result := db.WithContext(ctx).Where("session_id = ? AND step_id = ?", session.ID, session.CurrentStepID).Order("created_at DESC").Limit(1).Find(&step)
				if result.Error != nil {
					recoveryErr = errors.Join(recoveryErr, result.Error)
					continue
				}
				if step.ID != "" && (session.WorkflowMode == "dynamic" || step.Status == "interrupted") {
					if err := WaitScheduledTask(ctx, db, task.ID, step.ID, "请打开任务，确认当前步骤并继续执行。"); err != nil {
						recoveryErr = errors.Join(recoveryErr, err)
					}
				}
				continue
			}
		}
		var history orm.ChatHistory
		result := db.WithContext(ctx).Where("conversation_id = ?", task.ConversationID).Order("seq DESC").Limit(1).Find(&history)
		if result.Error != nil {
			recoveryErr = errors.Join(recoveryErr, result.Error)
			continue
		}
		if history.ID != "" && (history.RunStatus == "completed" || history.RunStatus == "failed" || history.RunStatus == "cancelled" || history.RunStatus == "interrupted") {
			_, err := finalizeScheduledOutput(ctx, db, task.ID, task.ConversationID)
			recoveryErr = errors.Join(recoveryErr, err)
		} else if task.Status == "running" && now.Sub(task.CreatedAt) > 2*time.Hour {
			if err := UpdateTaskFailure(ctx, db, task.ID, "任务执行超时（超过2小时）"); err != nil {
				recoveryErr = errors.Join(recoveryErr, err)
			}
		}
	}
	if len(tasks) == 100 {
		return tasks[len(tasks)-1].ID, recoveryErr
	}
	return "", recoveryErr
}
