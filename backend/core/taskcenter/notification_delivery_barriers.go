package taskcenter

import (
	"encoding/json"
	"time"

	"lazymind/core/common/orm"
)

type notificationDeliveryBarrier struct {
	Before time.Time `json:"before"`
	Reason string    `json:"reason"`
}

// Delivery barriers are server-owned metadata in the existing preferences JSON.
// Public NotificationConfig reads and schedule snapshots omit these fields.
type notificationStoredDefaults struct {
	NotificationConfig
	DeliveryBarriers map[string]notificationDeliveryBarrier `json:"_delivery_barriers,omitempty"`
}

func updatedNotificationDefaults(current orm.UserNotificationPreferences, enabled *bool, defaults *NotificationConfig, now time.Time) ([]byte, error) {
	var stored notificationStoredDefaults
	if err := json.Unmarshal(current.Defaults, &stored); err != nil {
		return nil, err
	}
	next := current
	if enabled != nil {
		next.Enabled = *enabled
	}
	if defaults != nil {
		stored.NotificationConfig = *defaults
	}
	raw, err := json.Marshal(stored.NotificationConfig)
	if err != nil {
		return nil, err
	}
	next.Defaults = raw
	for _, channel := range []string{"desktop", "feishu", "wecom", "wechat"} {
		before, after := notificationBlockReason(current, channel), notificationBlockReason(next, channel)
		if (before == "") == (after == "") {
			continue
		}
		if stored.DeliveryBarriers == nil {
			stored.DeliveryBarriers = make(map[string]notificationDeliveryBarrier)
		}
		reason := after
		if reason == "" {
			reason = before
		}
		stored.DeliveryBarriers[channel] = notificationDeliveryBarrier{Before: now, Reason: reason}
	}
	return json.Marshal(stored)
}

func completionNotificationBlockReason(prefs orm.UserNotificationPreferences, channel string, completedAt *time.Time) string {
	if reason := notificationBlockReason(prefs, channel); reason != "" {
		return reason
	}
	if completedAt == nil || completedAt.IsZero() {
		return ""
	}
	var stored notificationStoredDefaults
	if err := json.Unmarshal(prefs.Defaults, &stored); err != nil {
		return "NOTIFICATION_SETTINGS_INVALID"
	}
	barrier, ok := stored.DeliveryBarriers[channel]
	if ok && !completedAt.After(barrier.Before) {
		return barrier.Reason
	}
	return ""
}
