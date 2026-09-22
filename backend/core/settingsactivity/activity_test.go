package settingsactivity

import (
	"context"
	"fmt"
	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestActivityOwnershipAuthenticationAndLifecycle(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	cache, err := state.NewSQLiteStore(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	store.Init(db, nil, cache)
	defer store.Init(nil, nil, nil)
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "fixture-internal-token")
	if err := db.Create(&orm.Conversation{ID: "c", BaseModel: orm.BaseModel{CreateUserID: "owner"}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.MCPServer{ID: "m", Name: "MCP", Transport: "sse", HeadersJSON: []byte(`{}`), AllowedToolsJSON: []byte(`[]`), BaseModel: orm.BaseModel{CreateUserID: "owner"}}).Error; err != nil {
		t.Fatal(err)
	}
	report := func(user, token, capability, resource string, active bool) int {
		body := fmt.Sprintf(`{"id":"call","conversation_id":"c","capability":%q,"resource_id":%q,"active":%v}`, capability, resource, active)
		r := httptest.NewRequest(http.MethodPost, "/api/core/internal/settings/activity", strings.NewReader(body))
		r.Header.Set("X-User-Id", user)
		r.Header.Set("X-LazyMind-Internal-Token", token)
		w := httptest.NewRecorder()
		InternalReport(w, r)
		return w.Code
	}
	if code := report("owner", "", "mcp_enabled", "m", true); code != 401 {
		t.Fatalf("unauthenticated = %d", code)
	}
	if code := report("other", "fixture-internal-token", "mcp_enabled", "m", true); code != 403 {
		t.Fatalf("foreign owner = %d", code)
	}
	if code := report("owner", "fixture-internal-token", "mcp_enabled", "m", true); code != 200 {
		t.Fatalf("start = %d", code)
	}
	rows, err := Read(context.Background(), cache, "owner")
	if err != nil || len(rows) != 1 {
		t.Fatalf("read %+v %v", rows, err)
	}
	if code := report("owner", "fixture-internal-token", "mcp_enabled", "m", false); code != 200 {
		t.Fatalf("finish = %d", code)
	}
	rows, err = Read(context.Background(), cache, "owner")
	if err != nil || len(rows) != 0 {
		t.Fatalf("finished %+v %v", rows, err)
	}
	if code := report("owner", "fixture-internal-token", "mcp_enabled", "builtin", true); code != 200 {
		t.Fatal(code)
	}
	rows, _ = Read(context.Background(), cache, "owner")
	if len(rows) != 0 {
		t.Fatal("built-in counted against owned MCP switch")
	}
}

func TestActivityLockSerializesConcurrentSettingsUpdates(t *testing.T) {
	cache, err := state.NewSQLiteStore(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	var wg sync.WaitGroup
	count := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := WithLock(context.Background(), cache, "owner", func() error { count++; return nil }); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if count != 8 {
		t.Fatalf("count = %d", count)
	}
}
