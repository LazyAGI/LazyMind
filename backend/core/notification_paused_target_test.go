package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"lazymind/core/common/orm"
)

func TestNotificationPausedTargetAllowsOtherRuleEditsWithoutResolving(t *testing.T) {
	for _, provider := range []string{"feishu", "wecom", "wechat"} {
		t.Run(provider, func(t *testing.T) {
			a := newNotificationAPI(t)
			var unavailable atomic.Bool
			var requests atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if unavailable.Load() {
					http.Error(w, "target no longer exists", http.StatusNotFound)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"provider": provider, "items": []map[string]any{{"recipient_id": "group", "available": true}}})
			}))
			defer gateway.Close()
			t.Setenv("LAZYMIND_CHANNEL_GATEWAY_BASE_URL", gateway.URL)
			path := a.schedule("paused-target-"+provider, false)
			config := notificationConfig(true)
			config["channels"].(map[string]any)[provider] = map[string]any{"enabled": true, "account_id": "account", "recipient_id": "group"}
			saved := a.save(path, config)
			before := requests.Load()
			unavailable.Store(true)
			config["events"].(map[string]any)["failed"].(map[string]any)["content"] = "full"
			got := a.data("PUT", path, "owner", map[string]any{"revision": saved["revision"], "config": config})
			if requests.Load() != before {
				t.Fatal("unchanged paused target was resolved while saving another rule")
			}
			persisted := notificationObject(t, got["config"])
			if notificationObject(t, notificationObject(t, persisted["events"])["failed"])["content"] != "full" {
				t.Fatal("unrelated event edit was not saved")
			}
			channel := notificationObject(t, notificationObject(t, persisted["channels"])[provider])
			if channel["enabled"] != true || channel["account_id"] != "account" || channel["recipient_id"] != "group" {
				t.Fatalf("paused selection changed: %#v", channel)
			}
		})
	}
}

func TestNotificationPausedUntargetedDefaultAllowsOtherRuleEdits(t *testing.T) {
	a := newNotificationAPI(t)
	defaults := notificationConfig(true)
	defaults["channels"].(map[string]any)["feishu"] = map[string]any{"enabled": true}
	raw, err := json.Marshal(defaults)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.Create(&orm.UserNotificationPreferences{UserID: "owner", Enabled: true, Revision: 1, Defaults: raw}).Error; err != nil {
		t.Fatal(err)
	}
	path := a.schedule("paused-untargeted-default", false)
	before := a.data("GET", path, "owner", nil)
	defaults["channels"].(map[string]any)["feishu"] = map[string]any{"enabled": false}
	raw, _ = json.Marshal(defaults)
	if err := a.db.Model(&orm.UserNotificationPreferences{}).Where("user_id = ?", "owner").Update("defaults", raw).Error; err != nil {
		t.Fatal(err)
	}
	config := notificationObject(t, before["config"])
	config["events"].(map[string]any)["failed"].(map[string]any)["content"] = "full"
	got := a.data("PUT", path, "owner", map[string]any{"revision": before["revision"], "config": config})
	feishu := notificationObject(t, notificationObject(t, notificationObject(t, got["config"])["channels"])["feishu"])
	if feishu["enabled"] != true || feishu["account_id"] != nil || feishu["recipient_id"] != nil {
		t.Fatalf("paused untargeted default was changed: %#v", feishu)
	}
}

func TestNotificationPausedChangedTargetsStillValidateOwnership(t *testing.T) {
	for _, change := range []string{"new channel", "account", "recipient", "enable saved disabled channel"} {
		t.Run(change, func(t *testing.T) {
			a := newNotificationAPI(t)
			var requests atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Header.Get("X-User-Id") != "owner" {
					t.Errorf("validation lost request owner: %q", r.Header.Get("X-User-Id"))
				}
				if strings.Contains(r.URL.Path, "foreign-account") || r.URL.Query().Get("recipient_id") == "foreign-group" {
					http.Error(w, "not owned", http.StatusNotFound)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"provider": "feishu", "items": []map[string]any{{"recipient_id": "group", "available": true}}})
			}))
			defer gateway.Close()
			t.Setenv("LAZYMIND_CHANNEL_GATEWAY_BASE_URL", gateway.URL)
			path := a.schedule("paused-changed-target", false)
			config := notificationConfig(true)
			channel := map[string]any{"enabled": true, "account_id": "account", "recipient_id": "group"}
			if change != "new channel" {
				if change == "enable saved disabled channel" {
					channel["enabled"] = false
					channel["account_id"] = "foreign-account"
				}
				config["channels"].(map[string]any)["feishu"] = channel
			}
			saved := a.save(path, config)
			before := requests.Load()
			channel["enabled"] = true
			if change == "recipient" {
				channel["recipient_id"] = "foreign-group"
			} else {
				channel["account_id"] = "foreign-account"
			}
			config["channels"].(map[string]any)["feishu"] = channel
			rec := a.request("PUT", path, "owner", map[string]any{"revision": saved["revision"], "config": config})
			notificationError(t, rec, http.StatusUnprocessableEntity, "FEISHU_NOTIFICATION_TARGET_UNAVAILABLE")
			if requests.Load() == before {
				t.Fatal("changed paused target bypassed ownership validation")
			}
		})
	}
}

func TestNotificationActiveTargetStillValidatesBeforeOtherRuleEdits(t *testing.T) {
	a := newNotificationAPI(t)
	var unavailable atomic.Bool
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unavailable.Load() {
			http.Error(w, "missing", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"provider": "feishu", "items": []map[string]any{{"recipient_id": "group", "available": true}}})
	}))
	defer gateway.Close()
	t.Setenv("LAZYMIND_CHANNEL_GATEWAY_BASE_URL", gateway.URL)
	defaults := notificationConfig(true)
	defaults["channels"].(map[string]any)["feishu"] = map[string]any{"enabled": true}
	raw, _ := json.Marshal(defaults)
	if err := a.db.Create(&orm.UserNotificationPreferences{UserID: "owner", Enabled: true, Revision: 1, Defaults: raw}).Error; err != nil {
		t.Fatal(err)
	}
	path := a.schedule("active-target", false)
	config := notificationConfig(true)
	config["channels"].(map[string]any)["feishu"] = map[string]any{"enabled": true, "account_id": "account", "recipient_id": "group"}
	saved := a.save(path, config)
	unavailable.Store(true)
	config["events"].(map[string]any)["failed"].(map[string]any)["content"] = "full"
	rec := a.request("PUT", path, "owner", map[string]any{"revision": saved["revision"], "config": config})
	notificationError(t, rec, http.StatusUnprocessableEntity, "FEISHU_NOTIFICATION_TARGET_UNAVAILABLE")
}

func TestNotificationPausedTargetRejectsConcurrentGateEnable(t *testing.T) {
	a := newNotificationAPI(t)
	a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": 1, "enabled": true})
	var enableDuringValidation atomic.Bool
	var feishuRequests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provider := "feishu"
		if strings.Contains(r.URL.Path, "wecom-account") {
			provider = "wecom"
			if enableDuringValidation.Load() {
				defaults := notificationConfig(true)
				defaults["channels"].(map[string]any)["feishu"] = map[string]any{"enabled": true}
				raw, _ := json.Marshal(defaults)
				if err := a.db.Model(&orm.UserNotificationPreferences{}).Where("user_id = ?", "owner").Update("defaults", raw).Error; err != nil {
					t.Errorf("enable gate before transaction: %v", err)
				}
			}
		} else {
			feishuRequests.Add(1)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"provider": provider, "items": []map[string]any{{"recipient_id": "group", "available": true}}})
	}))
	defer gateway.Close()
	t.Setenv("LAZYMIND_CHANNEL_GATEWAY_BASE_URL", gateway.URL)
	path := a.schedule("concurrent-gate-enable", false)
	config := notificationConfig(true)
	config["channels"].(map[string]any)["feishu"] = map[string]any{"enabled": true, "account_id": "account", "recipient_id": "group"}
	saved := a.save(path, config)
	before := feishuRequests.Load()
	enableDuringValidation.Store(true)
	config["channels"].(map[string]any)["wecom"] = map[string]any{"enabled": true, "account_id": "wecom-account", "recipient_id": "group"}
	rec := a.request("PUT", path, "owner", map[string]any{"revision": saved["revision"], "config": config})
	notificationError(t, rec, http.StatusConflict, "NOTIFICATION_CONFIG_CONFLICT")
	if feishuRequests.Load() != before {
		t.Fatal("unchanged paused target was remotely resolved during concurrent enable")
	}
	after := a.data("GET", path, "owner", nil)
	if after["revision"] != saved["revision"] {
		t.Fatal("concurrent gate enable committed an unvalidated target")
	}
}

func TestNotificationCreateAllowsPausedUntargetedDraft(t *testing.T) {
	a := newNotificationDraftAPI(t)
	config := notificationConfig(true)
	config["channels"].(map[string]any)["feishu"] = map[string]any{"enabled": true}
	created := draftScheduleData(t, a, "POST", "/schedules", map[string]any{
		"name": "paused draft", "cron_expr": "0 9 * * *", "timezone": "Asia/Shanghai", "prompt_template": "整理当天项目进度",
		"notification": map[string]any{"revision": 0, "config": config},
	})
	var persisted orm.UserSchedule
	if err := a.db.First(&persisted, "id = ?", created["id"]).Error; err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if persisted.NotificationConfig == nil || json.Unmarshal([]byte(*persisted.NotificationConfig), &got) != nil {
		t.Fatal("missing paused draft notification policy")
	}
	feishu := notificationObject(t, notificationObject(t, got["channels"])["feishu"])
	if feishu["enabled"] != true || feishu["account_id"] != nil || feishu["recipient_id"] != nil {
		t.Fatalf("paused draft selection changed: %#v", feishu)
	}
}

func TestNotificationPausedChangedValidTargetCanBeSaved(t *testing.T) {
	a := newNotificationAPI(t)
	var requests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("X-User-Id") != "owner" {
			t.Errorf("target validation did not use the owner: %q", r.Header.Get("X-User-Id"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"provider": "feishu", "items": []map[string]any{{"recipient_id": r.URL.Query().Get("recipient_id"), "available": true}}})
	}))
	defer gateway.Close()
	t.Setenv("LAZYMIND_CHANNEL_GATEWAY_BASE_URL", gateway.URL)
	path := a.schedule("paused-valid-change", false)
	config := notificationConfig(true)
	config["channels"].(map[string]any)["feishu"] = map[string]any{"enabled": true, "account_id": "account", "recipient_id": "first-group"}
	saved := a.save(path, config)
	before := requests.Load()
	config["channels"].(map[string]any)["feishu"].(map[string]any)["recipient_id"] = "second-group"
	a.data("PUT", path, "owner", map[string]any{"revision": saved["revision"], "config": config})
	if requests.Load() <= before {
		t.Fatal("valid target change was saved without validation")
	}
}

func TestNotificationAtomicEditRetainsPausedUnavailableTarget(t *testing.T) {
	a := newNotificationDraftAPI(t)
	var unavailable atomic.Bool
	var requests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if unavailable.Load() {
			http.Error(w, "missing", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"provider": "feishu", "items": []map[string]any{{"recipient_id": "group", "available": true}}})
	}))
	defer gateway.Close()
	t.Setenv("LAZYMIND_CHANNEL_GATEWAY_BASE_URL", gateway.URL)
	path := a.schedule("paused-atomic-edit", false)
	config := notificationConfig(true)
	config["channels"].(map[string]any)["feishu"] = map[string]any{"enabled": true, "account_id": "account", "recipient_id": "group"}
	saved := a.save(path, config)
	before := requests.Load()
	unavailable.Store(true)
	config["events"].(map[string]any)["failed"].(map[string]any)["content"] = "full"
	draftScheduleData(t, a, "PUT", "/schedules/paused-atomic-edit", map[string]any{
		"name": "edited with paused target", "notification": map[string]any{"revision": saved["revision"], "config": config},
	})
	var schedule orm.UserSchedule
	if err := a.db.First(&schedule, "id = ?", "paused-atomic-edit").Error; err != nil {
		t.Fatal(err)
	}
	if schedule.Name != "edited with paused target" || schedule.NotificationRevision != int64(saved["revision"].(float64))+1 || requests.Load() != before {
		t.Fatalf("atomic task edit did not preserve its paused target: %#v, requests=%d", schedule, requests.Load())
	}
}

func TestNotificationBatchCreateAllowsPausedUntargetedDraft(t *testing.T) {
	a := newNotificationDraftAPI(t)
	config := notificationConfig(true)
	config["channels"].(map[string]any)["feishu"] = map[string]any{"enabled": true}
	draftScheduleData(t, a, "POST", "/automation-groups:batch-create", map[string]any{
		"group": map[string]any{"name": "paused draft group", "timezone": "Asia/Shanghai"},
		"tasks": []map[string]any{{"client_key": "draft", "name": "paused draft", "cron_expr": "0 9 * * *", "prompt_template": "整理当天项目进度",
			"notification": map[string]any{"revision": 0, "config": config}}},
	})
	var schedule orm.UserSchedule
	if err := a.db.First(&schedule, "user_id = ? AND name = ?", "owner", "paused draft").Error; err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if schedule.NotificationConfig == nil || json.Unmarshal([]byte(*schedule.NotificationConfig), &got) != nil {
		t.Fatal("missing batch-created paused draft notification policy")
	}
	feishu := notificationObject(t, notificationObject(t, got["channels"])["feishu"])
	if feishu["enabled"] != true || feishu["account_id"] != nil || feishu["recipient_id"] != nil {
		t.Fatalf("batch-created paused draft selection changed: %#v", feishu)
	}
}
