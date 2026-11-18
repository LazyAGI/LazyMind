package recovery

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func TestUpdateCleanupWaitsForActiveDeletion(t *testing.T) {
	t.Setenv("TEST_DB_DRIVER", "sqlite")
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", t.TempDir())
	db := orm.MigrateAllModelsForTest(t)
	seedTrashedConversation(t, db, "blocked-cleanup", time.Now().UTC().Add(-time.Second))
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var deletes atomic.Int32
	if err := db.Callback().Delete().Before("gorm:delete").Register("unit:hold-cleanup-delete", func(tx *gorm.DB) {
		if tx.Statement.Table == "conversations" {
			deletes.Add(1)
			enteredOnce.Do(func() { close(entered) })
			<-release
		}
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	interval := 10 * time.Millisecond
	done := Start(ctx, db.DB, interval)
	if done == nil {
		t.Fatal("cleanup has no completion signal")
	}
	defer func() {
		cancel()
		unblock()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("cleanup fixture did not finish")
		}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("cleanup did not reach real deletion")
	}
	cancel()
	select {
	case <-done:
		t.Fatal("cleanup reported completion while deletion was blocked")
	case <-time.After(30 * time.Millisecond):
	}
	unblock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cleanup did not complete after cancellation and release")
	}
	before := deletes.Load()
	seedTrashedConversation(t, db, "after-stop", time.Now().UTC().Add(-time.Second))
	<-time.After(3 * interval)
	if deletes.Load() != before {
		t.Fatal("new cleanup deletion started after completion")
	}
	waitForConversationCount(t, db, "after-stop", 1)
}

func TestUpdateCleanupCanceledBeforeStartCompletesWithoutDeletion(t *testing.T) {
	t.Setenv("TEST_DB_DRIVER", "sqlite")
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", t.TempDir())
	db := orm.MigrateAllModelsForTest(t)
	seedTrashedConversation(t, db, "preserved", time.Now().UTC().Add(-time.Second))
	var deletes atomic.Int32
	if err := db.Callback().Delete().Before("gorm:delete").Register("unit:count-cleanup-delete", func(*gorm.DB) { deletes.Add(1) }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := Start(ctx, db.DB, time.Millisecond)
	if done == nil {
		t.Fatal("cleanup has no completion signal")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("already canceled cleanup did not complete")
	}
	if deletes.Load() != 0 {
		t.Fatal("already canceled cleanup attempted deletion")
	}
	waitForConversationCount(t, db, "preserved", 1)
}
