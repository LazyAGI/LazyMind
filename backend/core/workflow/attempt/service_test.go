package attempt

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"lazymind/core/common/orm"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func testService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.WorkflowSessionStep{}, &orm.WorkflowOutbox{}, &orm.WorkflowEvent{}); err != nil {
		t.Fatal(err)
	}
	service := New(db, Config{LeaseDuration: time.Minute})
	return service, db
}

func queue(t *testing.T, service *Service, id, session, step string) {
	t.Helper()
	if _, err := service.Queue(context.Background(), QueueRequest{AttemptID: id,
		SessionID: session, StepID: step, Payload: json.RawMessage(`{"kind":"execute"}`)}); err != nil {
		t.Fatal(err)
	}
}

func TestQueueIsAtomicAndOutboxIsolated(t *testing.T) {
	service, db := testService(t)
	queue(t, service, "a1", "s1", "step1")
	var attempt orm.WorkflowSessionStep
	if err := db.First(&attempt, "id = ?", "a1").Error; err != nil || attempt.Status != "queued" {
		t.Fatalf("attempt=%#v err=%v", attempt, err)
	}
	var outbox orm.WorkflowOutbox
	if err := db.First(&outbox, "attempt_id = ?", "a1").Error; err != nil || outbox.Status != "pending" {
		t.Fatalf("outbox=%#v err=%v", outbox, err)
	}
	if db.Migrator().HasTable("plugin_run_outbox") { // workflow-naming: persistence
		t.Fatal("new protocol must not create the legacy worker outbox")
	}
}

func TestSchemaCapabilityBlocksOldBinarySchema(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE plugin_session_steps (
		id TEXT PRIMARY KEY, session_id TEXT NOT NULL, step_id TEXT NOT NULL,
		attempt INTEGER NOT NULL, task_id TEXT NOT NULL, status TEXT NOT NULL,
		validity TEXT NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	service := New(db, Config{})
	if _, err := service.Queue(context.Background(), QueueRequest{SessionID: "s", StepID: "x"}); !errors.Is(err, ErrSchemaUnavailable) {
		t.Fatalf("queue error = %v", err)
	}
}

func TestExpandedSchemaKeepsLegacyAttemptWriteCompatible(t *testing.T) {
	service, db := testService(t)
	if !SchemaCapable(db) {
		t.Fatal("expanded schema capability missing")
	}
	now := time.Now().UTC()
	// This is the column set written by the pre-expand binary. New columns must
	// receive harmless database defaults and must not be required by old code.
	if err := db.Exec(`INSERT INTO plugin_session_steps
		(id, session_id, step_id, attempt, task_id, status, validity, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, "legacy", "s1", "step", 1, "task", "pending", "effective", now, now).Error; err != nil {
		t.Fatal(err)
	}
	row, err := service.Attempt(context.Background(), "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if row.FencingGeneration != 0 || row.LeaseToken != "" || string(row.ProgressJSON) != `{}` {
		t.Fatalf("legacy defaults = %#v", row)
	}
}

func TestFakeExecutorSerialAndParallel(t *testing.T) {
	service, _ := testService(t)
	queue(t, service, "serial", "s1", "one")
	executor := FakeExecutor{Service: service, ExecutorID: "fake-1"}
	if _, err := executor.RunOne(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	row, _ := service.Attempt(context.Background(), "serial")
	if row.Status != "succeeded" {
		t.Fatal(row.Status)
	}
	queue(t, service, "parallel-a", "s2", "a")
	queue(t, service, "parallel-b", "s2", "b")
	for range 2 {
		if _, err := executor.RunOne(context.Background(), false); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"parallel-a", "parallel-b"} {
		row, _ = service.Attempt(context.Background(), id)
		if row.Status != "succeeded" {
			t.Fatalf("%s status=%s", id, row.Status)
		}
	}
}

func TestExpiredLeaseReclaimFencesOldExecutor(t *testing.T) {
	service, _ := testService(t)
	now := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	queue(t, service, "a1", "s1", "step")
	first, err := service.Claim(context.Background(), "crashed")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	second, err := service.Claim(context.Background(), "replacement")
	if err != nil {
		t.Fatal(err)
	}
	if second.FencingGeneration <= first.FencingGeneration || second.LeaseToken == first.LeaseToken {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
	if err := service.Progress(context.Background(), "a1", first.LeaseToken, json.RawMessage(`{}`)); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale progress error=%v", err)
	}
	if err := service.Complete(context.Background(), "a1", second.LeaseToken, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
}

func TestHeartbeatExtendsLeaseAndTransitionsRunning(t *testing.T) {
	service, _ := testService(t)
	now := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	queue(t, service, "a1", "s1", "step")
	claim, _ := service.Claim(context.Background(), "executor")
	now = now.Add(10 * time.Second)
	expires, err := service.Heartbeat(context.Background(), "a1", claim.LeaseToken)
	if err != nil || !expires.Equal(now.Add(time.Minute)) {
		t.Fatalf("expires=%v err=%v", expires, err)
	}
	if err := service.Progress(context.Background(), "a1", claim.LeaseToken, json.RawMessage(`{"pct":10}`)); err != nil {
		t.Fatal(err)
	}
	row, _ := service.Attempt(context.Background(), "a1")
	if row.Status != "running" {
		t.Fatal(row.Status)
	}
}

func TestValidateLeaseAcceptsOnlyCurrentLiveOwner(t *testing.T) {
	service, _ := testService(t)
	now := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	queue(t, service, "a1", "s1", "step")
	claim, _ := service.Claim(context.Background(), "executor")
	if err := service.ValidateLease(context.Background(), "a1", claim.LeaseToken); err != nil {
		t.Fatalf("live lease rejected: %v", err)
	}
	if err := service.ValidateLease(context.Background(), "a1", "stale"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale lease error=%v", err)
	}
	now = now.Add(2 * time.Minute)
	if err := service.ValidateLease(context.Background(), "a1", claim.LeaseToken); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired lease error=%v", err)
	}
}

func TestClaimForHostDoesNotStealAnotherHostAttempt(t *testing.T) {
	service, db := testService(t)
	if err := db.AutoMigrate(&orm.WorkflowSession{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, session := range []orm.WorkflowSession{
		{ID: "session-codex", ConversationID: "c1", WorkflowID: "w", ControllerHost: "codex", Status: "active", CreatedAt: now, UpdatedAt: now},
		{ID: "session-lazymind", ConversationID: "c2", WorkflowID: "w", ControllerHost: "lazymind", Status: "active", CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(&session).Error; err != nil {
			t.Fatal(err)
		}
	}
	queue(t, service, "codex-attempt", "session-codex", "step")
	queue(t, service, "lazymind-attempt", "session-lazymind", "step")
	claim, err := service.ClaimForHost(context.Background(), "lazy-executor", "lazymind")
	if err != nil {
		t.Fatal(err)
	}
	if claim.AttemptID != "lazymind-attempt" {
		t.Fatalf("claimed=%s", claim.AttemptID)
	}
	codex, _ := service.Attempt(context.Background(), "codex-attempt")
	if codex.Status != "queued" {
		t.Fatalf("codex status=%s", codex.Status)
	}
}

func TestCompleteCancelFirstValidTerminalWins(t *testing.T) {
	for _, winner := range []string{"complete", "cancel"} {
		t.Run(winner, func(t *testing.T) {
			service, _ := testService(t)
			queue(t, service, "a1", "s1", "step")
			claim, _ := service.Claim(context.Background(), "executor")
			if winner == "complete" {
				if err := service.Complete(context.Background(), "a1", claim.LeaseToken, json.RawMessage(`{"ok":true}`)); err != nil {
					t.Fatal(err)
				}
				if err := service.Cancel(context.Background(), "a1", claim.LeaseToken); !errors.Is(err, ErrAlreadyTerminal) {
					t.Fatalf("cancel error=%v", err)
				}
				if err := service.Complete(context.Background(), "a1", claim.LeaseToken, json.RawMessage(`{"ok":true}`)); err != nil {
					t.Fatalf("same terminal must be idempotent: %v", err)
				}
			} else {
				if err := service.Cancel(context.Background(), "a1", claim.LeaseToken); err != nil {
					t.Fatal(err)
				}
				if err := service.Complete(context.Background(), "a1", claim.LeaseToken, json.RawMessage(`{}`)); !errors.Is(err, ErrAlreadyTerminal) {
					t.Fatalf("complete error=%v", err)
				}
			}
		})
	}
}
