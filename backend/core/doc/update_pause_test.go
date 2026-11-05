package doc

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/maintenancegate"
	"lazymind/core/common/orm"
)

func TestUpdateDeferredIndexPause(t *testing.T) {
	for _, phase := range []string{"readiness", "final-write"} {
		t.Run(phase, func(t *testing.T) {
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestUpdateDeferredIndexPauseChild$")
			cmd.Env = append(os.Environ(), "LAZYMIND_UNIT_DEFERRED_UPDATE="+phase)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("isolated %s: %v\n%s", phase, err, output)
			}
		})
	}
}

func TestUpdateDeferredIndexPauseChild(t *testing.T) {
	phase := os.Getenv("LAZYMIND_UNIT_DEFERRED_UPDATE")
	if phase == "" {
		return
	}
	t.Setenv("TEST_DB_DRIVER", "sqlite")
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	db := newDocumentTestDB(t)
	now := time.Now().UTC()
	dataset := orm.Dataset{ID: "update-fixture", KbID: "update-fixture", ProcessingLevel: ProcessingLevelIndexed,
		BaseModel: orm.BaseModel{CreateUserID: "fixture-user", CreatedAt: now, UpdatedAt: now}}
	document := orm.Document{ID: "update-document", DatasetID: dataset.ID}
	state := newDocumentProcessingState(dataset.ID, document.ID, now)
	state.ParseArtifactRef = "fixture-artifact"
	state.IndexStatus = "pending"
	for _, row := range []any{&dataset, &document, &state} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	previous := http.DefaultTransport
	var requests, readyCalls atomic.Int32
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		return testJSONResponse(http.StatusServiceUnavailable, `{}`), nil
	})
	defer func() { http.DefaultTransport = previous }()
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	block := func() { enterOnce.Do(func() { close(entered) }); <-release }
	if phase == "final-write" {
		if err := db.Callback().Update().Before("gorm:update").Register("unit:deferred-final-write", func(tx *gorm.DB) {
			if values, ok := tx.Statement.Dest.(map[string]any); ok && values["index_status"] == "failed" {
				block()
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	ready := func(context.Context, string) (bool, error) {
		readyCalls.Add(1)
		if phase == "readiness" {
			block()
			return false, nil
		}
		return true, nil
	}
	pause := func(epoch uint64) *maintenancegate.Ticket {
		p, err := PauseDeferredIndexingForUpdate(epoch)
		if err != nil || p == nil {
			t.Fatalf("pause: %v", err)
		}
		return p
	}
	wait := func(p *maintenancegate.Ticket) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := p.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	first := pause(1)
	wait(first)
	reconcileDeferredIndexingWithReady(ctx, ready)
	if readyCalls.Load() != 0 || requests.Load() != 0 {
		t.Fatal("paused indexing reached a producer")
	}
	var stored orm.DocumentProcessingState
	if err := db.First(&stored, "dataset_id = ? AND document_id = ?", dataset.ID, document.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.IndexStatus != "pending" || !stored.UpdatedAt.Equal(state.UpdatedAt) {
		t.Fatal("pause changed pending work")
	}
	if err := ResumeDeferredIndexingFromUpdate(1); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(first.Validate(), maintenancegate.ErrStale) {
		t.Fatal("resume retained an old receipt")
	}
	go func() { defer close(finished); reconcileDeferredIndexingWithReady(ctx, ready) }()
	defer func() {
		unblock()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Error("indexing fixture did not finish")
		}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("indexing did not reach its boundary")
	}
	second := pause(2)
	if !errors.Is(second.Validate(), maintenancegate.ErrActive) {
		t.Fatal("pause completed before admitted indexing finished")
	}
	beforeReady, beforeRequests := readyCalls.Load(), requests.Load()
	reconcileDeferredIndexingWithReady(ctx, ready)
	if readyCalls.Load() != beforeReady || requests.Load() != beforeRequests {
		t.Fatal("pause admitted a second scan")
	}
	if ctx.Err() != nil {
		t.Fatal("pause cancelled admitted work")
	}
	unblock()
	wait(second)
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("receipt preceded completion")
	}
	if err := db.First(&stored, "dataset_id = ? AND document_id = ?", dataset.ID, document.ID).Error; err != nil {
		t.Fatal(err)
	}
	want := "pending"
	if phase == "final-write" {
		want = "failed"
	}
	if stored.IndexStatus != want {
		t.Fatalf("receipt did not include final state: %s, want %s", stored.IndexStatus, want)
	}
	if err := ResumeDeferredIndexingFromUpdate(2); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.DocumentProcessingState{}).Where("dataset_id = ? AND document_id = ?", dataset.ID, document.ID).
		Update("index_status", "pending").Error; err != nil {
		t.Fatal(err)
	}
	for index, disabled := range []string{"rag", "user-preference"} {
		value := "false"
		if disabled == "user-preference" {
			value = "true"
			if err := db.AutoMigrate(&orm.UserUIPreferences{}); err != nil {
				t.Fatal(err)
			}
			preferences := orm.UserUIPreferences{UserID: dataset.CreateUserID}
			if err := db.Create(&preferences).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&preferences).Update("document_parsing_enabled", false).Error; err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("LAZYMIND_RAG_ENABLED", value)
		epoch := uint64(index + 3)
		paused := pause(epoch)
		wait(paused)
		if err := ResumeDeferredIndexingFromUpdate(epoch); err != nil {
			t.Fatal(err)
		}
		if os.Getenv("LAZYMIND_RAG_ENABLED") != value {
			t.Fatal("maintenance changed the RAG setting")
		}
		if disabled == "user-preference" {
			var preferences orm.UserUIPreferences
			if err := db.First(&preferences, "user_id = ?", dataset.CreateUserID).Error; err != nil {
				t.Fatal(err)
			}
			if preferences.DocumentParsingEnabled {
				t.Fatal("maintenance enabled user-disabled parsing")
			}
		}
		reconcileDeferredIndexingWithReady(ctx, ready)
		if readyCalls.Load() != beforeReady || requests.Load() != beforeRequests {
			t.Fatalf("resume admitted indexing with %s disabled", disabled)
		}
	}
}
