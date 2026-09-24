package taskcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
)

func notificationGatewayURL() string {
	if value := strings.TrimSpace(os.Getenv("LAZYMIND_CHANNEL_GATEWAY_BASE_URL")); value != "" {
		return strings.TrimRight(value, "/")
	}
	return "http://channel-gateway:8085"
}

func notificationGatewayHeaders(userID string) map[string]string {
	headers := map[string]string{"X-User-Id": userID, "Accept": "application/json"}
	if token := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN")); token != "" {
		headers["X-LazyMind-Internal-Token"] = token
	}
	return headers
}

func validateNotificationTarget(ctx context.Context, userID, provider string, target NotificationChannelRule) error {
	var view struct {
		Provider string `json:"provider"`
		Items    []struct {
			RecipientID string `json:"recipient_id"`
			Available   bool   `json:"available"`
		} `json:"items"`
	}
	endpoint := notificationGatewayURL() + "/api/channel-gateway/v1/channel-accounts/" + url.PathEscape(target.AccountID) + "/notification-targets"
	endpoint += "?recipient_id=" + url.QueryEscape(target.RecipientID)
	if err := common.ApiGet(ctx, endpoint, notificationGatewayHeaders(userID), &view, 10*time.Second); err != nil {
		var httpErr *common.HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode >= 400 && httpErr.StatusCode < 500 && httpErr.StatusCode != http.StatusTooManyRequests {
			return notificationProblem(422, "NOTIFICATION_TARGET_UNAVAILABLE")
		}
		return notificationProblem(503, "NOTIFICATION_UNAVAILABLE")
	}
	if view.Provider == provider {
		for _, item := range view.Items {
			if item.RecipientID == target.RecipientID && item.Available {
				return nil
			}
		}
	}
	return notificationProblem(422, "NOTIFICATION_TARGET_UNAVAILABLE")
}

func notificationTargetUnavailableReason(provider string) string {
	switch provider {
	case "wechat":
		return "WECHAT_NOTIFICATION_CONTEXT_REQUIRED"
	case "wecom":
		return "WECOM_NOTIFICATION_TARGET_UNAVAILABLE"
	case "feishu":
		return "FEISHU_NOTIFICATION_TARGET_UNAVAILABLE"
	default:
		return "NOTIFICATION_TARGET_UNAVAILABLE"
	}
}

func resolveNotificationTarget(ctx context.Context, userID, provider string, target NotificationChannelRule) (NotificationChannelRule, error) {
	if strings.TrimSpace(target.AccountID) == "" {
		return target, notificationProblem(422, "NOTIFICATION_TARGET_REQUIRED")
	}
	if strings.TrimSpace(target.RecipientID) == "" {
		var account struct {
			Provider         string `json:"provider"`
			Status           string `json:"status"`
			DefaultRecipient *struct {
				RecipientID string `json:"recipient_id"`
				Available   bool   `json:"available"`
			} `json:"default_recipient"`
		}
		endpoint := notificationGatewayURL() + "/api/channel-gateway/v1/channel-accounts/" + url.PathEscape(target.AccountID) + "?include_references=false"
		if err := common.ApiGet(ctx, endpoint, notificationGatewayHeaders(userID), &account, 10*time.Second); err != nil {
			var httpErr *common.HTTPError
			if errors.As(err, &httpErr) && httpErr.StatusCode >= 400 && httpErr.StatusCode < 500 && httpErr.StatusCode != http.StatusTooManyRequests {
				return target, notificationProblem(422, notificationTargetUnavailableReason(provider))
			}
			return target, notificationProblem(503, "NOTIFICATION_UNAVAILABLE")
		}
		if account.Provider != provider || account.Status != "connected" || account.DefaultRecipient == nil ||
			!account.DefaultRecipient.Available || strings.TrimSpace(account.DefaultRecipient.RecipientID) == "" {
			return target, notificationProblem(422, notificationTargetUnavailableReason(provider))
		}
		target.RecipientID = account.DefaultRecipient.RecipientID
	}
	if err := validateNotificationTarget(ctx, userID, provider, target); err != nil {
		var problem *notificationError
		if errors.As(err, &problem) && problem.status == 503 {
			return target, err
		}
		return target, notificationProblem(422, notificationTargetUnavailableReason(provider))
	}
	return target, nil
}

// RunNotificationDelivery replays durable Core events until the gateway confirms
// its outbox entry. Only that outbox performs external sends and retries.
func RunNotificationDelivery(ctx context.Context, db *gorm.DB) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		// One bounded recovery worker may wait for model summaries; delivery
		// of already persisted events has its own loop and never waits for it.
		recovered := make(chan struct{})
		go func() {
			defer close(recovered)
			recoveryTicker := time.NewTicker(2 * time.Second)
			defer recoveryTicker.Stop()
			cursor := ""
			for ctx.Err() == nil {
				var err error
				cursor, err = ReconcileScheduledNotifications(ctx, db, cursor, time.Now().UTC())
				if err != nil && ctx.Err() == nil {
					log.Logger.Warn().Msg("scheduled_notification_reconciliation_unavailable")
				}
				select {
				case <-ctx.Done():
					return
				case <-recoveryTicker.C:
				}
			}
		}()
		defer func() { <-recovered }()
		for {
			if ctx.Err() != nil {
				return
			}

			if err := DispatchNotifications(ctx, db); err != nil {
				log.Logger.Warn().Msg("task_notification_dispatch_unavailable")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}

func DispatchNotifications(ctx context.Context, db *gorm.DB) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var notices []orm.TaskNotification
	if err := db.WithContext(ctx).Where("channel <> 'desktop' AND status IN ('pending','queued','sending')").
		Order("updated_at,id").Limit(100).Find(&notices).Error; err != nil {
		return err
	}
	for _, notice := range notices {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if strings.TrimSpace(notice.AccountID) == "" {
			if err := db.WithContext(ctx).Model(&orm.TaskNotification{}).
				Where("id = ? AND status IN ('pending','queued','sending')", notice.ID).
				Updates(map[string]any{"status": "skipped", "reason": gorm.Expr("CASE WHEN reason IN (?, ?) THEN reason ELSE ? END", "NOTIFICATIONS_DISABLED", "NOTIFICATION_CHANNEL_DISABLED", "NOTIFICATION_TARGET_UNAVAILABLE"), "updated_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
			continue
		}
		prefs, err := LoadNotificationPreferences(ctx, db, notice.UserID)
		if err != nil {
			return err
		}
		if reason := notificationBlockReason(prefs, notice.Channel); reason != "" {
			if err := db.WithContext(ctx).Model(&orm.TaskNotification{}).
				Where("id = ? AND status IN ('pending','queued','sending')", notice.ID).
				Updates(map[string]any{"status": "skipped", "reason": gorm.Expr("CASE WHEN reason IN (?, ?) THEN reason ELSE ? END", "NOTIFICATIONS_DISABLED", "NOTIFICATION_CHANNEL_DISABLED", reason), "updated_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
			continue
		}
		var view struct {
			ID     string `json:"notification_id"`
			Status string `json:"status"`
			Reason string `json:"reason"`
		}
		endpoint := notificationGatewayURL() + "/api/channel-gateway/v1/task-notifications"
		err = nil
		if notice.GatewayID == "" {
			payload := map[string]any{"event_id": notice.EventID, "task_id": notice.TaskID, "schedule_id": notice.ScheduleID,
				"event": notice.Event, "config_revision": notice.ConfigRevision, "title": notice.Title, "body": notice.Body,
				"content": notice.Content, "channel": notice.Channel, "account_id": notice.AccountID, "recipient_id": notice.RecipientID}
			err = common.ApiPost(ctx, endpoint, payload, notificationGatewayHeaders(notice.UserID), &view, 10*time.Second)
		} else {
			err = common.ApiGet(ctx, endpoint+"/"+url.PathEscape(notice.GatewayID), notificationGatewayHeaders(notice.UserID), &view, 10*time.Second)
		}
		updates := map[string]any{"updated_at": time.Now().UTC()}
		var httpError *common.HTTPError
		if errors.As(err, &httpError) && (httpError.StatusCode == 422 || httpError.StatusCode == 409) {
			var problem struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if json.Unmarshal(httpError.Body, &problem) == nil {
				switch problem.Error.Code {
				case "NOTIFICATION_TARGET_UNAVAILABLE", "NOTIFICATIONS_DISABLED", "NOTIFICATION_CHANNEL_DISABLED", "NOTIFICATION_EVENT_INVALID":
					updates["status"], updates["reason"] = "skipped", gorm.Expr("CASE WHEN reason IN (?, ?) THEN reason ELSE ? END", "NOTIFICATIONS_DISABLED", "NOTIFICATION_CHANNEL_DISABLED", problem.Error.Code)
				}
			}
		}
		if err == nil && len(view.ID) == 64 {
			switch view.Status {
			case "queued", "sending", "sent", "failed", "skipped", "unknown":
				updates["gateway_id"], updates["status"] = view.ID, view.Status
				// The gateway exposes only stable notification reasons.
				if view.Reason == "" || len(view.Reason) <= 64 && strings.HasPrefix(view.Reason, "NOTIFICATION") {
					// Preserve a concurrent close tombstone even if the gateway is
					// still reporting an earlier queued/sending state.
					updates["reason"] = gorm.Expr("CASE WHEN reason IN (?, ?) THEN reason ELSE ? END", "NOTIFICATIONS_DISABLED", "NOTIFICATION_CHANNEL_DISABLED", view.Reason)
				}
			}
		}
		if err := db.WithContext(ctx).Model(&orm.TaskNotification{}).
			Where("id = ? AND status IN ('pending','queued','sending')", notice.ID).Updates(updates).Error; err != nil {
			return err
		}
	}
	return nil
}
