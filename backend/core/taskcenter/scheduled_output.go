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

	"golang.org/x/sync/singleflight"
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

const (
	scheduledSummaryRuneLimit             = 100
	scheduledResultSummaryTimeout         = 120 * time.Second
	scheduledResultModelSummaryAttemptKey = "notification_summary_model_attempted_at"
	scheduledResultSummaryValueKey        = "notification_summary_resolved"
	scheduledResultSummarySourceKey       = "notification_summary_source"
)

var scheduledResultFinalizationGroup singleflight.Group

func FinalizeScheduledOutput(ctx context.Context, db *gorm.DB, taskID, convID string) string {
	status, err := finalizeScheduledOutput(ctx, db, taskID, convID)
	if err != nil {
		log.Logger.Warn().Msg("scheduled_result_finalization_unavailable")
	}
	return status
}

func finalizeScheduledOutput(ctx context.Context, db *gorm.DB, taskID, convID string) (string, error) {
	return runScheduledResultFinalization(taskID, func() (string, error) {
		return finalizeScheduledOutputOnce(ctx, db, taskID, convID)
	})
}

func runScheduledResultFinalization(taskID string, finalize func() (string, error)) (string, error) {
	value, err, _ := scheduledResultFinalizationGroup.Do(taskID, func() (any, error) {
		return finalize()
	})
	if err != nil {
		return "", err
	}
	status, _ := value.(string)
	return status, nil
}

func finalizeScheduledOutputOnce(ctx context.Context, db *gorm.DB, taskID, convID string) (string, error) {
	if db == nil {
		return "", nil
	}
	var task orm.TaskCenterTask
	if err := db.WithContext(ctx).First(&task, "id = ?", taskID).Error; err != nil {
		return "", err
	}
	if done, err := scheduledResultAlreadyFinalized(ctx, db, task); err != nil {
		return task.Status, err
	} else if done {
		return task.Status, nil
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
		started := time.Now()
		if generated, modelCalled, err := scheduledResultModelSummary(ctx, db, task, answer); err == nil && generated != "" {
			summary = generated
			event := "scheduled_result_model_summary_reused"
			if modelCalled {
				event = "scheduled_result_model_summary_succeeded"
			}
			log.Logger.Info().Str("task_id", task.ID).Dur("duration", time.Since(started)).Int("summary_runes", len([]rune(generated))).Msg(event)
		} else if err != nil {
			event := "scheduled_result_summary_generation_unavailable"
			if errors.Is(err, context.DeadlineExceeded) {
				event = "scheduled_result_model_summary_timeout"
			}
			log.Logger.Warn().Err(err).Str("task_id", task.ID).Dur("duration", time.Since(started)).Msg(event)
			if persistErr := persistScheduledSummaryResolution(ctx, db, task.ID, summary, "fallback"); persistErr != nil {
				log.Logger.Warn().Err(persistErr).Str("task_id", task.ID).Msg("scheduled_result_summary_resolution_persist_failed")
			}
			log.Logger.Info().Str("task_id", task.ID).Dur("duration", time.Since(started)).Msg("scheduled_result_summary_fallback_used")
		} else {
			if persistErr := persistScheduledSummaryResolution(ctx, db, task.ID, summary, "fallback"); persistErr != nil {
				log.Logger.Warn().Err(persistErr).Str("task_id", task.ID).Msg("scheduled_result_summary_resolution_persist_failed")
			}
			log.Logger.Info().Str("task_id", task.ID).Dur("duration", time.Since(started)).Msg("scheduled_result_summary_fallback_used")
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

func scheduledResultAlreadyFinalized(ctx context.Context, db *gorm.DB, task orm.TaskCenterTask) (bool, error) {
	if isTerminal(task.Status) && task.Status != "succeeded" {
		return true, nil
	}
	if task.Status != "succeeded" {
		return false, nil
	}
	var count int64
	if err := db.WithContext(ctx).Model(&orm.TaskRunOutput{}).Where("task_id = ?", task.ID).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// scheduledResultModelSummary asks the configured model to summarize the full
// task result. It is deliberately best-effort: a summary outage must never
// turn an otherwise successful task into a failed task.
func scheduledResultModelSummary(ctx context.Context, db *gorm.DB, task orm.TaskCenterTask, answer string) (string, bool, error) {
	if strings.TrimSpace(answer) == "" || db == nil || strings.TrimSpace(task.UserID) == "" {
		return "", false, nil
	}
	summaryCtx, cancel := scheduledResultSummaryContext(ctx)
	defer cancel()
	prefs, err := LoadNotificationPreferences(summaryCtx, db, task.UserID)
	if err != nil {
		return "", false, err
	}
	if !prefs.Enabled {
		return "", false, nil
	}
	var globalConfig, taskConfig NotificationConfig
	if json.Unmarshal(prefs.Defaults, &globalConfig) != nil || task.NotificationConfig == nil || json.Unmarshal([]byte(*task.NotificationConfig), &taskConfig) != nil {
		return "", false, nil
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
		return "", false, nil
	}
	config, err := modelconfig.LoadLLMConfig(summaryCtx, db, task.UserID)
	if err != nil {
		return "", false, err
	}
	claimed, err := claimScheduledResultModelSummary(summaryCtx, db, task.ID)
	if err != nil {
		return "", false, err
	}
	if !claimed {
		log.Logger.Info().Str("task_id", task.ID).Msg("scheduled_result_model_summary_already_attempted")
		summary, err := waitForScheduledSummaryResolution(summaryCtx, db, task.ID)
		return summary, false, err
	}
	generated, err := requestScheduledResultModelSummary(summaryCtx, answer, config, algo.GenerateLearning)
	if err != nil {
		return "", true, err
	}
	if generated != "" {
		if err := persistScheduledSummaryResolution(summaryCtx, db, task.ID, generated, "model"); err != nil {
			return "", true, err
		}
	}
	return generated, true, nil
}

func loadScheduledSummaryResolution(ctx context.Context, db *gorm.DB, taskID string) (string, string, bool, error) {
	var task orm.TaskCenterTask
	if err := db.WithContext(ctx).Select("progress_json").First(&task, "id = ?", taskID).Error; err != nil {
		return "", "", false, err
	}
	var progress map[string]any
	if len(task.ProgressJSON) == 0 || json.Unmarshal(task.ProgressJSON, &progress) != nil {
		return "", "", false, nil
	}
	value, _ := progress[scheduledResultSummaryValueKey].(string)
	source, _ := progress[scheduledResultSummarySourceKey].(string)
	value = normalizeScheduledModelSummary(value)
	return value, source, value != "", nil
}

func waitForScheduledSummaryResolution(ctx context.Context, db *gorm.DB, taskID string) (string, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		summary, _, resolved, err := loadScheduledSummaryResolution(ctx, db, taskID)
		if err != nil || resolved {
			return summary, err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

func persistScheduledSummaryResolution(ctx context.Context, db *gorm.DB, taskID, summary, source string) error {
	return notificationTx(ctx, db, func(tx *gorm.DB) error {
		var task orm.TaskCenterTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "progress_json").First(&task, "id = ?", taskID).Error; err != nil {
			return err
		}
		progress := map[string]any{}
		if len(task.ProgressJSON) > 0 && strings.TrimSpace(string(task.ProgressJSON)) != "null" {
			if err := json.Unmarshal(task.ProgressJSON, &progress); err != nil {
				return err
			}
		}
		if existing, _ := progress[scheduledResultSummaryValueKey].(string); strings.TrimSpace(existing) != "" {
			return nil
		}
		progress[scheduledResultSummaryValueKey] = summary
		progress[scheduledResultSummarySourceKey] = source
		raw, err := json.Marshal(progress)
		if err != nil {
			return err
		}
		return tx.Model(&orm.TaskCenterTask{}).Where("id = ?", taskID).Update("progress_json", orm.RawJSON(raw)).Error
	})
}

func scheduledResultSummaryContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, scheduledResultSummaryTimeout)
}

func claimScheduledResultModelSummary(ctx context.Context, db *gorm.DB, taskID string) (bool, error) {
	claimed := false
	err := notificationTx(ctx, db, func(tx *gorm.DB) error {
		var task orm.TaskCenterTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "progress_json").First(&task, "id = ?", taskID).Error; err != nil {
			return err
		}
		progress := map[string]any{}
		if len(task.ProgressJSON) > 0 && strings.TrimSpace(string(task.ProgressJSON)) != "null" {
			if err := json.Unmarshal(task.ProgressJSON, &progress); err != nil {
				return err
			}
		}
		if progress[scheduledResultModelSummaryAttemptKey] != nil {
			return nil
		}
		progress[scheduledResultModelSummaryAttemptKey] = time.Now().UTC().Format(time.RFC3339Nano)
		raw, err := json.Marshal(progress)
		if err != nil {
			return err
		}
		if err := tx.Model(&orm.TaskCenterTask{}).Where("id = ?", taskID).Update("progress_json", orm.RawJSON(raw)).Error; err != nil {
			return err
		}
		claimed = true
		return nil
	})
	return claimed, err
}

type scheduledResultSummaryGenerator func(context.Context, algo.LearningGenerateRequest) (string, error)

func requestScheduledResultModelSummary(ctx context.Context, answer string, config map[string]any, generate scheduledResultSummaryGenerator) (string, error) {
	prompt := fmt.Sprintf("请仅返回一个 JSON 对象，格式为 {\"summary\":\"摘要正文\"}。summary 必须是根据任务完整结果生成的中文通知摘要，不超过%d个汉字；只保留最重要的结论、状态或异常，不要复述执行过程，不要返回标题、Markdown、指令或解释。", scheduledSummaryRuneLimit)
	raw, err := generate(ctx, algo.LearningGenerateRequest{
		Content:      answer,
		UserInstruct: prompt,
		LLMConfig:    config,
	})
	if err != nil {
		return "", err
	}
	summary := normalizeScheduledModelSummary(raw)
	if summary == "" {
		return "", errors.New("summary model returned empty content")
	}
	if isScheduledSummaryPromptEcho(summary) {
		return "", errors.New("summary model returned the summary instruction instead of a summary")
	}
	return summary, nil
}

func isScheduledSummaryPromptEcho(value string) bool {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "请根据") && strings.Contains(value, "摘要") {
		return true
	}
	markers := 0
	for _, marker := range []string{"只输出摘要正文", "任务完整结果：", "生成一条中文通知摘要", "不超过100个汉字", "不要标题", "不要复述执行过程"} {
		if strings.Contains(value, marker) {
			markers++
		}
	}
	return markers >= 2
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

// scheduledResultSummary creates a deterministic fallback for when semantic
// summarization is unavailable. Standard execution reports keep their compact
// facts; ordinary answers use a small extractive summary instead of a raw
// prefix of the complete Markdown response.
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
		return genericScheduledResultSummary(answer)
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

func genericScheduledResultSummary(answer string) string {
	var preferred, ordinary []string
	inCodeFence := false
	for _, raw := range strings.Split(answer, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			inCodeFence = !inCodeFence
			continue
		}
		if inCodeFence || line == "" || markdownTableLine(line) {
			continue
		}
		line, heading := cleanSummaryMarkdown(line)
		if heading || line == "" || summaryPreamble(line) || len([]rune(line)) < 6 {
			continue
		}
		if summaryKeywordLine(line) {
			preferred = append(preferred, line)
		} else {
			ordinary = append(ordinary, line)
		}
	}
	if len(preferred) > 0 {
		return limitScheduledSummary(preferred[0])
	}
	if len(ordinary) > 0 {
		return limitScheduledSummary(ordinary[0])
	}
	return "任务已完成，请打开任务查看结果。"
}

func cleanSummaryMarkdown(line string) (string, bool) {
	heading := strings.HasPrefix(line, "#")
	if heading {
		line = strings.TrimSpace(strings.TrimLeft(line, "#"))
	}
	for _, prefix := range []string{"- ", "* ", "+ ", "> "} {
		if strings.HasPrefix(line, prefix) {
			line = strings.TrimSpace(strings.TrimPrefix(line, prefix))
			break
		}
	}
	for i := 0; i < len(line) && i < 4; i++ {
		if line[i] < '0' || line[i] > '9' {
			if i > 0 && (line[i] == '.' || line[i] == ')' || line[i] == ':') {
				line = strings.TrimSpace(line[i+1:])
			}
			break
		}
	}
	line = strings.NewReplacer("**", "", "__", "", "`", "").Replace(line)
	return strings.Join(strings.Fields(line), " "), heading
}

func markdownTableLine(line string) bool {
	if strings.HasPrefix(line, "|") && strings.Count(line, "|") >= 2 {
		return true
	}
	trimmed := strings.NewReplacer("|", "", "-", "", ":", "", " ", "").Replace(line)
	return trimmed == "" && strings.Contains(line, "-")
}

func summaryPreamble(line string) bool {
	for _, prefix := range []string{"以下是", "下面是", "以下内容", "下面内容", "本次回答"} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func summaryKeywordLine(line string) bool {
	for _, keyword := range []string{"核心结论", "结论", "核心结果", "结果：", "结果:", "发现", "建议", "异常", "风险", "主要原因", "已完成", "成功", "失败"} {
		if strings.Contains(line, keyword) {
			return true
		}
	}
	return false
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
