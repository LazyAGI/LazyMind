package taskcenter

import (
	"net/http"
	"sort"
	"strings"

	"lazymind/core/common"
	"lazymind/core/common/orm"
)

// WeChatNotificationTargets lists only explicitly saved schedule recipients.
// It deliberately does not query Gateway availability or global preferences:
// clients use these pairs to start checks after recipient confirmation and can
// resume checking when the global notification gate is reopened.
func WeChatNotificationTargets(w http.ResponseWriter, r *http.Request) {
	db, owner, ok := notificationRequest(w, r)
	if !ok {
		return
	}
	var schedules []orm.UserSchedule
	if err := db.Model(&orm.UserSchedule{}).
		Select("notification_config").
		Where("user_id = ? AND notification_config IS NOT NULL", owner).
		Find(&schedules).Error; err != nil {
		replyNotificationError(w, r, err)
		return
	}
	type target struct {
		AccountID   string `json:"account_id"`
		RecipientID string `json:"recipient_id"`
	}
	items := []target{}
	seen := map[target]bool{}
	for _, schedule := range schedules {
		config, err := notificationConfigValue(schedule.NotificationConfig)
		if err != nil {
			replyNotificationError(w, r, err)
			return
		}
		if config == nil {
			continue
		}
		rule := config.Channels["wechat"]
		if !rule.Enabled || strings.TrimSpace(rule.AccountID) == "" || strings.TrimSpace(rule.RecipientID) == "" {
			continue
		}
		item := target{AccountID: rule.AccountID, RecipientID: rule.RecipientID}
		if !seen[item] {
			seen[item] = true
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].AccountID == items[j].AccountID {
			return items[i].RecipientID < items[j].RecipientID
		}
		return items[i].AccountID < items[j].AccountID
	})
	common.ReplyOK(w, map[string]any{"items": items})
}
