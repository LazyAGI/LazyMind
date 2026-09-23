package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestToolConfigurationMailboxTargetIsVerifiedByBackend(t *testing.T) {
	db := newToolsTestDB(t)
	accountStatus := "ACTIVE"
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/token") {
			_, _ = w.Write([]byte(`{"data":{"access_token":"test-password"}}`))
			return
		}
		if r.URL.Query().Get("owner_user_id") != "owner" {
			t.Error("mailbox owner missing")
		}
		if r.URL.Query().Get("provider") == "qqmail" {
			_, _ = w.Write([]byte(strings.ReplaceAll(`{"data":{"items":[{"connection_id":"account","provider":"qqmail","display_name":"one@example.com","status":"ACTIVE"}]}}`, "ACTIVE", accountStatus)))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"items":[]}}`))
	}))
	defer auth.Close()
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", auth.URL)
	for _, tc := range []struct{ service, status string }{{"mail/one@example.com", "ready"}, {"mail/other@example.com", "needs_configuration"}, {"mail", "ready"}} {
		snapshot, err := probeToolConfiguration(t.Context(), db.DB, "owner", tc.service)
		if err != nil || snapshot.Status != tc.status {
			t.Fatalf("%s: %+v %v", tc.service, snapshot, err)
		}
	}
	accountStatus = "EXPIRED"
	snapshot, err := probeToolConfiguration(t.Context(), db.DB, "owner", "mail")
	if err != nil || snapshot.Status != "needs_authorization" {
		t.Fatalf("expired mailbox: %+v %v", snapshot, err)
	}

}

func TestToolConfigurationOwnershipVersionsAndDelivery(t *testing.T) {
	db := newToolsTestDB(t)
	if err := db.AutoMigrate(&ToolConfigurationAction{}); err != nil {
		t.Fatal(err)
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "test-internal")
	conv := orm.Conversation{ID: "c", BaseModel: orm.BaseModel{CreateUserID: "owner"}}
	if err := db.Create(&conv).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ChatHistory{ID: "h", ConversationID: "c", RunID: "r", RunStatus: "generating"}).Error; err != nil {
		t.Fatal(err)
	}
	server := orm.MCPServer{ID: "s", Name: "Documents", Transport: "http", URL: "https://example.com/mcp", Enabled: true,
		HeadersJSON: []byte(`{}`), AllowedToolsJSON: []byte(`[]`), BaseModel: orm.BaseModel{CreateUserID: "owner"}}
	if err := db.Create(&server).Error; err != nil {
		t.Fatal(err)
	}
	req := ToolConfigurationRequest{HistoryID: "h", RunID: "r", Service: "mcp:s"}
	action, err := prepareToolConfiguration(t.Context(), db.DB, "owner", "c", req)
	if err != nil {
		t.Fatal(err)
	}
	again, err := prepareToolConfiguration(t.Context(), db.DB, "owner", "c", req)
	if err != nil || again.ID != action.ID {
		t.Fatalf("dedup: %+v %v", again, err)
	}
	if _, err := prepareToolConfiguration(t.Context(), db.DB, "other", "c", req); err == nil {
		t.Fatal("cross-owner access")
	}
	req.RunID = "other-run"
	if _, err := prepareToolConfiguration(t.Context(), db.DB, "owner", "c", req); err == nil {
		t.Fatal("cross-task access")
	}
	if err := db.Model(&server).Updates(map[string]any{"is_verified": true, "allowed_tools_json": []byte(`["search"]`)}).Error; err != nil {
		t.Fatal(err)
	}
	snapshot, err := refreshToolConfiguration(t.Context(), db.DB, &action)
	if err != nil || snapshot.Status != "ready" || action.Version != 2 {
		t.Fatalf("refresh: %+v %+v %v", snapshot, action, err)
	}
	if _, err := refreshToolConfiguration(t.Context(), db.DB, &action); err != nil || action.Version != 2 {
		t.Fatal("non-idempotent read", err)
	}
	// A ready-to-ready allowlist change must also invalidate the old version.
	if err := db.Model(&server).Update("allowed_tools_json", []byte(`["read"]`)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := refreshToolConfiguration(t.Context(), db.DB, &action); err != nil || action.Version != 3 {
		t.Fatal("missing configuration revision", err)
	}
	call := func(user, token, body string) *httptest.ResponseRecorder {
		r := newSettingsRequest("POST", "/internal/conversations/c/tool-configuration-actions", body, user, map[string]string{"conversation_id": "c"})
		r.Header.Set("X-LazyMind-Internal-Token", token)
		w := httptest.NewRecorder()
		InternalToolConfiguration(w, r)
		return w
	}
	if w := call("owner", "", `{"operation":"list"}`); w.Code != 401 {
		t.Fatal("unauthenticated listing accepted")
	}
	if w := call("other", "test-internal", `{"operation":"list"}`); w.Code != 404 {
		t.Fatal("cross-owner listing accepted")
	}
	if w := call("owner", "test-internal", `{"operation":"list"}`); w.Code != 200 || !strings.Contains(w.Body.String(), action.ID) || strings.Contains(w.Body.String(), "example.com") {
		t.Fatal("invalid internal listing", w.Body.String())
	}
	payload := `{"operation":"ack","action_id":"` + action.ID + `","version":2,"request_id":"request"}`
	if w := call("owner", "", payload); w.Code != 401 {
		t.Fatal("missing internal token accepted")
	}
	if w := call("other", "test-internal", payload); w.Code != 404 {
		t.Fatal("cross-owner acknowledgement accepted")
	}
	if w := call("owner", "test-internal", payload); w.Code != 200 || !strings.Contains(w.Body.String(), `"acknowledged":false`) {
		t.Fatal(w.Body.String())
	}
	payload = strings.Replace(payload, `"version":2`, `"version":3`, 1)
	if w := call("owner", "test-internal", payload); w.Code != 200 || !strings.Contains(w.Body.String(), `"acknowledged":true`) {
		t.Fatal(w.Body.String())
	}
	w := httptest.NewRecorder()
	ListToolConfigurations(w, newSettingsRequest("GET", "/conversations/c/tool-configuration-actions", "", "owner", map[string]string{"conversation_id": "c"}))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var data map[string]any
	if json.Unmarshal(w.Body.Bytes(), &data) != nil {
		t.Fatal("invalid response")
	}
	if strings.Contains(w.Body.String(), "example.com") || strings.Contains(w.Body.String(), "revision") || strings.Contains(w.Body.String(), "request_id") {
		t.Fatal("private runtime data leaked")
	}
}

func TestToolConfigurationStreamForwarding(t *testing.T) {
	action := map[string]any{"id": "a", "status": "needs_authorization"}
	chunk := upstreamStreamChunkFromData(LazyChatData{ToolConfiguration: action})
	if !hasBusinessStreamPayload(chunk) || chunk.ToolConfiguration["id"] != "a" {
		t.Fatal("configuration event dropped")
	}
}
