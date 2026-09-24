package chat

import (
	"encoding/json"
	"fmt"
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

func TestMCPConfigurationTargetAndBatchReuse(t *testing.T) {
	db := newToolsTestDB(t)
	if err := db.AutoMigrate(&ToolConfigurationAction{}); err != nil {
		t.Fatal(err)
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal")
	calls := map[string]int{}
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		id, _ := request["server_id"].(string)
		calls[id]++
		_, _ = w.Write([]byte(`{"code":200,"data":{"status":"authorized","grant_id":"g","grant_version":3}}`))
	}))
	defer auth.Close()
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", auth.URL)
	for _, id := range []string{"target", "unrelated"} {
		row := orm.MCPServer{ID: id, Name: id, Transport: "http", URL: "https://example.com/" + id, AuthType: "oauth", HeadersJSON: []byte(`{}`), Enabled: true, IsVerified: true, AllowedToolsJSON: []byte(`["search"]`), BaseModel: orm.BaseModel{CreateUserID: "owner"}}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := probeToolConfiguration(t.Context(), db.DB, "owner", "mcp:target"); err != nil {
		t.Fatal(err)
	}
	if calls["target"] != 1 || calls["unrelated"] != 0 {
		t.Fatalf("target check probes unrelated server: %v", calls)
	}
	if err := db.Create(&orm.Conversation{ID: "c", BaseModel: orm.BaseModel{CreateUserID: "owner"}}).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if err := db.Create(&ToolConfigurationAction{ID: id, UserID: "owner", ConversationID: "c", Service: "mcp:target", Version: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	calls = map[string]int{}
	r := newSettingsRequest("POST", "/internal/conversations/c/tool-configuration-actions", `{"operation":"poll_batch","action_ids":["a","b","a"]}`, "owner", map[string]string{"conversation_id": "c"})
	r.Header.Set("X-LazyMind-Internal-Token", "internal")
	w := httptest.NewRecorder()
	InternalToolConfiguration(w, r)
	if w.Code != 200 || calls["target"] != 1 || calls["unrelated"] != 0 {
		t.Fatalf("batch calls=%v code=%d body=%s", calls, w.Code, w.Body)
	}
	calls = map[string]int{}
	w = httptest.NewRecorder()
	ListToolConfigurations(w, newSettingsRequest("GET", "/conversations/c/tool-configuration-actions", "", "owner", map[string]string{"conversation_id": "c"}))
	if w.Code != 200 || calls["target"] != 1 || calls["unrelated"] != 0 {
		t.Fatalf("list calls=%v code=%d body=%s", calls, w.Code, w.Body)
	}
	// A missing or foreign action fails the entire batch before any auth probe.
	calls = map[string]int{}
	r = newSettingsRequest("POST", "/internal/conversations/c/tool-configuration-actions", `{"operation":"poll_batch","action_ids":["a","foreign"]}`, "owner", map[string]string{"conversation_id": "c"})
	r.Header.Set("X-LazyMind-Internal-Token", "internal")
	w = httptest.NewRecorder()
	InternalToolConfiguration(w, r)
	if w.Code != 404 || len(calls) != 0 {
		t.Fatalf("invalid batch performed probes: code=%d calls=%v", w.Code, calls)
	}
	calls = map[string]int{}
	body := map[string]any{}
	applyMCPRuntimeConfig(t.Context(), db.DB, "owner", "", body)
	if calls["target"] != 1 || calls["unrelated"] != 1 {
		t.Fatalf("runtime snapshot resolved twice: %v", calls)
	}
}

func TestToolConfigurationDatabaseFailureIsFatal(t *testing.T) {
	db := newToolsTestDB(t)
	if err := db.Migrator().DropTable(&orm.UserSelectedProvider{}); err != nil {
		t.Fatal(err)
	}
	if snapshot, err := probeToolConfiguration(t.Context(), db.DB, "owner", "web_search"); err == nil {
		t.Fatalf("database error converted into service status: %+v", snapshot)
	}
}

func TestConfigurationBatchAuthFailureIsolationAndRevocation(t *testing.T) {
	db := newToolsTestDB(t)
	if err := db.AutoMigrate(&ToolConfigurationAction{}); err != nil {
		t.Fatal(err)
	}
	authorized := true
	calls := map[string]int{}
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		id, _ := request["server_id"].(string)
		calls[id]++
		if id == "broken" {
			w.WriteHeader(503)
			return
		}
		status := "needs_authorization"
		if authorized {
			status = "authorized"
		}
		_, _ = fmt.Fprintf(w, `{"code":200,"data":{"status":%q,"grant_id":"grant","grant_version":3}}`, status)
	}))
	defer auth.Close()
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal")
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", auth.URL)
	actions := []ToolConfigurationAction{}
	for _, id := range []string{"broken", "healthy"} {
		row := orm.MCPServer{ID: id, Name: id, Transport: "http", URL: "https://example.com/" + id, AuthType: "oauth", Enabled: true, IsVerified: true, HeadersJSON: []byte(`{}`), AllowedToolsJSON: []byte(`["search"]`), BaseModel: orm.BaseModel{CreateUserID: "owner"}}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		action := ToolConfigurationAction{ID: id, UserID: "owner", ConversationID: "c", Service: "mcp:" + id, Version: 1}
		if err := db.Create(&action).Error; err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
	}
	snapshots, err := refreshToolConfigurations(t.Context(), db.DB, actions)
	if err != nil || snapshots["mcp:broken"].Status != "unavailable" || snapshots["mcp:healthy"].MCP == nil || calls["broken"] != 1 || calls["healthy"] != 1 {
		t.Fatalf("failure isolation: %+v calls=%v err=%v", snapshots, calls, err)
	}
	oldVersion := actions[1].Version
	authorized = false
	snapshots, err = refreshToolConfigurations(t.Context(), db.DB, actions)
	if err != nil || snapshots["mcp:healthy"].MCP != nil || snapshots["mcp:healthy"].Status != "needs_authorization" || actions[1].Version != oldVersion+1 {
		t.Fatalf("revocation: %+v actions=%+v err=%v", snapshots, actions, err)
	}
}
