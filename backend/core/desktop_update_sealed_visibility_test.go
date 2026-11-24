package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/internal/updateformattest"
)

func TestSealedCoreStartupDoesNotBackfillUserDocumentsBeforeCommit(t *testing.T) {
	root := t.TempDir()
	corePath, lazyPath := filepath.Join(root, "core.db"), filepath.Join(root, "documents.db")
	statePath := filepath.Join(root, "core-state.db")
	if err := updateformattest.CreateCoreDatabase(corePath); err != nil {
		t.Fatal(err)
	}
	open := func(path string) *gorm.DB {
		t.Helper()
		db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = raw.Close() })
		return db
	}
	core, lazy := open(corePath), open(lazyPath)
	t.Setenv("LAZYMIND_READONLY_SCHEMA", "")
	if err := lazy.AutoMigrate(&readonlyorm.LazyLLMKBDocRow{}, &readonlyorm.LazyLLMDocRow{}); err != nil {
		t.Fatal(err)
	}
	ds := orm.Dataset{ID: "fixture-library", KbID: "fixture-kb", ResourceUID: "fixture-library", DisplayName: "Library", DatasetInfo: json.RawMessage(`{}`), ProcessingConfig: json.RawMessage(`{}`), BaseModel: orm.BaseModel{CreateUserID: "fixture-owner"}}
	if err := core.Create(&ds).Error; err != nil {
		t.Fatal(err)
	}
	if err := lazy.Create(&readonlyorm.LazyLLMKBDocRow{KbID: ds.KbID, DocID: "old-document"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := lazy.Create(&readonlyorm.LazyLLMDocRow{DocID: "old-document", Filename: "source.txt", Path: "/historical/source.txt"}).Error; err != nil {
		t.Fatal(err)
	}
	storage, err := json.Marshal(map[string]any{"version": 2, "databases": map[string]string{"core": corePath, "core-state": statePath}})
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestComponentMaintenanceEntrypointChild$")
	tx := strings.Repeat("a", 32)
	child.Env = []string{
		"PATH=" + os.Getenv("PATH"), "SystemRoot=" + os.Getenv("SystemRoot"),
		"LAZYMIND_UNIT_COMPONENT_ENTRY=1", "LAZYMIND_UNIT_CORE_BOOTSTRAP_ENTRY=1",
		"LAZYMIND_UPDATE_BOOTSTRAP_TRANSACTION=" + tx, "LAZYMIND_UPDATE_BOOTSTRAP_OWNER=fixture-owner", "LAZYMIND_UPDATE_BOOTSTRAP_STORAGE=" + string(storage),
		"LAZYMIND_RUNTIME_MODE=local", "LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN=fixture-token",
		"ACL_DB_DRIVER=sqlite", "ACL_DB_DSN=" + corePath, "LAZYMIND_READONLY_DB_DRIVER=sqlite", "LAZYMIND_READONLY_DB_DSN=" + lazyPath, "LAZYMIND_READONLY_SCHEMA=",
		"LAZYMIND_STATE_BACKEND=sqlite", "LAZYMIND_STATE_SQLITE_PATH=" + statePath,
		"LAZYMIND_CORE_HOST=127.0.0.1", "LAZYMIND_CORE_PORT=0", "LAZYMIND_OPENAPI_ARTIFACT_EXPORT_ENABLED=false",
		"LAZYMIND_BACKGROUND_JOBS_ENABLED=false", "LAZYMIND_HISTORY_INJECTION_ENABLED=false", "LAZYMIND_RAG_ENABLED=false",
		"LAZYMIND_WORKFLOW_BUILTIN_ROOT=" + t.TempDir(),
	}
	logfile, err := os.Create(filepath.Join(root, "core.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logfile.Close()
	child.Stdout, child.Stderr = logfile, logfile
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = child.Wait(); close(done) }()
	defer func() { cancel(); <-done }()
	listening := regexp.MustCompile(`Core listening[^\n]*?(127\.0\.0\.1:[0-9]+)`)
	var address string
	for address == "" {
		raw, err := os.ReadFile(logfile.Name())
		if err != nil {
			t.Fatal(err)
		}
		if match := listening.FindSubmatch(raw); match != nil {
			address = string(match[1])
			break
		}
		select {
		case <-done:
			t.Fatalf("Core exited before listening:\n%s", raw)
		case <-ctx.Done():
			t.Fatalf("Core startup timed out:\n%s", raw)
		case <-time.After(20 * time.Millisecond):
		}
	}
	type proof struct {
		Bootstrap *struct {
			TransactionID, Owner string
			Sealed               bool
		}
	}
	control := func(action, owner string, wantCode int) proof {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+address+"/internal/desktop-update",
			strings.NewReader(`{"action":"`+action+`","transactionId":"`+tx+`","owner":"`+owner+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-LazyMind-Internal-Token", "fixture-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
		if err != nil || resp.StatusCode != wantCode {
			logs, _ := os.ReadFile(logfile.Name())
			t.Fatalf("%s: status=%d body=%s err=%v\n%s", action, resp.StatusCode, body, err, logs)
		}
		var result proof
		if wantCode == http.StatusOK {
			if err := json.Unmarshal(body, &result); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	checkRows := func(stage string, want int64) {
		t.Helper()
		checkpoint := core.Model(&orm.DocumentOwnershipBackfill{}).Where("dataset_id = ?", ds.ID)
		if want != 0 {
			checkpoint = checkpoint.Where("kb_id = ? AND completed_at IS NOT NULL", ds.KbID)
		}
		document := core.Model(&orm.Document{}).Where("dataset_id = ? AND lazyllm_doc_id = ?", ds.ID, "old-document")
		for name, q := range map[string]*gorm.DB{"ownership checkpoint": checkpoint, "document mapping": document} {
			var rows int64
			if err := q.Count(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if rows != want {
				t.Errorf("%s: %s has %d rows, want %d", stage, name, rows, want)
			}
		}
	}
	current := control("status", "fixture-owner", http.StatusOK)
	if current.Bootstrap == nil || !current.Bootstrap.Sealed || current.Bootstrap.TransactionID != tx || current.Bootstrap.Owner != "fixture-owner" {
		t.Fatalf("live Core did not prove the expected bootstrap seal: %+v", current.Bootstrap)
	}
	checkRows("sealed and listening", 0)
	control("bootstrap-complete", "wrong-owner", http.StatusConflict)
	checkRows("wrong owner rejected", 0)
	if control("bootstrap-complete", "fixture-owner", http.StatusOK).Bootstrap != nil {
		t.Fatal("correct owner did not release the bootstrap seal")
	}
	checkRows("released", 1)
}
