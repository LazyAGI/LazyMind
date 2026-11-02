package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"gorm.io/gorm"
	"lazymind/core/common/maintenancegate"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/realtime"
)

func updateControlRequest(t *testing.T, h http.Handler, action string, epoch int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/internal/desktop-update", strings.NewReader(fmt.Sprintf(`{"action":%q,"epoch":%d}`, action, epoch)))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-LazyMind-Internal-Token", "fixture-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestDesktopUpdateAdmissionAndFinalization(t *testing.T) {
	gate := maintenancegate.New()
	gate.Enter()
	c := &desktopUpdateControl{token: "fixture-token", count: func(context.Context) (int64, error) { return 0, nil }, participants: []desktopUpdateParticipant{{gate.Pause, gate.Resume}}}
	entered, finish, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	h := c.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/work" {
			close(entered)
			<-finish
		}
		w.WriteHeader(204)
	}))
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/work", nil))
		close(done)
	}()
	<-entered
	w := updateControlRequest(t, h, "pause", 1)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	var state struct {
		Active int64
		Paused bool
	}
	if json.Unmarshal(w.Body.Bytes(), &state) != nil || state.Active != 2 || !state.Paused {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/new-task", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "2004817") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events", nil))
	if w.Code != 204 {
		t.Fatal("observer blocked")
	}
	close(finish)
	<-done
	gate.Leave()
	w = updateControlRequest(t, h, "pause", 1)
	_ = json.Unmarshal(w.Body.Bytes(), &state)
	if state.Active != 0 || !state.Paused {
		t.Fatal(w.Body.String())
	}
	if updateControlRequest(t, h, "resume", 2).Code != 409 {
		t.Fatal("stale resume accepted")
	}
	if updateControlRequest(t, h, "resume", 1).Code != 200 || !gate.Enter() {
		t.Fatal("resume did not reopen work")
	}
	gate.Leave()
	if updateControlRequest(t, h, "pause", 1).Code != 409 {
		t.Fatal("resumed epoch reused")
	}
}
func TestDesktopUpdateAuthAndUnknownState(t *testing.T) {
	c := &desktopUpdateControl{token: "fixture-token", count: func(context.Context) (int64, error) { return 0, errors.New("private database detail") }}
	h := c.wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("control escaped handler") }))
	for _, mutation := range []func(*http.Request){func(r *http.Request) { r.Header.Set("Origin", "https://fixture.invalid") }, func(r *http.Request) { r.RemoteAddr = "192.0.2.1:1234" }, func(r *http.Request) { r.Header.Del("X-LazyMind-Internal-Token") }} {
		r := httptest.NewRequest("POST", "/internal/desktop-update", strings.NewReader(`{"action":"pause","epoch":1}`))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("X-LazyMind-Internal-Token", "fixture-token")
		mutation(r)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 || c.epoch != 0 {
			t.Fatal("unauthorized pause")
		}
	}
	w := updateControlRequest(t, h, "status", 0)
	if w.Code != 503 || strings.Contains(w.Body.String(), "database") {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestDesktopUpdateLeaseExpiresWithoutCancellingAdmittedWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := maintenancegate.New()
		gate.Enter()
		c := &desktopUpdateControl{token: "fixture-token", count: func(context.Context) (int64, error) { return 0, nil }, participants: []desktopUpdateParticipant{{gate.Pause, gate.Resume}}}
		h := c.wrap(http.NotFoundHandler())
		updateControlRequest(t, h, "pause", 1)
		time.Sleep(90 * time.Second)
		updateControlRequest(t, h, "pause", 1)
		time.Sleep(40 * time.Second)
		if gate.Enter() {
			t.Fatal("old lease expired a renewed pause")
		}
		time.Sleep(81 * time.Second)
		if !gate.Enter() {
			t.Fatal("controller crash left admission closed")
		}
		gate.Leave()
		gate.Leave()
	})
}
func TestDesktopUpdatePartialPauseRollsBack(t *testing.T) {
	gate := maintenancegate.New()
	c := &desktopUpdateControl{token: "fixture-token", count: func(context.Context) (int64, error) { return 0, nil }, participants: []desktopUpdateParticipant{{gate.Pause, gate.Resume}, {func(uint64) (*maintenancegate.Ticket, error) { return nil, errors.New("unavailable") }, func(uint64) error { return errors.New("not paused") }}}}
	if updateControlRequest(t, c.wrap(http.NotFoundHandler()), "pause", 1).Code != 503 || !gate.Enter() || c.epoch != 0 {
		t.Fatal("partial pause leaked")
	}
	gate.Leave()
}
func TestDesktopUpdateGatesMultiplexedChatWithoutCountingIdleSocket(t *testing.T) {
	c := &desktopUpdateControl{token: "fixture-token", count: func(context.Context) (int64, error) { return 0, nil }}
	r := mux.NewRouter()
	r.Use(c.wrap)
	r.Handle("/realtime/connect", realtime.Handler{Routes: r, Authorize: func(*http.Request) int { return 200 }})
	r.HandleFunc("/internal/desktop-update", c.control)
	r.HandleFunc("/conversations:chat", func(w http.ResponseWriter, r *http.Request) { t.Error("chat reached handler while paused") })
	s := httptest.NewServer(r)
	defer s.Close()
	dialer := websocket.Dialer{Subprotocols: []string{"lazymind.realtime.v1"}}
	ws, _, err := dialer.Dial("ws"+strings.TrimPrefix(s.URL, "http")+"/realtime/connect", http.Header{"Origin": []string{s.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	w := updateControlRequest(t, r, "pause", 1)
	var state struct{ Active int64 }
	_ = json.Unmarshal(w.Body.Bytes(), &state)
	if state.Active != 0 {
		t.Fatal("idle websocket blocks updates")
	}
	if err := ws.WriteJSON(map[string]string{"type": "open", "id": "chat", "method": "POST", "path": "/conversations:chat", "authorization": "Bearer fixture"}); err != nil {
		t.Fatal(err)
	}
	_ = ws.SetReadDeadline(time.Now().Add(time.Second))
	seen := false
	for i := 0; i < 4; i++ {
		var frame struct {
			Type   string
			Status int
			Data   string
		}
		if err := ws.ReadJSON(&frame); err != nil {
			t.Fatal(err)
		}
		if frame.Status == 503 || strings.Contains(frame.Data, "2004817") {
			seen = true
		}
		if frame.Type == "end" {
			break
		}
	}
	if !seen {
		t.Fatal("multiplexed chat did not receive maintenance rejection")
	}
	updateControlRequest(t, r, "resume", 1)
}
func TestDesktopUpdateCountsDurableWorkIncludingArchivedAndRemoteDocuments(t *testing.T) {
	t.Setenv("LAZYMIND_READONLY_SCHEMA", "")
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "core.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	for _, model := range []any{&orm.TaskCenterTask{}, &orm.AsyncJob{}, &orm.SubAgentTask{}, &orm.ExternalChatRun{}, &orm.ChatHistory{}, &orm.MultiAnswersChatHistory{}, &orm.Task{}, &readonlyorm.LazyLLMDocServiceTaskRow{}} {
		if err := db.AutoMigrate(model); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`CREATE TABLE plugin_session_steps (validity TEXT, status TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	future := now.Add(time.Hour)
	rows := []any{&orm.TaskCenterTask{ID: "active", Status: "running", ArchivedAt: &now}, &orm.TaskCenterTask{ID: "future", Status: "pending", ScheduledFireAt: &future}, &orm.AsyncJob{ID: "queued", Status: "pending"}, &orm.SubAgentTask{ID: "done", Status: "succeeded", InputSlots: json.RawMessage(`[]`), OutputSlots: json.RawMessage(`[]`)}, &orm.Task{ID: "native", Ext: json.RawMessage(`{"task_state":"running"}`)}, &readonlyorm.LazyLLMDocServiceTaskRow{TaskID: "document", Status: "RUNNING"}}
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	n, err := desktopUpdateTaskCount(context.Background(), db, db, true)
	if err != nil || n != 4 {
		t.Fatal(n, err)
	}
	if _, err := desktopUpdateTaskCount(context.Background(), db, nil, true); err == nil {
		t.Fatal("missing RAG state reported idle")
	}
	if err := db.Exec(`DROP TABLE async_jobs`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := desktopUpdateTaskCount(context.Background(), db, db, true); err == nil {
		t.Fatal("query failure reported idle")
	}
}
