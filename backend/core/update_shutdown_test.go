package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/scheduler"
	"lazymind/core/store"
)

type updateShutdownTransport struct{ host string }

func (transport updateShutdownTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host != transport.host {
		return nil, errors.New("request escaped shutdown fixture")
	}
	return http.DefaultTransport.RoundTrip(request)
}

func TestUpdateShutdownBoundsAllWaitersAfterLongUptime(t *testing.T) {
	for _, stage := range []string{"execution", "final-write"} {
		t.Run(stage, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, exe, "-test.run=^TestUpdateShutdownChild$", "-test.v")
			cmd.Env = append(os.Environ(), "LAZYMIND_UNIT_SHUTDOWN_STAGE="+stage)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("shutdown fixture: %v\n%s", err, out)
			}
		})
	}
}

func TestUpdateShutdownChild(t *testing.T) {
	stage := os.Getenv("LAZYMIND_UNIT_SHUTDOWN_STAGE")
	if stage == "" {
		return
	}
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, db.DB, nil)
	limit := 150 * time.Millisecond
	entered, releaseHTTP := make(chan struct{}), make(chan struct{})
	finalizing, releaseWrite := make(chan struct{}), make(chan struct{})
	var httpOnce, writeOnce, finalOnce sync.Once
	unblockHTTP := func() { httpOnce.Do(func() { close(releaseHTTP) }) }
	unblockWrite := func() { writeOnce.Do(func() { close(releaseWrite) }) }
	if err := db.Callback().Create().Before("gorm:create").Register("unit:hold-shutdown-write", func(tx *gorm.DB) {
		if stage == "final-write" && tx.Statement.Table == "task_run_outputs" {
			finalOnce.Do(func() { close(finalizing) })
			<-releaseWrite
		}
	}); err != nil {
		t.Fatal(err)
	}
	ln := newListener(t)
	t.Setenv("LAZYMIND_CORE_SELF_URL", "http://"+ln.Addr().String())
	// The child runs only this test; no failure path can reach a real service.
	http.DefaultClient = &http.Client{Transport: updateShutdownTransport{host: ln.Addr().String()}}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations:chat" {
			w.WriteHeader(http.StatusOK)
			return
		}
		var payload struct {
			ConversationID string `json:"conversation_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		close(entered)
		<-releaseHTTP
		if err := db.Create(&orm.ChatHistory{ID: "shutdown-history", Seq: 1, ConversationID: payload.ConversationID, Result: "fixture result"}).Error; err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})}
	ctx, cancel := context.WithCancel(context.Background())
	runtimeCtx, cancelRuntime := context.WithCancel(ctx)
	schedulerDone := scheduler.RunScheduler(runtimeCtx, db.DB, "")
	otherWriter := make(chan struct{})
	result, coordinatorFinished := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(coordinatorFinished)
		result <- coordinateShutdown(ctx, server, ln, []<-chan struct{}{otherWriter, schedulerDone}, limit, cancelRuntime, nil)
	}()
	defer func() {
		cancel()
		unblockHTTP()
		unblockWrite()
		close(otherWriter)
		_ = server.Close()
		select {
		case <-schedulerDone:
		case <-time.After(3 * time.Second):
			t.Error("fixture scheduler failed to finish")
		}
		select {
		case <-coordinatorFinished:
		case <-time.After(3 * time.Second):
			t.Error("fixture coordinator failed to finish")
		}
	}()
	schedule := orm.UserSchedule{ID: "shutdown-schedule", UserID: "owner", Name: "shutdown fixture", CronExpr: "0 * * * *", Timezone: "UTC", PromptTemplate: "fixture", Enabled: true, NextRunAt: time.Now().Add(-time.Minute)}
	if err := scheduler.CreateSchedule(t.Context(), db.DB, &schedule); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/schedules/"+schedule.ID+":run-now", nil)
	request.Header.Set("X-User-Id", "owner")
	reply := httptest.NewRecorder()
	scheduler.RunNowHandler(reply, request)
	if reply.Code != http.StatusOK {
		t.Fatalf("initial run failed: %d", reply.Code)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduled execution not entered")
	}
	if stage == "final-write" {
		unblockHTTP()
		select {
		case <-finalizing:
		case <-time.After(3 * time.Second):
			t.Fatal("final database write not entered")
		}
	}
	// Model normal uptime longer than the shutdown budget. A timeout created
	// at server startup is already exhausted when shutdown actually begins.
	<-time.After(3 * limit)
	cancel()
	select {
	case err := <-result:
		t.Fatalf("shutdown budget was consumed during normal uptime: %v", err)
	case <-time.After(limit / 3):
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("ordinary bounded shutdown changed its result: %v", err)
		}
	case <-time.After(5 * limit):
		t.Fatal("multiple background waiters did not share a bounded shutdown deadline")
	}
	select {
	case <-schedulerDone:
		t.Fatal("ordinary shutdown timeout was mistaken for scheduler completion")
	default:
	}
	select {
	case <-scheduler.BeginDrain():
		t.Fatal("ordinary timeout was mistaken for update drain success")
	default:
	}
}
