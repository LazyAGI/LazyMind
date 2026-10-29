package taskcenter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
)

var taskOutputProcessBoundary = regexp.MustCompile(`(?is)</(?:think|tp|trp|tool_call|tool_result)\s*>`)
var unfinishedTaskProtocol = regexp.MustCompile(`(?is)<(?:think|tp|trp|tool_call|tool_result)\b[^>]*>.*$`)

// TaskOutputBody matches the final-result boundary used by schedule dependencies.
func TaskOutputBody(result string) string {
	boundaries := taskOutputProcessBoundary.FindAllStringIndex(result, -1)
	if len(boundaries) > 0 {
		result = result[boundaries[len(boundaries)-1][1]:]
	}
	return strings.TrimSpace(strings.ReplaceAll(unfinishedTaskProtocol.ReplaceAllString(result, ""), "\x00", ""))
}

func notificationTx(ctx context.Context, db *gorm.DB, fn func(*gorm.DB) error) error {
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return fn(db.WithContext(ctx))
	}
	return common.TransactionWithSQLiteBusyRetry(ctx, db, fn)
}

func notificationIdentity(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(hash[:])
}

func notificationPreview(text string, limit int) string {
	text = TaskOutputBody(text)
	if len([]rune(text)) <= limit {
		return text
	}
	return string([]rune(text)[:limit-1]) + "…"
}

// Keep notification failures distinguishable from failures of task execution.
// The caller must roll back and retry finalization, never rerun the task.
type notificationPersistenceError struct{ cause error }

func (e *notificationPersistenceError) Error() string {
	// Preserve SQLite's transaction retry classification without exposing
	// driver diagnostics through this internal error category.
	if common.IsSQLiteBusy(e.cause) {
		return "task notification persistence unavailable: sqlite_busy"
	}
	return "task notification persistence unavailable"
}
func (e *notificationPersistenceError) Unwrap() error { return e.cause }

// Called in the same transaction as task state and final output persistence.
func persistTaskNotifications(ctx context.Context, tx *gorm.DB, id string) (resultErr error) {
	defer func() {
		if resultErr != nil {
			resultErr = &notificationPersistenceError{cause: resultErr}
		}
	}()
	var task orm.TaskCenterTask
	if err := tx.WithContext(ctx).First(&task, "id = ?", id).Error; err != nil {
		return err
	}
	if task.ArchivedAt != nil {
		return nil
	}
	if task.TaskType != "scheduled" || task.NotificationConfig == nil {
		return persistDesktopTaskCompletion(ctx, tx, task)
	}
	if task.ScheduleID == nil || task.NotificationConfig == nil {
		return nil
	}
	var config NotificationConfig
	if err := json.Unmarshal([]byte(*task.NotificationConfig), &config); err != nil {
		return err
	}
	event := task.Status
	rule, ok := config.Events[event]
	if !ok || !rule.Enabled {
		return nil
	}
	body, occurrence := "", event
	eventAt := task.FinishedAt
	if isTerminal(event) && eventAt != nil {
		occurrence += ":" + eventAt.UTC().Format(time.RFC3339Nano)
	}
	switch event {
	case "succeeded":
		var output orm.TaskRunOutput
		if err := tx.WithContext(ctx).Where("task_id = ? AND output_status = ?", task.ID, "ready").First(&output).Error; err != nil {
			return err
		}
		body = output.FinalAnswerText
		if rule.Content == "summary" && strings.TrimSpace(output.SummaryText) != "" {
			body = output.SummaryText
		}
		if strings.TrimSpace(body) == "" {
			body = "任务已生成结果文件，请打开任务查看。"
		}
	case "failed":
		body = "任务执行失败，请打开任务查看详情。"
		// Failure details may contain provider diagnostics. Expose only scheduler-owned messages.
		var progress map[string]any
		if json.Unmarshal(task.ProgressJSON, &progress) == nil {
			if reason, ok := progress["failure_reason"].(string); ok && safeScheduledFailure(reason) {
				body = reason
			}
		}
	case "waiting":
		var progress struct {
			ActionID string     `json:"pending_action_id"`
			Action   string     `json:"pending_action"`
			At       *time.Time `json:"pending_action_at"`
		}
		if err := json.Unmarshal(task.ProgressJSON, &progress); err != nil {
			return nil
		}
		if progress.ActionID == "" || strings.TrimSpace(progress.Action) == "" {
			return nil
		}
		body, occurrence = progress.Action, "waiting:"+progress.ActionID
		eventAt = progress.At
	default:
		return nil
	}
	prefs, err := LoadNotificationPreferences(ctx, tx, task.UserID)
	if err != nil {
		return err
	}
	title := "定时任务"
	if task.Title != nil && strings.TrimSpace(*task.Title) != "" {
		title = *task.Title
	}
	for channel, target := range config.Channels {
		if !target.Enabled {
			continue
		}
		content := TaskOutputBody(body)
		if rule.Content == "summary" {
			content = notificationPreview(content, 1000)
		}
		if channel == "desktop" {
			content = notificationPreview(content, 200)
		}
		if len([]rune(content)) > 262000 {
			content = notificationPreview(content, 262000) + "\n内容较长，请在 LazyMind 打开任务查看完整结果。"
		}
		now := time.Now().UTC()
		notice := orm.TaskNotification{
			ID:     notificationIdentity(task.ID, occurrence, channel, target.AccountID, target.RecipientID),
			UserID: task.UserID, TaskID: task.ID, ScheduleID: *task.ScheduleID,
			EventID: notificationIdentity(task.ID, occurrence), Event: event, Channel: channel,
			AccountID: target.AccountID, RecipientID: target.RecipientID, ConfigRevision: task.NotificationRevision,
			Title: notificationPreview(title, 200), Body: content, Content: rule.Content, Status: "pending",
			CreatedAt: now, UpdatedAt: now,
		}
		if reason := completionNotificationBlockReason(prefs, channel, eventAt); reason != "" {
			notice.Status, notice.Reason = "skipped", reason
		} else if channel != "desktop" && (strings.TrimSpace(target.AccountID) == "" || strings.TrimSpace(target.RecipientID) == "") {
			notice.Status, notice.Reason = "skipped", "NOTIFICATION_TARGET_REQUIRED"
		}
		if err := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&notice).Error; err != nil {
			return err
		}
	}
	return nil
}

// Completions without a scheduled policy use the desktop gate only. Global
// external defaults never select recipients for these tasks.
func persistDesktopTaskCompletion(ctx context.Context, tx *gorm.DB, task orm.TaskCenterTask) error {
	if (task.TaskType != "workflow_run" && task.TaskType != "background_chat" && task.TaskType != "scheduled") ||
		(task.Status != "succeeded" && task.Status != "failed") {
		return nil
	}
	prefs, err := LoadNotificationPreferences(ctx, tx, task.UserID)
	if err != nil {
		return err
	}
	title, body := "任务", "任务已完成，请打开任务查看结果。"
	if task.Title != nil && strings.TrimSpace(*task.Title) != "" {
		title = *task.Title
	}
	if task.Status == "failed" {
		body = "任务执行失败，请打开任务查看详情。"
	}
	now := time.Now().UTC()
	occurrence := task.Status
	if task.FinishedAt != nil {
		occurrence += ":" + task.FinishedAt.UTC().Format(time.RFC3339Nano)
	}
	notice := orm.TaskNotification{
		ID:     notificationIdentity(task.ID, occurrence, "desktop", "", ""),
		UserID: task.UserID, TaskID: task.ID, EventID: notificationIdentity(task.ID, occurrence),
		Event: task.Status, Channel: "desktop", ConfigRevision: prefs.Revision,
		Title: notificationPreview(title, 200), Body: body, Content: "summary", Status: "pending",
		CreatedAt: now, UpdatedAt: now,
	}
	if task.ScheduleID != nil {
		notice.ScheduleID = *task.ScheduleID
	}
	if reason := completionNotificationBlockReason(prefs, "desktop", task.FinishedAt); reason != "" {
		notice.Status, notice.Reason = "skipped", reason
	}
	return tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&notice).Error
}

func notificationBlockReason(prefs orm.UserNotificationPreferences, channel string) string {
	if !prefs.Enabled {
		return "NOTIFICATIONS_DISABLED"
	}
	// Channel defaults are evaluated when an unexecuted notification is queued;
	// once a task starts, its notification config is already snapshotted.
	var globalConfig NotificationConfig
	if json.Unmarshal(prefs.Defaults, &globalConfig) != nil {
		return "NOTIFICATION_SETTINGS_INVALID"
	}
	globalChannel, ok := globalConfig.Channels[channel]
	if !ok || !globalChannel.Enabled {
		return "NOTIFICATION_CHANNEL_DISABLED"
	}
	return ""
}

func safeScheduledFailure(reason string) bool {
	return reason == "聊天服务未生成可用结果" || reason == "最终结果保存失败，请重试任务" ||
		reason == "任务执行超时（超过2小时）" || reason == "任务执行被中断" ||
		reason == "任务请求失败，请稍后重试" || regexp.MustCompile(`^任务请求失败：服务返回 HTTP [1-5][0-9]{2}$`).MatchString(reason)
}

func activeNotificationRuns(tx *gorm.DB, userID string) ([]string, error) {
	prefs, err := ReadNotificationPreferences(tx.Statement.Context, tx, userID)
	if err != nil {
		return nil, err
	}
	desktopEnabled := notificationBlockReason(prefs, "desktop") == ""
	var tasks []orm.TaskCenterTask
	if err := tx.Where("user_id = ? AND task_type IN ? AND status IN ('pending','running','waiting','waiting_inputs') AND archived_at IS NULL", userID,
		[]string{"scheduled", "background_chat", "workflow_run"}).Order("id").Find(&tasks).Error; err != nil {
		return nil, err
	}
	ids := []string{}
	for _, task := range tasks {
		if task.TaskType != "scheduled" || task.NotificationConfig == nil {
			if desktopEnabled {
				ids = append(ids, task.ID)
			}
			continue
		}
		config, err := notificationConfigValue(task.NotificationConfig)
		if err != nil {
			return nil, err
		}
		for _, channel := range config.Channels {
			if channel.Enabled {
				ids = append(ids, task.ID)
				break
			}
		}
	}
	return ids, nil
}

func notificationConfigValue(raw *string) (*NotificationConfig, error) {
	if raw == nil {
		return nil, nil
	}
	var config NotificationConfig
	if err := json.Unmarshal([]byte(*raw), &config); err != nil {
		return nil, err
	}
	return &config, nil
}

// WaitScheduledTask records only an explicit user action, never dependency or tool progress.
func WaitScheduledTask(ctx context.Context, db *gorm.DB, taskID, actionID, action string) error {
	return waitScheduledTaskAt(ctx, db, taskID, actionID, action, time.Now().UTC())
}

func waitScheduledTaskAt(ctx context.Context, db *gorm.DB, taskID, actionID, action string, occurredAt time.Time) error {
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	return notificationTx(ctx, db, func(tx *gorm.DB) error {
		var task orm.TaskCenterTask
		if err := tx.First(&task, "id = ?", taskID).Error; err != nil {
			return err
		}
		if task.TaskType != "scheduled" || isTerminal(task.Status) || task.ArchivedAt != nil {
			return nil
		}
		progress := map[string]any{}
		if len(task.ProgressJSON) > 0 {
			if err := json.Unmarshal(task.ProgressJSON, &progress); err != nil {
				return err
			}
		}
		if task.Status == "waiting" && progress["pending_action_id"] == actionID {
			return nil
		}
		progress["pending_action_id"], progress["pending_action"] = actionID, notificationPreview(action, 1000)
		progress["pending_action_at"] = occurredAt
		raw, err := json.Marshal(progress)
		if err != nil {
			return err
		}
		if err := tx.Model(&task).Updates(map[string]any{"status": "waiting", "progress_json": orm.RawJSON(raw), "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		return persistTaskNotifications(ctx, tx, taskID)
	})
}

func FinalizeScheduledConversation(ctx context.Context, db *gorm.DB, conversationID string) error {
	var tasks []orm.TaskCenterTask
	if err := db.WithContext(ctx).Where("conversation_id = ? AND task_type = 'scheduled' AND status IN ('running','waiting') AND archived_at IS NULL", conversationID).Find(&tasks).Error; err != nil {
		return err
	}
	var resultErr error
	for _, task := range tasks {
		_, err := finalizeScheduledOutput(ctx, db, task.ID, conversationID)
		resultErr = errors.Join(resultErr, err)
	}
	return resultErr
}

func disabledNotificationChannels(config NotificationConfig) []string {
	disabled := make([]string, 0, 4)
	for _, channel := range []string{"desktop", "feishu", "wecom", "wechat"} {
		globalChannel, ok := config.Channels[channel]
		if !ok || !globalChannel.Enabled {
			disabled = append(disabled, channel)
		}
	}
	return disabled
}

// NotifyAcademicExpansion records one pending action per preparation revision.
// Read/dismissal is independent of desktop delivery and of the business state.
func NotifyAcademicExpansion(ctx context.Context, tx *gorm.DB, user, dataset, expansion string, revision int64, status string) error {
	event, title, body := "paper_ready", "初步调研已完成", "已整理研究方向，确认后即可开始论文拓展。"
	if status == "awaiting_info" {
		event, title, body = "paper_info", "论文拓展需要补充信息", "请打开拓展记录，补充影响研究范围的信息。"
	} else if status != "awaiting_confirmation" {
		return nil
	}
	target, _ := json.Marshal(map[string]string{"type": "academic_expansion", "dataset_id": dataset, "expansion_id": expansion})
	now := time.Now().UTC()
	identity := notificationIdentity(expansion, strconv.FormatInt(revision, 10), event)
	notice := orm.TaskNotification{ID: identity, UserID: user, EventID: identity, Event: event, Channel: "desktop", Title: title, Body: body, Content: "summary", Status: "pending", TargetJSON: orm.RawJSON(target), CreatedAt: now, UpdatedAt: now}
	var prefs orm.UserNotificationPreferences
	if err := tx.Where("user_id=?", user).Take(&prefs).Error; err == nil && notificationBlockReason(prefs, "desktop") != "" {
		notice.Status = "suppressed"
	}
	return tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&notice).Error
}

func ResolveAcademicExpansionNotifications(ctx context.Context, tx *gorm.DB, expansion string, revision int64) error {
	ids := []string{notificationIdentity(expansion, strconv.FormatInt(revision, 10), "paper_info"), notificationIdentity(expansion, strconv.FormatInt(revision, 10), "paper_ready")}
	return tx.WithContext(ctx).Model(&orm.TaskNotification{}).Where("id IN ? AND read_at IS NULL", ids).Update("read_at", time.Now().UTC()).Error
}
