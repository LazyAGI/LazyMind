package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/chat"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func newUpdateHostPollFixture(t *testing.T) (*gorm.DB, *mux.Router) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "core.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	t.Cleanup(func() { _ = sql.Close() })
	for _, model := range []any{&orm.TaskCenterTask{}, &orm.AsyncJob{}, &orm.SubAgentTask{}, &orm.ExternalChatRun{}, &orm.ExternalChatHost{}, &orm.ChatHistory{}, &orm.MultiAnswersChatHistory{}, &orm.Task{}} {
		if err := db.AutoMigrate(model); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`CREATE TABLE plugin_session_steps (validity TEXT, status TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	previousDB, previousLazy, previousState := store.DB(), store.LazyLLMDB(), store.State()
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(previousDB, previousLazy, previousState) })
	c := &desktopUpdateControl{token: "fixture-token", count: func(ctx context.Context) (int64, error) {
		return desktopUpdateTaskCount(ctx, db, nil, false)
	}}
	t.Cleanup(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.timer != nil {
			c.timer.Stop()
		}
	})
	router := mux.NewRouter()
	router.Use(c.wrap)
	router.HandleFunc("/internal/desktop-update", c.control).Methods(http.MethodPost)
	router.HandleFunc("/external-chat/hosts/{provider}/claim", chat.ClaimExternalChatRun).Methods(http.MethodPost)
	return db, router
}

func hostPollRequest(provider, host string, ready bool) *http.Request {
	encoded, _ := json.Marshal(map[string]any{"host_id": host, "installed": true, "ready": ready})
	r := httptest.NewRequest(http.MethodPost, "/external-chat/hosts/"+provider+"/claim", strings.NewReader(string(encoded)))
	r.Header.Set("X-User-Id", "fixture-owner")
	return r
}

func assertUpdateHostWork(t *testing.T, router http.Handler, action string, epoch int, active int64, paused bool) {
	t.Helper()
	w := updateControlRequest(t, router, action, epoch)
	var state struct {
		Active int64
		Paused bool
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &state) != nil || state.Active != active || state.Paused != paused {
		t.Fatalf("%s: status=%d body=%s, want active=%d paused=%v", action, w.Code, w.Body.String(), active, paused)
	}
}

func TestDesktopUpdateIdleHostPollDoesNotBlockPause(t *testing.T) {
	db, router := newUpdateHostPollFixture(t)
	for _, provider := range []string{"codex", "cursor", "workbuddy"} {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func(provider string) {
			defer close(done)
			router.ServeHTTP(httptest.NewRecorder(), hostPollRequest(provider, "idle-"+provider, false).WithContext(ctx))
		}(provider)
		t.Cleanup(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("idle poll did not exit on cancellation")
			}
		})
		deadline := time.Now().Add(time.Second)
		for {
			var count int64
			if err := db.Model(&orm.ExternalChatHost{}).Where("provider = ?", provider).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count == 1 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("host did not enter the real claim handler")
			}
			time.Sleep(time.Millisecond)
		}
		select {
		case <-done:
			t.Fatal("claim returned instead of waiting for work")
		default:
		}
	}
	assertUpdateHostWork(t, router, "status", 0, 0, false)
	assertUpdateHostWork(t, router, "pause", 1, 0, true)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, hostPollRequest("codex", "blocked-host", true))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatal("new claim was admitted during pause", w.Code)
	}
	var blocked int64
	if err := db.Model(&orm.ExternalChatHost{}).Where("host_id = ?", "blocked-host").Count(&blocked).Error; err != nil || blocked != 0 {
		t.Fatal("paused claim reached the handler", err, blocked)
	}
}

func TestDesktopUpdateClaimedAndQueuedRunsStillBlockInstallation(t *testing.T) {
	db, router := newUpdateHostPollFixture(t)
	first := orm.ExternalChatRun{ID: "claimed", RequestID: "request-claimed", ActorUserID: "fixture-owner", Provider: "codex", Status: "pending"}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	assertUpdateHostWork(t, router, "status", 0, 1, false)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, hostPollRequest("codex", "executing-host", true))
	if w.Code != http.StatusOK {
		t.Fatal("real claim failed", w.Code)
	}
	if err := db.First(&first, "id = ?", first.ID).Error; err != nil || first.Status != "running" || first.HostID != "executing-host" {
		t.Fatal("claim did not persist running work", err, first.Status)
	}
	second := orm.ExternalChatRun{ID: "queued", RequestID: "request-queued", ActorUserID: "fixture-owner", Provider: "codex", Status: "pending"}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	assertUpdateHostWork(t, router, "pause", 1, 2, true)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, hostPollRequest("codex", "blocked-host", true))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatal("paused host acquired queued work", w.Code)
	}
	if err := db.First(&second, "id = ?", second.ID).Error; err != nil || second.Status != "pending" {
		t.Fatal("paused queue changed", err, second.Status)
	}
	assertUpdateHostWork(t, router, "resume", 1, 2, false)
	if err := db.Model(&orm.ExternalChatRun{}).Where("id IN ?", []string{first.ID, second.ID}).Update("status", "completed").Error; err != nil {
		t.Fatal(err)
	}
	assertUpdateHostWork(t, router, "pause", 2, 0, true)
}

func TestDesktopUpdateAdmittedProducerRemainsWorkUntilDurableQueueExists(t *testing.T) {
	db, router := newUpdateHostPollFixture(t)
	entered, finish, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(finish) })
	// A lookalike route must retain ordinary POST admission/counting.
	router.HandleFunc("/external-chat/hosts/{provider}/claim-extra", func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-finish
		row := orm.ExternalChatRun{ID: "admitted", RequestID: "request-admitted", ActorUserID: "fixture-owner", Provider: "codex", Status: "pending"}
		if err := db.Create(&row).Error; err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}).Methods(http.MethodPost)
	go func() {
		defer close(done)
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/external-chat/hosts/codex/claim-extra", nil))
	}()
	t.Cleanup(func() { unblock(); <-done })
	<-entered
	assertUpdateHostWork(t, router, "pause", 1, 1, true)
	unblock()
	<-done
	assertUpdateHostWork(t, router, "pause", 1, 1, true)
}
