package taskcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
)

type NotificationEventRule struct {
	Enabled bool   `json:"enabled"`
	Content string `json:"content"`
}
type NotificationChannelRule struct {
	Enabled     bool   `json:"enabled"`
	AccountID   string `json:"account_id,omitempty"`
	RecipientID string `json:"recipient_id,omitempty"`
}
type NotificationConfig struct {
	Events   map[string]NotificationEventRule   `json:"events"`
	Channels map[string]NotificationChannelRule `json:"channels"`
}

func notificationPreferencesView(row orm.UserNotificationPreferences) map[string]any {
	return map[string]any{"enabled": row.Enabled, "revision": row.Revision, "defaults": json.RawMessage(row.Defaults)}
}

func DefaultNotificationConfig() NotificationConfig {
	return NotificationConfig{
		Events:   map[string]NotificationEventRule{"succeeded": {true, "summary"}, "failed": {true, "summary"}, "waiting": {false, "summary"}},
		Channels: map[string]NotificationChannelRule{"desktop": {Enabled: true}},
	}
}

type notificationError struct {
	status   int
	reason   string
	affected []string
}

func (e *notificationError) Error() string { return e.reason }
func notificationProblem(status int, reason string) error {
	return &notificationError{status: status, reason: reason}
}

func LoadNotificationPreferences(ctx context.Context, db *gorm.DB, userID string) (orm.UserNotificationPreferences, error) {
	defaults, err := json.Marshal(DefaultNotificationConfig())
	if err != nil {
		return orm.UserNotificationPreferences{}, err
	}
	row := orm.UserNotificationPreferences{UserID: userID, Enabled: true, Revision: 1, Defaults: defaults, UpdatedAt: time.Now().UTC()}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return row, err
	}
	err = db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", userID).First(&row).Error
	return row, err
}

// InitializeScheduleNotifications is shared by all schedule creation entrypoints.
func InitializeScheduleNotifications(ctx context.Context, db *gorm.DB, schedule *orm.UserSchedule) error {
	prefs, err := LoadNotificationPreferences(ctx, db, schedule.UserID)
	if err != nil {
		return err
	}
	value := string(prefs.Defaults)
	schedule.NotificationConfig = &value
	schedule.NotificationRevision = 1
	return nil
}

func snapshotNotifications(ctx context.Context, db *gorm.DB, task *orm.TaskCenterTask) error {
	if task.TaskType != "scheduled" || task.ScheduleID == nil {
		return nil
	}
	var schedule orm.UserSchedule
	err := db.WithContext(ctx).Select("notification_config", "notification_revision").Where("id = ? AND user_id = ?", *task.ScheduleID, task.UserID).First(&schedule).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	} // Existing historical/orphaned runs have no policy.
	if err != nil {
		return err
	}
	task.NotificationConfig, task.NotificationRevision = schedule.NotificationConfig, schedule.NotificationRevision
	return nil
}

func validateNotificationConfig(ctx context.Context, userID string, config NotificationConfig, defaults bool) error {
	invalid := func() error { return notificationProblem(422, "INVALID_REQUEST") }
	if len(config.Events) != 3 || len(config.Channels) == 0 || len(config.Channels) > 4 {
		return invalid()
	}
	activeEvent, activeChannel := false, false
	for name, event := range config.Events {
		if name != "succeeded" && name != "failed" && name != "waiting" {
			return invalid()
		}
		if event.Content != "summary" && event.Content != "full" {
			return invalid()
		}
		activeEvent = activeEvent || event.Enabled
	}
	for name, channel := range config.Channels {
		if name != "desktop" && name != "wechat" && name != "wecom" && name != "feishu" {
			return invalid()
		}
		if utf8.RuneCountInString(channel.AccountID) > 256 || utf8.RuneCountInString(channel.RecipientID) > 256 || strings.ContainsAny(channel.AccountID+channel.RecipientID, "\x00\r\n") {
			return invalid()
		}
		if name == "desktop" && (channel.AccountID != "" || channel.RecipientID != "") {
			return invalid()
		}
		activeChannel = activeChannel || channel.Enabled
		if channel.Enabled && name != "desktop" && (strings.TrimSpace(channel.AccountID) == "" || strings.TrimSpace(channel.RecipientID) == "") {
			return notificationProblem(422, "NOTIFICATION_TARGET_REQUIRED")
		}
	}
	if !activeEvent && (defaults || activeChannel) {
		return notificationProblem(422, "NOTIFICATION_EVENT_REQUIRED")
	}
	for provider, channel := range config.Channels {
		if channel.Enabled && provider != "desktop" {
			if err := validateNotificationTarget(ctx, userID, provider, channel); err != nil {
				return err
			}
		}
	}
	return nil
}

func replyNotificationError(w http.ResponseWriter, r *http.Request, err error) {
	status, reason := 500, "NOTIFICATION_INTERNAL_ERROR"
	var expected *notificationError
	detail := map[string]any{}
	if errors.As(err, &expected) {
		status, reason = expected.status, expected.reason
		if expected.affected != nil {
			detail["running_task_ids"] = expected.affected
		}
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		status, reason = 404, "NOTIFICATION_NOT_FOUND"
	}
	requestID := r.Header.Get("X-Request-Id")
	if len(requestID) > 128 || strings.TrimSpace(requestID) == "" {
		requestID = common.GenerateID()
	}
	detail["reason"], detail["request_id"] = reason, requestID
	common.ReplyAppErr(w, common.NewAppError(status, common.ErrorCodeFromHTTPStatus(status), "通知请求未完成，请检查设置后重试").WithDetail(detail))
}
