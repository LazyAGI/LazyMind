package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/learning"
	"lazymind/core/scheduler"
	"lazymind/core/store"
)

func TestUpdateIgnoresDeletedSourceHistoryButCountsActiveDocuments(t *testing.T) {
	t.Setenv("LAZYMIND_READONLY_SCHEMA", "")
	db := updateActivityDB(t)
	if err := db.AutoMigrate(&orm.Document{}, &readonlyorm.LazyLLMDocServiceTaskRow{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, row := range []any{
		&orm.Document{ID: "removed", DatasetID: "library", BaseModel: orm.BaseModel{DeletedAt: &now}},
		&orm.Task{ID: "history", DatasetID: "library", DocID: "removed", Ext: json.RawMessage(`{"task_state":"UPLOADED"}`)},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	assertActive := func(want bool) {
		t.Helper()
		n, err := desktopUpdateTaskCount(t.Context(), db, db, true)
		if err != nil || (n > 0) != want {
			t.Errorf("active=%d, want work present=%v, err=%v", n, want, err)
		}
	}
	assertActive(false)
	if err := db.Create(&orm.Document{ID: "live", DatasetID: "library"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.Task{ID: "live-upload", DatasetID: "library", DocID: "live", Ext: json.RawMessage(`{"task_state":"UPLOADED"}`)}).Error; err != nil {
		t.Fatal(err)
	}
	assertActive(true)
	if err := db.Model(&orm.Task{}).Where("id = ?", "live-upload").Update("ext", json.RawMessage(`{"task_state":"SUCCESS"}`)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.Task{ID: "in-progress", DatasetID: "library", Ext: json.RawMessage(`{"task_state":"UPLOADING"}`)}).Error; err != nil {
		t.Fatal(err)
	}
	assertActive(true)
	if err := db.Model(&orm.Task{}).Where("id = ?", "in-progress").Update("ext", json.RawMessage(`{"task_state":"SUCCESS"}`)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&readonlyorm.LazyLLMDocServiceTaskRow{TaskID: "remote", DocID: "removed", Status: "RUNNING"}).Error; err != nil {
		t.Fatal(err)
	}
	assertActive(true)
	if err := db.Model(&readonlyorm.LazyLLMDocServiceTaskRow{}).Where("task_id = ?", "remote").Update("status", "SUCCESS").Error; err != nil {
		t.Fatal(err)
	}
	assertActive(false)
	var history orm.Task
	if err := db.First(&history, "id = ?", "history").Error; err != nil || history.DeletedAt != nil || string(history.Ext) != `{"task_state":"UPLOADED"}` {
		t.Fatalf("update check changed retained task history: %s, %v", history.Ext, err)
	}
}

func TestUpdateWaitsForLearningPreanalysisAfterRunResponse(t *testing.T) {
	t.Setenv("LAZYMIND_VOCABULARY_ENABLED", "true")
	db, router := newUpdateHostPollFixture(t)
	if err := db.AutoMigrate(&orm.Dataset{}, &orm.Document{}, &learning.KnowledgeBaseCapability{}, &learning.Preset{}, &learning.PreanalysisTask{}); err != nil {
		t.Fatal(err)
	}
	const owner = "fixture-owner"
	if err := db.Create(&orm.Dataset{ID: "library", BaseModel: orm.BaseModel{CreateUserID: owner}}).Error; err != nil {
		t.Fatal(err)
	}
	svc := learning.New(db)
	if err := svc.PutKnowledgeBaseCapabilities(t.Context(), owner, "library", []learning.CapabilityRef{{Key: "chinese_definition", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PutPreset(t.Context(), owner, learning.PresetInput{ScopeType: "document", ScopeID: "doc", CapabilityKey: "chinese_definition", Key: "求索", Value: map[string]any{"meaning_in_context": "探索追求"}, Origin: "user"}); err != nil {
		t.Fatal(err)
	}
	task, err := svc.CreatePreanalysisTask(t.Context(), owner, learning.PreanalysisRequest{DatasetID: "library", DocumentID: "doc", CapabilityKeys: []string{"chinese_definition"}, Items: []learning.PreanalysisItem{{Text: "求索", Language: "zh-Hans", SubjectKind: "word"}}})
	if err != nil {
		t.Fatal(err)
	}
	active := func() int64 {
		t.Helper()
		w := updateControlRequest(t, router, "status", 0)
		var result struct{ Active *int64 }
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Active == nil {
			t.Fatalf("update status unavailable: %d %s", w.Code, w.Body.String())
		}
		return *result.Active
	}
	if n := active(); n != 0 {
		t.Errorf("queued preanalysis that the user has not run counted as active: %d", n)
	}
	// Both barriers precede opening a SQLite transaction, so the status request
	// can observe the previously committed state while each transition is held.
	claiming, allowClaim := make(chan struct{}), make(chan struct{})
	releaseClaim := sync.OnceFunc(func() { close(allowClaim) })
	defer releaseClaim()
	persisting, finish := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(finish) })
	defer release()
	if err := db.Callback().Update().Before("gorm:begin_transaction").Register("fixture:hold-learning-completion", func(tx *gorm.DB) {
		values, ok := tx.Statement.Dest.(map[string]any)
		if !ok || tx.Statement.Table != task.TableName() {
			return
		}
		switch values["status"] {
		case "running":
			close(claiming)
			<-allowClaim
		case "completed":
			close(persisting)
			<-finish
		}
	}); err != nil {
		t.Fatal(err)
	}
	router.HandleFunc("/learning/preanalysis/tasks/{task_id}:run", learning.RunPreanalysisTask).Methods(http.MethodPost)
	request := httptest.NewRequest(http.MethodPost, "/learning/preanalysis/tasks/"+task.ID+":run", nil)
	request.Header.Set("X-User-Id", owner)
	reply := httptest.NewRecorder()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		router.ServeHTTP(reply, request)
	}()
	select {
	case <-claiming:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not reach the claim")
	}
	// A synchronous claim can keep the HTTP request open. An asynchronous
	// claim must retain its own admission after the HTTP request has returned.
	select {
	case <-requestDone:
	case <-time.After(100 * time.Millisecond):
	}
	if n := active(); n == 0 {
		t.Error("accepted preanalysis was idle before its claim committed")
	}
	releaseClaim()
	select {
	case <-persisting:
	case <-time.After(5 * time.Second):
		t.Fatal("accepted preanalysis did not reach its final write")
	}
	persisted, err := svc.GetPreanalysisTask(t.Context(), owner, task.ID)
	if err != nil || persisted.Status != "running" {
		t.Fatalf("preanalysis was not executing: %s, %v", persisted.Status, err)
	}
	if n := active(); n == 0 {
		t.Error("running preanalysis was idle before its final write")
	}
	release()
	select {
	case <-requestDone:
	case <-time.After(5 * time.Second):
		t.Fatal("run request did not return")
	}
	if reply.Code != http.StatusOK {
		t.Fatalf("run returned %d: %s", reply.Code, reply.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		persisted, err = svc.GetPreanalysisTask(t.Context(), owner, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if persisted.Status == "completed" && active() == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("preanalysis completion was lost: %s", persisted.Status)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestUpdatePreservesAdmittedScheduleAcrossSelfRequest(t *testing.T) {
	t.Setenv("TEST_DB_DRIVER", "sqlite")
	db := orm.MigrateAllModelsForTest(t).DB
	prevDB, prevLazy, prevState := store.DB(), store.LazyLLMDB(), store.State()
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(prevDB, prevLazy, prevState) })
	schedule := orm.UserSchedule{ID: "fixture-schedule", UserID: "fixture-owner", Name: "Report", CronExpr: "0 * * * *", Timezone: "UTC", PromptTemplate: "Describe today's results", Enabled: true}
	if err := scheduler.CreateSchedule(t.Context(), db, &schedule); err != nil {
		t.Fatal(err)
	}
	c := &desktopUpdateControl{token: "fixture-token", count: func(ctx context.Context) (int64, error) {
		return desktopUpdateTaskCount(ctx, db, nil, false)
	}, participants: []desktopUpdateParticipant{{scheduler.PauseForUpdate, scheduler.ResumeFromUpdate}}}
	r := mux.NewRouter()
	r.Use(c.wrap)
	r.HandleFunc("/internal/desktop-update", c.control)
	r.HandleFunc("/schedules/{id}:run-now", scheduler.RunNowHandler)
	probeEntered := false
	r.HandleFunc("/conversations:chat", func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Conversation string `json:"conversation_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if payload.Conversation == "new-root" {
			probeEntered = true
			return
		}
		if err := db.Create(&orm.ChatHistory{ID: "fixture-history", Seq: 1, ConversationID: payload.Conversation, Result: "finished report"}).Error; err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})
	arrived, forward, delivered := make(chan struct{}), make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(forward) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		close(arrived)
		<-forward
		r.ServeHTTP(w, req)
		close(delivered)
	}))
	defer server.Close()
	defer release()
	t.Setenv("LAZYMIND_CORE_SELF_URL", server.URL)
	assertUpdateHostWork(t, r, "status", 0, 0, false)
	req := httptest.NewRequest(http.MethodPost, "/schedules/"+schedule.ID+":run-now", nil)
	req.Header.Set("X-User-Id", schedule.UserID)
	reply := httptest.NewRecorder()
	r.ServeHTTP(reply, req)
	var accepted struct {
		ID string `json:"task_id"`
	}
	if reply.Code != 200 || json.Unmarshal(reply.Body.Bytes(), &accepted) != nil || accepted.ID == "" {
		t.Fatalf("manual run was not admitted: %d %s", reply.Code, reply.Body.String())
	}
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not send its continuation")
	}
	epoch := int(time.Now().UnixMilli())
	defer updateControlRequest(t, r, "resume", epoch)
	w := updateControlRequest(t, r, "pause", epoch)
	var status struct {
		Active int
		Paused bool
	}
	if w.Code == http.StatusOK {
		if json.Unmarshal(w.Body.Bytes(), &status) != nil || status.Active == 0 {
			t.Fatalf("pause failed to account for accepted task: %s", w.Body.String())
		}
	} else if w.Code != http.StatusConflict && w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected pause result: %d %s", w.Code, w.Body.String())
	}
	assertNewChatBlocked := func() {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/conversations:chat", strings.NewReader(`{"conversation_id":"new-root","query":"new work"}`))
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("X-User-Id", schedule.UserID)
		res := httptest.NewRecorder()
		r.ServeHTTP(res, req)
		if res.Code != http.StatusServiceUnavailable || probeEntered {
			t.Errorf("paused Core admitted a new loopback chat: status=%d handlerCalled=%v", res.Code, probeEntered)
		}
	}
	if status.Paused {
		assertNewChatBlocked()
	}
	release()
	// The controller resumes when its pause observes work. Continuations may
	// complete during the pause or wait for that resumption.
	select {
	case <-delivered:
	case <-time.After(100 * time.Millisecond):
	}
	if w := updateControlRequest(t, r, "resume", epoch); w.Code != http.StatusOK {
		t.Fatalf("resume after busy pause failed: %d %s", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var task orm.TaskCenterTask
		if err := db.First(&task, "id = ?", accepted.ID).Error; err != nil {
			t.Fatal(err)
		}
		if task.Status == "succeeded" {
			break
		}
		if task.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf("update interrupted the admitted task: status=%s", task.Status)
		}
		time.Sleep(time.Millisecond)
	}
	epoch++
	defer updateControlRequest(t, r, "resume", epoch)
	for {
		w := updateControlRequest(t, r, "pause", epoch)
		if w.Code == http.StatusOK && json.Unmarshal(w.Body.Bytes(), &status) == nil && status.Paused && status.Active == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("idle runtime did not pause: %d %s", w.Code, w.Body.String())
		}
		time.Sleep(time.Millisecond)
	}
	assertNewChatBlocked()
}
