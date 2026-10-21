package academic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func TestArxivImportLockSurvivesPostgresIdleSessionTimeout(t *testing.T) {
	svc, downloads := arxivImportTestService(t)
	dialect, ok := svc.db.Dialector.(*postgres.Dialector)
	if !ok {
		t.Skip("PostgreSQL session timeout")
	}
	dsn, err := url.Parse(dialect.DSN)
	if err != nil {
		t.Fatal(err)
	}
	query := dsn.Query()
	query.Set("idle_session_timeout", "100ms")
	dsn.RawQuery = query.Encode()
	timed, err := orm.Connect("postgres", dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := timed.DB.DB()
	// Application sessions remain usable, as they do under ordinary query load;
	// the private lock session alone stays idle during the blocked download.
	pool.SetMaxIdleConns(0)
	defer pool.Close()
	svc.db = timed.DB
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	started, resume := make(chan struct{}), make(chan struct{})
	original := svc.acquire
	var calls atomic.Int32
	svc.acquire = func(ctx context.Context, candidate FulltextCandidate) (string, int64, error) {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-resume:
			case <-ctx.Done():
				return "", 0, ctx.Err()
			}
		}
		return original(ctx, candidate)
	}
	done := make(chan error, 2)
	importPaper := func() {
		_, err := svc.importPaper(ctx, "2404.06654v2", "dataset", "user", "User")
		done <- err
	}
	go importPaper()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Exceed the server timeout before starting the competing importer.
	time.Sleep(300 * time.Millisecond)
	go importPaper()
	time.Sleep(100 * time.Millisecond)
	close(resume)
	for range 2 {
		if err = <-done; err != nil {
			t.Fatal(err)
		}
	}
	var documents int64
	if err = svc.db.Model(&orm.Document{}).Count(&documents).Error; err != nil {
		t.Fatal(err)
	}
	if downloads.Load() != 1 || documents != 1 {
		t.Fatalf("idle lock session admitted duplicate imports: downloads=%d documents=%d", downloads.Load(), documents)
	}
}

func TestArxivImportCancelsWhenPostgresLockSessionIsLost(t *testing.T) {
	svc, _ := arxivImportTestService(t)
	if svc.db.Dialector.Name() != "postgres" {
		t.Skip("PostgreSQL session termination")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	started := make(chan struct{})
	svc.acquire = func(ctx context.Context, _ FulltextCandidate) (string, int64, error) {
		close(started)
		<-ctx.Done()
		return "", 0, ctx.Err()
	}
	done := make(chan error, 1)
	go func() {
		_, err := svc.importPaper(ctx, "2404.06654v2", "dataset", "user", "User")
		done <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	key := "academic-arxiv-import:2404.06654"
	var pid int
	err := svc.db.Raw(`SELECT pid FROM pg_locks
		WHERE locktype = 'advisory' AND granted
		AND database = (SELECT oid FROM pg_database WHERE datname = current_database())
		AND classid = ((hashtextextended(?, 0) >> 32) & 4294967295)::oid
		AND objid = (hashtextextended(?, 0) & 4294967295)::oid`, key, key).Scan(&pid).Error
	if err != nil || pid == 0 {
		t.Fatalf("find held import session: pid=%d err=%v", pid, err)
	}
	var terminated bool
	if err = svc.db.Raw("SELECT pg_terminate_backend(?)", pid).Scan(&terminated).Error; err != nil || !terminated {
		t.Fatalf("terminate import lock session: %v", err)
	}
	select {
	case err = <-done:
		if err == nil || !strings.Contains(err.Error(), "lock") || errors.Is(err, context.Canceled) {
			t.Fatalf("lock loss must retain its cause, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("import kept downloading after its database lock was lost")
	}
	var documents int64
	if err = svc.db.Model(&orm.Document{}).Count(&documents).Error; err != nil || documents != 0 {
		t.Fatalf("lost lock created documents=%d: %v", documents, err)
	}
}

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
	defer release.release()
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
			unlock.release()
		}
		if !errors.Is(waitErr, context.DeadlineExceeded) {
			t.Fatalf("cancelled waiter bypassed/released the active import lock: %v", waitErr)
		}
	}
	release.release()
	next, err := lockArxivImport(ctx, db, "2404.06654")
	if err != nil {
		t.Fatalf("released lock could not be reacquired: %v", err)
	}
	next.release()
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
	defer release.release()
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Millisecond)
	defer cancel()
	unexpected, err := lockArxivImport(ctx, direct.DB, "2404.06654")
	if unexpected != nil {
		unexpected.release()
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
