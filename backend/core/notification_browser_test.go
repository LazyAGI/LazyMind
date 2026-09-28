package main

import (
	"testing"
)

func TestNotificationBrowserFeedAcrossDeployments(t *testing.T) {
	for _, mode := range []string{"local", "desktop", "cloud", ""} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("LAZYMIND_RUNTIME_MODE", mode)
			a := newNotificationAPI(t)
			a.schedule("browser-schedule", false)
			a.completeRun("browser-run", "browser-schedule", "浏览器真实任务结果摘要")
			path := "/task-center/desktop-notifications?device_id=browser"
			feed := a.data("GET", path, "owner", nil)
			items := notificationItems(t, feed["items"])
			if feed["device_id"] != "browser" || len(items) != 1 {
				t.Fatalf("invalid browser feed: %#v", feed)
			}
			notice := notificationObject(t, items[0])
			if notice["user_id"] != "owner" {
				t.Fatal("system notification feed must include the authenticated owner for client identity verification")
			}
			if notice["title"] != "每日简报" || notice["body"] != "浏览器真实任务结果摘要" {
				t.Fatal("lost actual result")
			}
			if len(notificationItems(t, a.data("GET", path, "other", nil)["items"])) != 0 {
				t.Fatal("cross-user disclosure")
			}
			notificationError(t, a.request("GET", path, "", nil), 401, "UNAUTHORIZED")
			ack := "/task-center/desktop-notifications/" + notice["notification_id"].(string) + ":ack"
			body := map[string]any{"device_id": "browser", "status": "delivered"}
			notificationError(t, a.request("POST", ack, "other", body), 404, "NOTIFICATION_NOT_FOUND")
			a.data("POST", ack, "owner", body)
			a.data("POST", ack, "owner", body)
			if len(notificationItems(t, a.data("GET", path, "owner", nil)["items"])) != 0 {
				t.Fatal("delivered notification replayed")
			}
			view := a.data("GET", "/schedules/browser-schedule/notifications", "owner", nil)
			availability := notificationObject(t, view["availability"])
			if notificationObject(t, availability["desktop"])["state"] != "available" {
				t.Fatalf("browser channel marked unavailable: %#v", view)
			}
			if mode == "cloud" || mode == "" {
				notificationError(t, a.request("GET", "/task-center/desktop-notifications?device_id=local", "owner", nil), 503, "NOTIFICATION_DEVICE_UNAVAILABLE")
			}
		})
	}
}

func TestNotificationBrowserRespectsGlobalGate(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "cloud")
	a := newNotificationAPI(t)
	a.schedule("browser-gate", false)
	a.completeRun("browser-before", "browser-gate", "result")
	feed := a.data("GET", "/task-center/desktop-notifications?device_id=browser", "owner", nil)
	notice := notificationObject(t, notificationItems(t, feed["items"])[0])
	prefs := a.data("GET", "/user/notification-preferences", "owner", nil)
	closed := a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": prefs["revision"], "enabled": false})
	notificationError(t, a.request("POST", "/task-center/desktop-notifications/"+notice["notification_id"].(string)+":ack", "owner", map[string]any{"device_id": "browser", "status": "delivered"}), 409, "NOTIFICATIONS_DISABLED")
	a.completeRun("browser-during", "browser-gate", "result")
	a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": closed["revision"], "enabled": true})
	if len(notificationItems(t, a.data("GET", "/task-center/desktop-notifications?device_id=browser", "owner", nil)["items"])) != 0 {
		t.Fatal("suppressed notifications replayed")
	}
}
