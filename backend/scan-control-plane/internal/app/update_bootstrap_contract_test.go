package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/lazymind/scan_control_plane/internal/config"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/schedule"
	scanstate "github.com/lazymind/scan_control_plane/internal/state"
	sourcestore "github.com/lazymind/scan_control_plane/internal/store/source"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type scanBootstrapParticipant interface {
	BeginUpdateBootstrap(transactionID, owner string) error
	CompleteUpdateBootstrap(transactionID, owner string) error
}

type bootstrapScanWork struct{ calls atomic.Int64 }

func (w *bootstrapScanWork) RecoverLocalWatchers(context.Context, time.Time) (int, error) {
	w.calls.Add(1)
	return 0, nil
}
func (w *bootstrapScanWork) MaintainAgentCommands(context.Context, time.Time, int) (int64, error) {
	w.calls.Add(1)
	return 0, nil
}
func (w *bootstrapScanWork) EnqueueDueSyncRuns(context.Context, int) ([]schedule.SyncRunIntent, error) {
	w.calls.Add(1)
	return nil, nil
}

func bootstrapScanConfig(t *testing.T) config.Config {
	t.Helper()
	root := t.TempDir()
	for key, value := range map[string]string{
		"LAZYMIND_RUNTIME_MODE": "local", "LAZYMIND_STATE_BACKEND": "sqlite", "LAZYMIND_REDIS_URL": "", "LAZYMIND_SCAN_CONTROL_PLANE_REDIS_URL": "",
		"LAZYMIND_STATE_SQLITE_PATH":            filepath.Join(root, "state.db"),
		"LAZYMIND_SCAN_CONTROL_PLANE_DB_DRIVER": "sqlite", "LAZYMIND_SCAN_CONTROL_PLANE_DB_DSN": filepath.Join(root, "scan.db"),
		"LAZYMIND_SCAN_CONTROL_PLANE_CORE_BASE_URL": "http://127.0.0.1:1", "LAZYMIND_SCAN_CONTROL_PLANE_AGENT_BASE_URL": "http://127.0.0.1:1",
		"LAZYMIND_SCAN_CONTROL_PLANE_AUTH_SERVICE_BASE_URL": "http://127.0.0.1:1", "LAZYMIND_SCAN_CONTROL_PLANE_FEISHU_BASE_URL": "http://127.0.0.1:1",
		"LAZYMIND_SCAN_CONTROL_PLANE_TEMP_DIR": filepath.Join(root, "tmp"), "LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN": "fixture-internal-token",
	} {
		t.Setenv(key, value)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func scanSchemaForBootstrap(t *testing.T, filename string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", filename)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query("SELECT type||':'||name||':'||coalesce(sql,'') FROM sqlite_schema ORDER BY type,name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		result = append(result, value)
	}
	return result
}

func setScanBootstrap(t *testing.T, cfg config.Config) config.Config {
	t.Helper()
	t.Setenv("LAZYMIND_UPDATE_BOOTSTRAP_TRANSACTION", strings.Repeat("a", 32))
	t.Setenv("LAZYMIND_UPDATE_BOOTSTRAP_OWNER", "fixture-owner")
	raw, _ := json.Marshal(map[string]any{"version": 2, "databases": map[string]string{"scan": cfg.DBDSN, "scan-state": os.Getenv("LAZYMIND_STATE_SQLITE_PATH")}})
	t.Setenv("LAZYMIND_UPDATE_BOOTSTRAP_STORAGE", string(raw))
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func TestUpdateBootstrapAppRejectsUnknownSchemaBeforeBuildCanMigrate(t *testing.T) {
	cfg := bootstrapScanConfig(t)
	db, err := sql.Open("sqlite", cfg.DBDSN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE fixture_unknown (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	before := scanSchemaForBootstrap(t, cfg.DBDSN)
	cfg = setScanBootstrap(t, cfg)
	app, err := NewWithConfig(cfg)
	if app != nil {
		_ = app.server.Close()
	}
	after := scanSchemaForBootstrap(t, cfg.DBDSN)
	if err == nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("target Build repaired/migrated unverified source schema: err=%v before=%d after=%d", err, len(before), len(after))
	}
}

func TestUpdateBootstrapRealAppSharesFenceAcrossStartAndHTTP(t *testing.T) {
	cfg := bootstrapScanConfig(t)
	db, err := sql.Open("sqlite", cfg.DBDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := sourcestore.NewSQLRepositoryWithDriver("sqlite", db).AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	state, err := scanstate.NewSQLiteStore(os.Getenv("LAZYMIND_STATE_SQLITE_PATH"))
	if err != nil {
		t.Fatal(err)
	}
	_ = state.Close()
	cfg = setScanBootstrap(t, cfg)
	application, err := NewWithConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer application.server.Close()
	if application.runtime.updateGate == nil {
		t.Fatal("production App did not install its own gate")
	}
	work := &bootstrapScanWork{}
	// Replace only external work boundaries. App-created gate and Runtime.Start
	// remain real; this observes the first watcher and due/expiry iterations.
	application.runtime.watcherRecovery = work
	application.runtime.scheduler = work
	application.runtime.commandMaintenance = work
	application.runtime.parseRunner = nil
	application.runtime.crawlWorker = nil
	application.runtime.reconcilerRunner = nil
	application.runtime.bindingCleanupRunner = nil
	application.runtime.tempCleanupRunner = nil
	application.runtime.targetCachePrewarmer = nil
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		done := application.runtime.Start(ctx)
		defer func() { cancel(); <-done }()
		time.Sleep(3 * time.Minute)
		if work.calls.Load() != 0 {
			t.Fatalf("production Runtime.Start executed %d jobs while sealed", work.calls.Load())
		}
		response := httptest.NewRecorder()
		application.server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/fixture-mutation", nil))
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("App HTTP bypassed initial fence: %d", response.Code)
		}
		participant := requireScanBootstrap(t, application.runtime.updateGate)
		if err := participant.CompleteUpdateBootstrap(strings.Repeat("a", 32), "fixture-owner"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(31 * time.Second)
		if work.calls.Load() == 0 {
			t.Fatal("released production runtime never executed retained work")
		}
	})
}

func requireScanBootstrap(t *testing.T, gate *desktopUpdateGate) scanBootstrapParticipant {
	t.Helper()
	participant, ok := any(gate).(scanBootstrapParticipant)
	if !ok {
		t.Fatal("Scan has no transaction-bound, non-expiring startup fence")
	}
	return participant
}

func TestUpdateBootstrapScanBlocksFirstLoopAndDoesNotExpire(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := &desktopUpdateGate{token: "fixture-token", count: func(context.Context) (int64, error) { return 0, nil }}
		participant := requireScanBootstrap(t, gate)
		tx := strings.Repeat("a", 32)
		if err := participant.BeginUpdateBootstrap(tx, "fixture-owner"); err != nil {
			t.Fatal(err)
		}
		runtime := &Runtime{updateGate: gate}
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		t.Cleanup(func() { cancel(); wg.Wait() })
		var calls atomic.Int64
		runtime.startLoop(ctx, &wg, time.Second, func(context.Context) { calls.Add(1) })
		time.Sleep(3 * time.Minute)
		if calls.Load() != 0 {
			t.Fatal("first loop or preparation lease bypassed startup verification")
		}
		// Ordinary source pause/resume cannot release a target bootstrap.
		scanUpdateRequest(gate, "pause", 17)
		scanUpdateRequest(gate, "resume", 17)
		time.Sleep(3 * time.Minute)
		if calls.Load() != 0 {
			t.Fatal("ordinary resume/controller loss reopened target jobs")
		}
		var state map[string]any
		if json.Unmarshal(scanUpdateRequest(gate, "status", 0).Body.Bytes(), &state) != nil {
			t.Fatal("unreadable bootstrap proof")
		}
		bootstrap, _ := state["bootstrap"].(map[string]any)
		if bootstrap["transactionId"] != tx || bootstrap["owner"] != "fixture-owner" || bootstrap["sealed"] != true {
			t.Fatalf("Scan must report its own startup proof: %+v", state)
		}
		if err := participant.CompleteUpdateBootstrap(tx, "different-owner"); err == nil {
			t.Fatal("another owner released startup")
		}
		if err := participant.CompleteUpdateBootstrap(strings.Repeat("b", 32), "fixture-owner"); err == nil {
			t.Fatal("another transaction released startup")
		}
		if err := participant.CompleteUpdateBootstrap(tx, "fixture-owner"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second)
		if calls.Load() == 0 {
			t.Fatal("validated Scan never resumed")
		}
		if err := participant.CompleteUpdateBootstrap(tx, "fixture-owner"); err != nil {
			t.Fatalf("lost release ACK cannot require another install: %v", err)
		}
	})
}

func TestUpdateBootstrapScanBlocksWritesButAllowsHealth(t *testing.T) {
	gate := &desktopUpdateGate{token: "fixture-token", count: func(context.Context) (int64, error) { return 0, nil }}
	p := requireScanBootstrap(t, gate)
	if err := p.BeginUpdateBootstrap(strings.Repeat("a", 32), "fixture-owner"); err != nil {
		t.Fatal(err)
	}
	var writes int
	handler := gate.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			writes++
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, "/fixture", nil))
		want := http.StatusServiceUnavailable
		if method == http.MethodGet {
			want = http.StatusNoContent
		}
		if response.Code != want {
			t.Fatalf("%s returned %d, want %d", method, response.Code, want)
		}
	}
	if writes != 0 {
		t.Fatal("Scan handled a mutation before target validation")
	}
}
