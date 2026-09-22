package taskcenter

import (
	"bytes"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func notificationRequest(w http.ResponseWriter, r *http.Request) (*gorm.DB, string, bool) {
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		replyNotificationError(w, r, notificationProblem(401, "UNAUTHORIZED"))
		return nil, "", false
	}
	if len(userID) > 255 || store.DB() == nil {
		replyNotificationError(w, r, notificationProblem(503, "NOTIFICATION_UNAVAILABLE"))
		return nil, "", false
	}
	w.Header().Set("Cache-Control", "no-store")
	return store.DB().WithContext(r.Context()), userID, true
}

// Check duplicate fields before typed decoding: encoding/json otherwise silently
// accepts the last value, including conflicting authorization/configuration fields.
func decodeNotificationRequest(w http.ResponseWriter, r *http.Request, value any) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16*1024))
	if err != nil {
		return notificationProblem(422, "INVALID_REQUEST")
	}
	return decodeNotificationBytes(raw, value)
}

func decodeNotificationBytes(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 12 {
			return notificationProblem(422, "INVALID_REQUEST")
		}
		token, err := decoder.Token()
		if err != nil || token == nil {
			return notificationProblem(422, "INVALID_REQUEST")
		}
		if delim, ok := token.(json.Delim); ok {
			if delim != '{' && delim != '[' {
				return notificationProblem(422, "INVALID_REQUEST")
			}
			keys := map[string]bool{}
			for decoder.More() {
				if delim == '{' {
					key, err := decoder.Token()
					if err != nil {
						return err
					}
					name, ok := key.(string)
					if !ok || keys[name] {
						return notificationProblem(422, "INVALID_REQUEST")
					}
					keys[name] = true
				}
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		}
		return nil
	}
	if err := walk(0); err != nil {
		return notificationProblem(422, "INVALID_REQUEST")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return notificationProblem(422, "INVALID_REQUEST")
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return notificationProblem(422, "INVALID_REQUEST")
	}
	return nil
}

func NotificationPreferences(w http.ResponseWriter, r *http.Request) {
	db, owner, ok := notificationRequest(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		row, err := ReadNotificationPreferences(r.Context(), db, owner)
		if err != nil {
			replyNotificationError(w, r, err)
			return
		}
		common.ReplyOK(w, notificationPreferencesView(row))
		return
	}
	var req struct {
		Revision int64               `json:"revision"`
		Enabled  *bool               `json:"enabled"`
		Defaults *NotificationConfig `json:"defaults"`
		Confirm  []string            `json:"confirm_running_task_ids"`
	}
	if err := decodeNotificationRequest(w, r, &req); err != nil {
		replyNotificationError(w, r, err)
		return
	}
	if req.Revision < 1 || req.Enabled == nil && req.Defaults == nil {
		replyNotificationError(w, r, notificationProblem(422, "INVALID_REQUEST"))
		return
	}
	if req.Defaults != nil {
		if err := validateNotificationConfig(*req.Defaults, true); err != nil {
			replyNotificationError(w, r, err)
			return
		}
	}
	var result orm.UserNotificationPreferences
	err := notificationTx(r.Context(), db, func(tx *gorm.DB) error {
		current, err := LoadNotificationPreferences(r.Context(), tx, owner)
		if err != nil {
			return err
		}
		if current.Revision != req.Revision {
			return notificationProblem(409, "NOTIFICATION_CONFIG_CONFLICT")
		}
		if req.Enabled != nil && !*req.Enabled && current.Enabled {
			ids, err := activeNotificationRuns(tx, owner)
			if err != nil {
				return err
			}
			slices.Sort(req.Confirm)
			if len(ids) > 0 && !slices.Equal(ids, req.Confirm) {
				return &notificationError{status: 409, reason: "NOTIFICATION_CONFIRMATION_REQUIRED", affected: ids}
			}
			if err := tx.Model(&orm.TaskNotification{}).Where("user_id = ? AND status IN ('pending','queued')", owner).Updates(map[string]any{"status": "skipped", "reason": "NOTIFICATIONS_DISABLED", "updated_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
			if err := tx.Model(&orm.TaskNotification{}).Where("user_id = ? AND status = 'sending'", owner).Update("reason", "NOTIFICATIONS_DISABLED").Error; err != nil {
				return err
			}
		}
		if req.Defaults != nil {
			disabled := disabledNotificationChannels(*req.Defaults)
			if len(disabled) > 0 {
				now := time.Now().UTC()
				if err := tx.Model(&orm.TaskNotification{}).
					Where("user_id = ? AND channel IN ? AND status IN ('pending','queued')", owner, disabled).
					Updates(map[string]any{"status": "skipped", "reason": "NOTIFICATION_CHANNEL_DISABLED", "updated_at": now}).Error; err != nil {
					return err
				}
			}
		}
		updates := map[string]any{"revision": current.Revision + 1, "updated_at": time.Now().UTC()}
		if req.Enabled != nil {
			updates["enabled"] = *req.Enabled
		}
		if req.Defaults != nil {
			raw, err := json.Marshal(req.Defaults)
			if err != nil {
				return err
			}
			updates["defaults"] = string(raw)
		}
		update := tx.Model(&orm.UserNotificationPreferences{}).Where("user_id = ? AND revision = ?", owner, req.Revision).Updates(updates)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return notificationProblem(409, "NOTIFICATION_CONFIG_CONFLICT")
		}
		return tx.First(&result, "user_id = ?", owner).Error
	})
	if err != nil {
		replyNotificationError(w, r, err)
		return
	}
	common.ReplyOK(w, notificationPreferencesView(result))
}

// ClaimNotification grants a single segment send under the same preference lock
// as closing the global switch. Already started platform calls cannot be revoked.
func ClaimNotification(w http.ResponseWriter, r *http.Request) {
	db, owner, ok := notificationRequest(w, r)
	if !ok {
		return
	}
	expected := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"))
	if expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(r.Header.Get("X-LazyMind-Internal-Token"))) != 1 {
		replyNotificationError(w, r, notificationProblem(401, "UNAUTHORIZED"))
		return
	}
	var req struct {
		OutboxID string `json:"outbox_id"`
		Retry    bool   `json:"retry"`
	}
	if err := decodeNotificationRequest(w, r, &req); err != nil {
		replyNotificationError(w, r, err)
		return
	}
	if len(req.OutboxID) != 64 {
		replyNotificationError(w, r, notificationProblem(422, "INVALID_REQUEST"))
		return
	}
	err := notificationTx(r.Context(), db, func(tx *gorm.DB) error {
		prefs, err := LoadNotificationPreferences(r.Context(), tx, owner)
		if err != nil {
			return err
		}
		var notice orm.TaskNotification
		if err = tx.First(&notice, "id = ? AND user_id = ? AND channel <> 'desktop'", mux.Vars(r)["notification_id"], owner).Error; err != nil {
			return err
		}
		if reason := notificationBlockReason(prefs, notice.Channel); reason != "" || notice.Status == "skipped" ||
			notice.Reason == "NOTIFICATIONS_DISABLED" || notice.Reason == "NOTIFICATION_CHANNEL_DISABLED" {
			if reason == "" {
				reason = notice.Reason
			}
			if reason == "" {
				reason = "NOTIFICATION_EVENT_INVALID"
			}
			return notificationProblem(409, reason)
		}
		updates := map[string]any{"status": "sending", "updated_at": time.Now().UTC()}
		if !req.Retry {
			updates["gateway_id"] = req.OutboxID
		}
		return tx.Model(&notice).Updates(updates).Error
	})
	if err != nil {
		replyNotificationError(w, r, err)
		return
	}
	common.ReplyOK(w, map[string]any{"granted": true})
}

func ScheduleNotifications(w http.ResponseWriter, r *http.Request) {
	db, owner, ok := notificationRequest(w, r)
	if !ok {
		return
	}
	id := mux.Vars(r)["schedule_id"]
	var schedule orm.UserSchedule
	if err := db.Where("id = ? AND user_id = ?", id, owner).First(&schedule).Error; err != nil {
		replyNotificationError(w, r, err)
		return
	}
	if r.Method != http.MethodGet {
		var req ScheduleNotificationUpdate
		if err := decodeNotificationRequest(w, r, &req); err != nil {
			replyNotificationError(w, r, err)
			return
		}
		if strings.HasSuffix(r.URL.Path, ":reset") {
			prefs, err := ReadNotificationPreferences(r.Context(), db, owner)
			if err != nil {
				replyNotificationError(w, r, err)
				return
			}
			if err := json.Unmarshal(prefs.Defaults, &req.Config); err != nil {
				replyNotificationError(w, r, err)
				return
			}
		}
		prepared, err := PrepareScheduleNotificationUpdate(r.Context(), owner, req)
		if err != nil {
			replyNotificationError(w, r, err)
			return
		}
		req = prepared
		err = notificationTx(r.Context(), db, func(tx *gorm.DB) error {
			if err := SaveScheduleNotificationUpdate(r.Context(), tx, owner, id, req); err != nil {
				return err
			}
			return tx.First(&schedule, "id = ? AND user_id = ?", id, owner).Error
		})
		if err != nil {
			replyNotificationError(w, r, err)
			return
		}

	}
	config, err := notificationConfigValue(schedule.NotificationConfig)
	if err != nil {
		replyNotificationError(w, r, err)
		return
	}
	availability := map[string]any{}
	if config != nil {
		for provider, channel := range config.Channels {
			state, reason := "disabled", ""
			if channel.Enabled {
				state = "available"
				// Native and browser consumers share the system-notification channel.
				// Permission/capability is determined by the receiving client.
				if provider != "desktop" {
					if err := validateNotificationTarget(r.Context(), owner, provider, channel); err != nil {
						state, reason = "unavailable", notificationTargetUnavailableReason(provider)
					}
				}
			}
			availability[provider] = map[string]any{"state": state, "reason": reason}
		}
	}
	common.ReplyOK(w, map[string]any{"configured": schedule.NotificationConfig != nil, "revision": schedule.NotificationRevision, "config": config, "availability": availability})
}

func TaskNotifications(w http.ResponseWriter, r *http.Request) {
	db, owner, ok := notificationRequest(w, r)
	if !ok {
		return
	}
	var task orm.TaskCenterTask
	if err := db.First(&task, "id = ? AND user_id = ?", mux.Vars(r)["task_id"], owner).Error; err != nil {
		replyNotificationError(w, r, err)
		return
	}
	items := []orm.TaskNotification{}
	if err := db.Where("task_id = ? AND user_id = ?", task.ID, owner).Order("created_at,id").Find(&items).Error; err != nil {
		replyNotificationError(w, r, err)
		return
	}
	config, err := notificationConfigValue(task.NotificationConfig)
	if err != nil {
		replyNotificationError(w, r, err)
		return
	}
	common.ReplyOK(w, map[string]any{"snapshot": map[string]any{"revision": task.NotificationRevision, "config": config}, "items": items})
}

func NotificationAccountReferences(w http.ResponseWriter, r *http.Request) {
	db, owner, ok := notificationRequest(w, r)
	if !ok {
		return
	}
	accountID := mux.Vars(r)["account_id"]
	if len([]rune(accountID)) > 256 {
		replyNotificationError(w, r, notificationProblem(422, "INVALID_REQUEST"))
		return
	}
	limit := 20
	var err error
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
	}
	if err != nil || limit < 1 || limit > 100 {
		replyNotificationError(w, r, notificationProblem(422, "INVALID_REQUEST"))
		return
	}
	type reference struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"`
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	items := []reference{}
	appendReference := func(raw *string, id, kind, name string) error {
		config, err := notificationConfigValue(raw)
		if err != nil || config == nil {
			return err
		}
		for _, channel := range config.Channels {
			if channel.AccountID == accountID {
				items = append(items, reference{id, kind, name, channel.Enabled})
				break
			}
		}
		return nil
	}
	prefs, err := ReadNotificationPreferences(r.Context(), db, owner)
	if err != nil {
		replyNotificationError(w, r, err)
		return
	}
	raw := string(prefs.Defaults)
	if err = appendReference(&raw, "defaults", "defaults", "默认通知配置"); err != nil {
		replyNotificationError(w, r, err)
		return
	}
	var schedules []orm.UserSchedule
	if err = db.Where("user_id = ? AND notification_config IS NOT NULL", owner).Order("id").Find(&schedules).Error; err != nil {
		replyNotificationError(w, r, err)
		return
	}
	for _, schedule := range schedules {
		if err = appendReference(schedule.NotificationConfig, schedule.ID, "schedule", schedule.Name); err != nil {
			replyNotificationError(w, r, err)
			return
		}
	}
	var tasks []orm.TaskCenterTask
	if err = db.Where("user_id = ? AND task_type = 'scheduled' AND status IN ('pending','running','waiting','waiting_inputs') AND notification_config IS NOT NULL AND archived_at IS NULL", owner).Order("id").Find(&tasks).Error; err != nil {
		replyNotificationError(w, r, err)
		return
	}
	for _, task := range tasks {
		title := "定时任务"
		if task.Title != nil {
			title = *task.Title
		}
		if err = appendReference(task.NotificationConfig, task.ID, "run", title); err != nil {
			replyNotificationError(w, r, err)
			return
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Kind+":"+items[i].ID < items[j].Kind+":"+items[j].ID })
	total := len(items)
	filtered := []reference{}
	cursor := r.URL.Query().Get("cursor")
	if len(cursor) > 512 {
		replyNotificationError(w, r, notificationProblem(422, "INVALID_REQUEST"))
		return
	}
	for _, item := range items {
		if item.Kind+":"+item.ID > cursor {
			filtered = append(filtered, item)
		}
	}
	next := ""
	if len(filtered) > limit {
		filtered = filtered[:limit]
		last := filtered[len(filtered)-1]
		next = last.Kind + ":" + last.ID
	}
	common.ReplyOK(w, map[string]any{"items": filtered, "total": total, "next_cursor": next})
}

func notificationDevice(requested string) (string, error) {
	// A fixed browser receipt namespace, not a caller-selected remote device.
	// Both feed and receipt remain scoped to the authenticated owner.
	if requested == "browser" {
		return "browser", nil
	}
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("LAZYMIND_RUNTIME_MODE")))
	if mode != "local" && mode != "desktop" {
		return "", notificationProblem(503, "NOTIFICATION_DEVICE_UNAVAILABLE")
	}
	if requested != "" && requested != "local" {
		return "", notificationProblem(403, "NOTIFICATION_DEVICE_UNAVAILABLE")
	}
	return "local", nil
}

func DesktopNotifications(w http.ResponseWriter, r *http.Request) {
	db, owner, ok := notificationRequest(w, r)
	if !ok {
		return
	}
	device, err := notificationDevice(r.URL.Query().Get("device_id"))
	if err != nil {
		replyNotificationError(w, r, err)
		return
	}
	limit := 20
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
	}
	if err != nil || limit < 1 || limit > 100 {
		replyNotificationError(w, r, notificationProblem(422, "INVALID_REQUEST"))
		return
	}
	var cursor struct {
		At    time.Time `json:"at"`
		ID    string    `json:"id"`
		Owner string    `json:"owner"`
	}
	query := db.Where("user_id = ? AND channel = 'desktop' AND status = 'pending'", owner).Where("NOT EXISTS (SELECT 1 FROM desktop_notification_receipts receipt WHERE receipt.notification_id = task_notifications.id AND receipt.device_id = ? AND receipt.user_id = ?)", device, owner)
	if value := r.URL.Query().Get("cursor"); value != "" {
		raw, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil || len(raw) > 1024 || json.Unmarshal(raw, &cursor) != nil || cursor.At.IsZero() || len(cursor.ID) != 64 || cursor.Owner != owner {
			replyNotificationError(w, r, notificationProblem(422, "INVALID_REQUEST"))
			return
		}
		query = query.Where("created_at > ? OR (created_at = ? AND id > ?)", cursor.At, cursor.At, cursor.ID)
	}
	items := []orm.TaskNotification{}
	if err := query.Order("created_at,id").Limit(limit + 1).Find(&items).Error; err != nil {
		replyNotificationError(w, r, err)
		return
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		cursor.At, cursor.ID, cursor.Owner = last.CreatedAt, last.ID, owner
		raw, _ := json.Marshal(cursor)
		next = base64.RawURLEncoding.EncodeToString(raw)
	}
	type desktopView struct {
		orm.TaskNotification
		// The ORM hides ownership by default; system clients require this explicit
		// authenticated identity to reject stale or cross-session deliveries.
		UserID      string            `json:"user_id"`
		AppName     string            `json:"app_name"`
		ExecutionID string            `json:"execution_id"`
		Navigation  map[string]string `json:"navigation"`
	}
	views := make([]desktopView, 0, len(items))
	for _, item := range items {
		views = append(views, desktopView{item, owner, "LazyMind", item.TaskID, map[string]string{"type": "task", "task_id": item.TaskID, "schedule_id": item.ScheduleID}})
	}
	common.ReplyOK(w, map[string]any{"items": views, "next_cursor": next, "device_id": device})
}

func AcknowledgeDesktopNotification(w http.ResponseWriter, r *http.Request) {
	db, owner, ok := notificationRequest(w, r)
	if !ok {
		return
	}
	var req struct {
		DeviceID string `json:"device_id"`
		Status   string `json:"status"`
	}
	if err := decodeNotificationRequest(w, r, &req); err != nil {
		replyNotificationError(w, r, err)
		return
	}
	device, err := notificationDevice(req.DeviceID)
	if err != nil {
		replyNotificationError(w, r, err)
		return
	}
	if req.Status != "delivered" && req.Status != "permission_denied" {
		replyNotificationError(w, r, notificationProblem(422, "INVALID_REQUEST"))
		return
	}
	receipt := orm.DesktopNotificationReceipt{NotificationID: mux.Vars(r)["notification_id"], UserID: owner, DeviceID: device, Status: req.Status, CreatedAt: time.Now().UTC()}
	if req.Status == "permission_denied" {
		receipt.Reason = "DESKTOP_NOTIFICATION_PERMISSION_DENIED"
	}
	err = notificationTx(r.Context(), db, func(tx *gorm.DB) error {
		var notice orm.TaskNotification
		if err := tx.First(&notice, "id = ? AND user_id = ? AND channel = 'desktop'", receipt.NotificationID, owner).Error; err != nil {
			return err
		}
		if notice.Status == "skipped" {
			return notificationProblem(409, "NOTIFICATIONS_DISABLED")
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&receipt).Error; err != nil {
			return err
		}
		if err := tx.First(&receipt, "notification_id = ? AND device_id = ? AND user_id = ?", receipt.NotificationID, device, owner).Error; err != nil {
			return err
		}
		status := "unavailable"
		if receipt.Status == "delivered" {
			status = "sent"
		}
		// Recheck the mutable state in the UPDATE itself. A concurrent settings
		// transaction may have disabled this notice after the initial SELECT.
		// Keep the real receipt for audit without overwriting its disabled state.
		return tx.Model(&orm.TaskNotification{}).Where("id = ? AND user_id = ? AND status <> ?", receipt.NotificationID, owner, "skipped").Updates(map[string]any{"status": status, "reason": receipt.Reason, "updated_at": time.Now().UTC()}).Error
	})
	if err != nil {
		replyNotificationError(w, r, err)
		return
	}
	common.ReplyOK(w, receipt)
}
