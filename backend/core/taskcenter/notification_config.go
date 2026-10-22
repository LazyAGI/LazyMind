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
	var defaults NotificationConfig
	if json.Unmarshal(row.Defaults, &defaults) == nil {
		clearNotificationDefaultTargets(&defaults)
		return map[string]any{"enabled": row.Enabled, "revision": row.Revision, "defaults": defaults}
	}
	return map[string]any{"enabled": row.Enabled, "revision": row.Revision, "defaults": json.RawMessage(row.Defaults)}
}

func clearNotificationDefaultTargets(config *NotificationConfig) {
	for name, channel := range config.Channels {
		if name == "desktop" {
			continue
		}
		channel.AccountID = ""
		channel.RecipientID = ""
		config.Channels[name] = channel
	}
}

// Global channel flags never select a recipient for an individual task.
func taskNotificationDefaults(defaults NotificationConfig) NotificationConfig {
	channels := make(map[string]NotificationChannelRule, len(defaults.Channels))
	for provider, channel := range defaults.Channels {
		if provider != "desktop" {
			channel = NotificationChannelRule{Enabled: channel.Enabled}
		}
		channels[provider] = channel
	}
	defaults.Channels = channels
	return defaults
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

// ReadNotificationPreferences never inserts or locks rows: Gateway reference
// callbacks must be safe while a schedule transaction owns the preference lock.
func ReadNotificationPreferences(ctx context.Context, db *gorm.DB, userID string) (orm.UserNotificationPreferences, error) {
	var row orm.UserNotificationPreferences
	err := db.WithContext(ctx).Where("user_id = ?", userID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		raw, marshalErr := json.Marshal(DefaultNotificationConfig())
		return orm.UserNotificationPreferences{UserID: userID, Enabled: true, Revision: 1, Defaults: raw}, marshalErr
	}
	return row, err
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
	prefs, err := ReadNotificationPreferences(ctx, db, schedule.UserID)
	if err != nil {
		return err
	}
	var config NotificationConfig
	if err := json.Unmarshal(prefs.Defaults, &config); err != nil {
		return err
	}
	config = taskNotificationDefaults(config)
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	value := string(raw)
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

func validateNotificationConfig(config NotificationConfig, defaults bool) error {
	return validateNotificationConfigForUpdate(config, defaults, nil)
}

func validateNotificationConfigForUpdate(config NotificationConfig, defaults bool, preserved map[string]NotificationChannelRule) error {
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
		_, keepPaused := preserved[name]
		if !defaults && !keepPaused && channel.Enabled && name != "desktop" && strings.TrimSpace(channel.AccountID) == "" {
			return notificationProblem(422, "NOTIFICATION_TARGET_REQUIRED")
		}
	}
	if !activeEvent && (defaults || activeChannel) {
		return notificationProblem(422, "NOTIFICATION_EVENT_REQUIRED")
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

// ScheduleNotificationUpdate is shared by standalone and atomic schedule edits.
// Clear is explicit so an accidental missing/null config cannot erase a rule.
type ScheduleNotificationUpdate struct {
	Revision      int64               `json:"revision"`
	Config        *NotificationConfig `json:"config,omitempty"`
	Clear         bool                `json:"clear,omitempty"`
	prepared      bool
	preparedOwner string
	preparedValue any
	preparedID    string
	preparedNew   bool
	preparedKeep  map[string]NotificationChannelRule
}

// UnmarshalJSON applies the same bounded, strict contract at every entrypoint.
func (update *ScheduleNotificationUpdate) UnmarshalJSON(raw []byte) error {
	if len(raw) > 16*1024 {
		return notificationProblem(422, "INVALID_REQUEST")
	}
	type plain ScheduleNotificationUpdate
	var value plain
	if err := decodeNotificationBytes(raw, &value); err != nil {
		return notificationProblem(422, "INVALID_REQUEST")
	}
	*update = ScheduleNotificationUpdate(value)
	return nil
}

// PrepareScheduleNotificationUpdate resolves external targets before opening a
// database transaction. The frozen value cannot change during the CAS write.
func PrepareScheduleNotificationUpdate(ctx context.Context, owner string, update ScheduleNotificationUpdate) (ScheduleNotificationUpdate, error) {
	return prepareScheduleNotificationUpdate(ctx, owner, update, nil)
}

// PrepareScheduleNotificationUpdateForSchedule can preserve an unchanged paused
// selection without asking the gateway about an account that may be offline.
// Reads and any required gateway validation finish before the caller starts its
// write transaction. An empty id is reserved for a new schedule's draft.
func PrepareScheduleNotificationUpdateForSchedule(ctx context.Context, db *gorm.DB, owner, id string, update ScheduleNotificationUpdate) (ScheduleNotificationUpdate, error) {
	keep := make(map[string]NotificationChannelRule)
	if update.Config != nil {
		prefs, err := ReadNotificationPreferences(ctx, db, owner)
		if err != nil {
			return update, err
		}
		var original NotificationConfig
		if id != "" {
			var schedule orm.UserSchedule
			if err := db.WithContext(ctx).Select("notification_config", "notification_revision").Where("id = ? AND user_id = ?", id, owner).First(&schedule).Error; err != nil {
				return update, err
			}
			if schedule.NotificationRevision != update.Revision {
				return update, notificationProblem(409, "NOTIFICATION_CONFIG_CONFLICT")
			}
			if schedule.NotificationConfig != nil {
				if err := json.Unmarshal([]byte(*schedule.NotificationConfig), &original); err != nil {
					return update, err
				}
			}
		}
		for provider, channel := range update.Config.Channels {
			if provider == "desktop" || !channel.Enabled || !notificationChannelPaused(prefs, provider) {
				continue
			}
			if id == "" {
				// New drafts may retain a selected but untargeted default after
				// its global switch was closed. No account identity is accepted.
				if channel.AccountID == "" && channel.RecipientID == "" {
					keep[provider] = channel
				}
			} else if saved, ok := original.Channels[provider]; ok && saved == channel {
				keep[provider] = channel
			}
		}
	}
	prepared, err := prepareScheduleNotificationUpdate(ctx, owner, update, keep)
	if err != nil {
		return update, err
	}
	prepared.preparedID, prepared.preparedNew = id, id == ""
	return prepared, nil
}

func notificationChannelPaused(prefs orm.UserNotificationPreferences, provider string) bool {
	reason := notificationBlockReason(prefs, provider)
	return reason == "NOTIFICATIONS_DISABLED" || reason == "NOTIFICATION_CHANNEL_DISABLED"
}

func prepareScheduleNotificationUpdate(ctx context.Context, owner string, update ScheduleNotificationUpdate, keep map[string]NotificationChannelRule) (ScheduleNotificationUpdate, error) {
	if update.Revision < 0 || (update.Config == nil && !update.Clear) || (update.Config != nil && update.Clear) {
		return update, notificationProblem(422, "INVALID_REQUEST")
	}
	var value any
	if update.Config != nil {
		copied := *update.Config
		copied.Channels = make(map[string]NotificationChannelRule, len(update.Config.Channels))
		for name, rule := range update.Config.Channels {
			copied.Channels[name] = rule
		}
		update.Config = &copied
		if err := validateNotificationConfigForUpdate(*update.Config, false, keep); err != nil {
			return update, err
		}
		for provider, channel := range update.Config.Channels {
			if provider == "desktop" || !channel.Enabled {
				continue
			}
			if _, preserved := keep[provider]; preserved {
				continue
			}
			resolved, err := resolveNotificationTarget(ctx, owner, provider, channel)
			if err != nil {
				return update, err
			}
			update.Config.Channels[provider] = resolved
		}
		raw, err := json.Marshal(update.Config)
		if err != nil {
			return update, err
		}
		value = string(raw)
	}
	update.prepared, update.preparedOwner, update.preparedValue = true, owner, value
	update.preparedKeep = keep
	return update, nil
}

// SaveScheduleNotificationUpdate persists a prepared policy with revision CAS.
func SaveScheduleNotificationUpdate(ctx context.Context, tx *gorm.DB, owner, id string, update ScheduleNotificationUpdate) error {
	if !update.prepared || update.preparedOwner != owner {
		var err error
		update, err = PrepareScheduleNotificationUpdate(ctx, owner, update)
		if err != nil {
			return err
		}
	}
	value := update.preparedValue
	if len(update.preparedKeep) > 0 {
		// Recheck under the same preference lock used by global switch
		// changes. A gate opened since preparation requires a fresh attempt;
		// gateway calls here could deadlock its reference callback.
		prefs, err := LoadNotificationPreferences(ctx, tx, owner)
		if err != nil {
			return err
		}
		var current orm.UserSchedule
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("notification_config", "notification_revision").Where("id = ? AND user_id = ?", id, owner).First(&current).Error; err != nil {
			return err
		}
		if current.NotificationRevision != update.Revision || !update.preparedNew && update.preparedID != id {
			return notificationProblem(409, "NOTIFICATION_CONFIG_CONFLICT")
		}
		var saved NotificationConfig
		if current.NotificationConfig != nil {
			if err := json.Unmarshal([]byte(*current.NotificationConfig), &saved); err != nil {
				return err
			}
		}
		for provider, channel := range update.preparedKeep {
			if !notificationChannelPaused(prefs, provider) {
				return notificationProblem(409, "NOTIFICATION_CONFIG_CONFLICT")
			}
			if update.preparedNew {
				if update.Revision != 1 || channel.AccountID != "" || channel.RecipientID != "" {
					return notificationProblem(409, "NOTIFICATION_CONFIG_CONFLICT")
				}
			} else if original, ok := saved.Channels[provider]; !ok || original != channel {
				return notificationProblem(409, "NOTIFICATION_CONFIG_CONFLICT")
			}
		}
	}

	result := tx.WithContext(ctx).Model(&orm.UserSchedule{}).Where("id = ? AND user_id = ? AND notification_revision = ?", id, owner, update.Revision).Updates(map[string]any{"notification_config": value, "notification_revision": update.Revision + 1})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return notificationProblem(409, "NOTIFICATION_CONFIG_CONFLICT")
	}
	return nil
}

// ReplyScheduleNotificationError preserves the notification API's safe envelope.
func ReplyScheduleNotificationError(w http.ResponseWriter, r *http.Request, err error) {
	replyNotificationError(w, r, err)
}
