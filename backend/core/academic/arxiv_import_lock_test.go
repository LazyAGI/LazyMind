package academic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func TestArxivImportLockCancellationAndMainDatabaseWrites(t *testing.T) {
	db := orm.OpenTestDB(t).DB
	pool, _ := db.DB()
	pool.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	release, err := lockArxivImport(ctx, db, "2404.06654")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	// A held lock must not occupy the main pool's only connection or SQLite's
	// main writer, because the real document bridge uses that pool independently.
	if err = db.WithContext(ctx).Exec("CREATE TABLE import_lock_probe (id INTEGER)").Error; err != nil {
		t.Fatalf("lock blocks document database writes: %v", err)
	}
	for i := 0; i < 2; i++ {
		waiting, stop := context.WithTimeout(ctx, 75*time.Millisecond)
		unlock, waitErr := lockArxivImport(waiting, db, "2404.06654")
		stop()
		if unlock != nil {
			unlock()
		}
		if !errors.Is(waitErr, context.DeadlineExceeded) {
			t.Fatalf("cancelled waiter bypassed/released the active import lock: %v", waitErr)
		}
	}
	release()
	next, err := lockArxivImport(ctx, db, "2404.06654")
	if err != nil {
		t.Fatalf("released lock could not be reacquired: %v", err)
	}
	next()
}

func TestSQLiteArxivImportLockUsesProxyDatabaseFilesystem(t *testing.T) {
	direct, err := orm.Connect("sqlite", filepath.Join(t.TempDir(), "shared.db"))
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := direct.DB.DB()
	defer pool.Close()
	var databases []struct{ Name, File string }
	if err = direct.Raw("PRAGMA database_list").Scan(&databases).Error; err != nil {
		t.Fatal(err)
	}
	var databasePath string
	for _, database := range databases {
		if database.Name == "main" {
			databasePath = database.File
		}
	}
	// The proxy returns database_list exactly as its underlying local SQLite
	// connection does. No transaction or lock is held on the proxy's main DB.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct{ SQL string }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var response any
		switch request.SQL {
		case "SELECT 1":
			response = map[string]any{"columns": []string{"1"}, "rows": []any{[]any{map[string]string{"type": "int", "int": "1"}}}}
		case "select sqlite_version()":
			response = map[string]any{"columns": []string{"sqlite_version()"}, "rows": []any{[]any{map[string]string{"type": "text", "text": "3.45.0"}}}}
		case "PRAGMA database_list":
			response = map[string]any{"columns": []string{"seq", "name", "file"}, "rows": []any{[]any{map[string]string{"type": "int", "int": "0"}, map[string]string{"type": "text", "text": "main"}, map[string]string{"type": "text", "text": databasePath}}}}
		default:
			t.Errorf("unexpected proxy SQL: %s", request.SQL)
			response = map[string]string{"error": "unexpected SQL"}
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err = os.WriteFile(tokenPath, []byte("test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_SQLITE_SERVER_URL", server.URL)
	t.Setenv("LAZYMIND_SQLITE_SERVER_TOKEN_FILE", tokenPath)
	proxy, err := orm.Connect("sqlite", "sqliteproxy://core")
	if err != nil {
		t.Fatal(err)
	}
	proxyPool, _ := proxy.DB.DB()
	defer proxyPool.Close()
	release, err := lockArxivImport(t.Context(), proxy.DB, "2404.06654")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Millisecond)
	defer cancel()
	unexpected, err := lockArxivImport(ctx, direct.DB, "2404.06654")
	if unexpected != nil {
		unexpected()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("proxy and direct connections did not share the database lock: %v", err)
	}
	t.Setenv("LAZYMIND_SQLITE_SERVER_URL", "https://remote-sqlite.example")
	if unlock, err := lockArxivImport(t.Context(), proxy.DB, "another-paper"); unlock != nil || err == nil {
		t.Fatalf("remote proxy silently used a host-local lock: %v", err)
	}
}

func TestSQLiteArxivImportLockRejectsMemoryDatabase(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := db.DB()
	defer pool.Close()
	unlock, err := lockArxivImport(t.Context(), db, "2404.06654")
	if unlock != nil || err == nil {
		t.Fatalf("import silently ran without a cross-process lock: %v", err)
	}
}
