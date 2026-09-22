package taskcenter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
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
	if task.TaskType != "scheduled" || task.ScheduleID == nil || task.NotificationConfig == nil || task.ArchivedAt != nil {
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
			ActionID string `json:"pending_action_id"`
			Action   string `json:"pending_action"`
		}
		if err := json.Unmarshal(task.ProgressJSON, &progress); err != nil {
			return nil
		}
		if progress.ActionID == "" || strings.TrimSpace(progress.Action) == "" {
			return nil
		}
		body, occurrence = progress.Action, "waiting:"+progress.ActionID
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
		if reason := notificationBlockReason(prefs, channel); reason != "" {
			notice.Status, notice.Reason = "skipped", reason
		}
		if err := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&notice).Error; err != nil {
			return err
		}
	}
	return nil
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
	var tasks []orm.TaskCenterTask
	if err := tx.Where("user_id = ? AND task_type = 'scheduled' AND status IN ('pending','running','waiting','waiting_inputs') AND archived_at IS NULL AND notification_config IS NOT NULL", userID).Order("id").Find(&tasks).Error; err != nil {
		return nil, err
	}
	ids := []string{}
	for _, task := range tasks {
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
