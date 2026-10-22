package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/taskcenter"
)

func TestWeChatNotificationTargetsOnlyConfirmedOwnedScheduleRules(t *testing.T) {
	a := newNotificationAPI(t)
	var gatewayCalls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		gatewayCalls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer gateway.Close()
	t.Setenv("LAZYMIND_CHANNEL_GATEWAY_BASE_URL", gateway.URL)

	config := func(provider string, enabled bool, account, recipient string) *string {
		value := taskcenter.DefaultNotificationConfig()
		value.Channels[provider] = taskcenter.NotificationChannelRule{Enabled: enabled, AccountID: account, RecipientID: recipient}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		return &text
	}
	for _, row := range []orm.UserSchedule{
		{ID: "first", UserID: "owner", NotificationConfig: config("wechat", true, "account-b", "recipient-b")},
		{ID: "second", UserID: "owner", NotificationConfig: config("wechat", true, "account-a", "recipient-z")},
		{ID: "third", UserID: "owner", NotificationConfig: config("wechat", true, "account-a", "recipient-a")},
		{ID: "duplicate", UserID: "owner", NotificationConfig: config("wechat", true, "account-a", "recipient-a")},
		{ID: "disabled-rule", UserID: "owner", NotificationConfig: config("wechat", false, "disabled-account", "disabled-recipient")},
		{ID: "missing-account", UserID: "owner", NotificationConfig: config("wechat", true, "", "recipient")},
		{ID: "missing-recipient", UserID: "owner", NotificationConfig: config("wechat", true, "account", "")},
		{ID: "blank-account", UserID: "owner", NotificationConfig: config("wechat", true, "  ", "recipient")},
		{ID: "blank-recipient", UserID: "owner", NotificationConfig: config("wechat", true, "account", " \t")},
		{ID: "feishu-only", UserID: "owner", NotificationConfig: config("feishu", true, "feishu-account", "feishu-recipient")},
		{ID: "legacy", UserID: "owner"},
		{ID: "other-owner", UserID: "someone-else", NotificationConfig: config("wechat", true, "private-account", "private-recipient")},
		{ID: "deleted", UserID: "owner", NotificationConfig: config("wechat", true, "deleted-account", "deleted-recipient")},
	} {
		if err := a.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := a.db.Delete(&orm.UserSchedule{}, "id = ?", "deleted").Error; err != nil {
		t.Fatal(err)
	}
	// Pausing execution preserves a saved notification rule and its confirmed recipient.
	if err := a.db.Model(&orm.UserSchedule{}).Where("id = ?", "third").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.db.Create(&orm.TaskCenterTask{ID: "history", UserID: "owner", TaskType: "scheduled", Status: "running",
		NotificationConfig: config("wechat", true, "run-account", "run-recipient")}).Error; err != nil {
		t.Fatal(err)
	}
	// The caller applies the global gate. A closed gate must not hide saved recipients.
	if err := a.db.Create(&orm.UserNotificationPreferences{UserID: "owner", Enabled: false, Revision: 1, Defaults: []byte(`{"channels":{"wechat":{"enabled":false}}}`)}).Error; err != nil {
		t.Fatal(err)
	}

	got := a.data(http.MethodGet, "/user/wechat-notification-targets", "owner", nil)
	want := map[string]any{"items": []any{
		map[string]any{"account_id": "account-a", "recipient_id": "recipient-a"},
		map[string]any{"account_id": "account-a", "recipient_id": "recipient-z"},
		map[string]any{"account_id": "account-b", "recipient_id": "recipient-b"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected saved recipients: %#v", got)
	}
	if gatewayCalls.Load() != 0 {
		t.Fatalf("listing saved recipients must not query Gateway: %d calls", gatewayCalls.Load())
	}
}

func TestWeChatNotificationTargetsIsAuthenticatedAndReadOnly(t *testing.T) {
	a := newNotificationAPI(t)
	notificationError(t, a.request(http.MethodGet, "/user/wechat-notification-targets", "", nil), http.StatusUnauthorized, "UNAUTHORIZED")
	rec := a.request(http.MethodGet, "/user/wechat-notification-targets", "owner", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expected uncached successful response: %d %#v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	var body struct {
		Data struct {
			Items []any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Data.Items == nil || len(body.Data.Items) != 0 {
		t.Fatalf("expected empty array, got %s (error=%v)", rec.Body.String(), err)
	}
	var count int64
	if err := a.db.Model(&orm.UserNotificationPreferences{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("read-only lookup wrote preferences: count=%d error=%v", count, err)
	}
}

func TestWeChatNotificationTargetsReturnsSafeStorageErrors(t *testing.T) {
	a := newNotificationAPI(t)
	privateMalformedConfig := `{"channels":{"wechat":{"enabled":true,"account_id":"private-account"` // corrupt persisted value
	if err := a.db.Create(&orm.UserSchedule{ID: "corrupt", UserID: "owner", NotificationConfig: &privateMalformedConfig}).Error; err != nil {
		t.Fatal(err)
	}
	rec := a.request(http.MethodGet, "/user/wechat-notification-targets", "owner", nil)
	notificationError(t, rec, http.StatusInternalServerError, "NOTIFICATION_INTERNAL_ERROR")
	if strings.Contains(rec.Body.String(), "private-account") {
		t.Fatalf("storage errors leaked configuration: %s", rec.Body.String())
	}
}

func TestWeChatNotificationTargetsOpenAPIContract(t *testing.T) {
	router := mux.NewRouter()
	registerCoreRoutes(router)
	raw, err := buildOpenAPISpecFromRouter(router)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	paths := spec["paths"].(map[string]any)
	operation := paths["/api/core/user/wechat-notification-targets"].(map[string]any)["get"].(map[string]any)
	response := operation["responses"].(map[string]any)["200"].(map[string]any)
	schema := response["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	data := schema["properties"].(map[string]any)["data"].(map[string]any)
	if data["$ref"] != "#/components/schemas/WeChatNotificationTargets" {
		t.Fatalf("unexpected response contract: %#v", data)
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	targets := schemas["WeChatNotificationTargets"].(map[string]any)
	items := targets["properties"].(map[string]any)["items"].(map[string]any)
	if items["type"] != "array" || !reflect.DeepEqual(items["items"].(map[string]any)["required"], []any{"account_id", "recipient_id"}) {
		t.Fatalf("unexpected recipient list contract: %#v", targets)
	}
}
