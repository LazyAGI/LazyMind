package main

import (
	"context"
	"encoding/json"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDesktopUpdateRegistersBackgroundRunnerOwners(t *testing.T) {
	data, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"runner =", "paperChatRunner :=", "directoryRunner :="} {
		if !strings.Contains(string(data), name+" updateControl.startRunner(") {
			t.Errorf("%s must start through the update-aware owner registration", name)
		}
	}
}

func TestDesktopUpdateRunnerOwnerPauseFinalWriteAndResume(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "runner.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	if err := db.AutoMigrate(&orm.AsyncJob{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	handlerEntered, handlerRelease := make(chan struct{}), make(chan struct{})
	finalEntered, finalRelease := make(chan struct{}), make(chan struct{})
	var handlerOnce, finalOnce, finalObserved sync.Once
	release := func() { handlerOnce.Do(func() { close(handlerRelease) }); finalOnce.Do(func() { close(finalRelease) }) }
	const kind = "fixture.paper-update-owner"
	asyncjob.Register(kind, func(ctx context.Context, job asyncjob.Job, _ asyncjob.Reporter) (asyncjob.Result, error) {
		if job.ResourceID == "active" {
			close(handlerEntered)
			select {
			case <-handlerRelease:
			case <-ctx.Done():
			}
		}
		return asyncjob.Result{}, nil
	})
	enqueue := func(id string) *orm.AsyncJob {
		row, err := asyncjob.Enqueue(ctx, db, asyncjob.EnqueueRequest{JobType: kind, ResourceType: "fixture", ResourceID: id, MaxAttempts: 2})
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	active := enqueue("active")
	if err := db.Callback().Update().Before("gorm:update").Register("fixture:final-write", func(tx *gorm.DB) {
		if values, ok := tx.Statement.Dest.(map[string]any); ok && values["status"] == string(asyncjob.StatusSucceeded) {
			finalObserved.Do(func() { close(finalEntered) })
			<-finalRelease
		}
	}); err != nil {
		t.Fatal(err)
	}
	c := &desktopUpdateControl{token: "fixture-token", count: func(context.Context) (int64, error) { return 0, nil }}
	runner := c.startRunner(ctx, db, asyncjob.Options{JobTypes: []string{kind}, Concurrency: 1, PollInterval: 5 * time.Millisecond, LockTTL: 40 * time.Millisecond})
	defer func() {
		cancel()
		release()
		select {
		case <-runner.Done():
		case <-time.After(3 * time.Second):
			t.Error("runner did not stop")
		}
	}()
	wait := func(ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(3 * time.Second):
			t.Fatal("fixture did not reach boundary")
		}
	}
	wait(handlerEntered)
	handler := c.wrap(http.NotFoundHandler())
	paused := func() int64 {
		t.Helper()
		w := updateControlRequest(t, handler, "pause", 7)
		var s struct {
			Active int64
			Paused bool
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &s) != nil || !s.Paused {
			t.Fatalf("pause: %s", w.Body.String())
		}
		return s.Active
	}
	if paused() == 0 {
		t.Fatal("admitted paper work was not counted")
	}
	queued := enqueue("queued")
	stale := enqueue("stale")
	expired := time.Now().Add(-time.Hour)
	if err := db.Model(&orm.AsyncJob{}).Where("id = ?", stale.ID).Updates(map[string]any{"status": "running", "lock_until": expired, "attempt_count": 1}).Error; err != nil {
		t.Fatal(err)
	}
	handlerOnce.Do(func() { close(handlerRelease) })
	wait(finalEntered)
	if paused() == 0 {
		t.Fatal("final result write was not counted")
	}
	finalOnce.Do(func() { close(finalRelease) })
	deadline := time.Now().Add(3 * time.Second)
	for paused() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("completed work did not drain")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(80 * time.Millisecond)
	for id, want := range map[string]string{active.ID: "succeeded", queued.ID: "pending", stale.ID: "running"} {
		var row orm.AsyncJob
		if err := db.First(&row, "id = ?", id).Error; err != nil || row.Status != want {
			t.Fatalf("paused row %s status=%s err=%v", id, row.Status, err)
		}
	}
	if updateControlRequest(t, handler, "resume", 7).Code != 200 {
		t.Fatal("resume failed")
	}
	deadline = time.Now().Add(3 * time.Second)
	for {
		var rows []orm.AsyncJob
		db.Where("id IN ?", []string{queued.ID, stale.ID}).Find(&rows)
		if len(rows) == 2 && rows[0].Status == "succeeded" && rows[1].Status == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("resume did not restore claims and lease recovery")
		}
		time.Sleep(time.Millisecond)
	}
}
