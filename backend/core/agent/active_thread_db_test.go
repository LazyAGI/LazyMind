package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

// newAgentDB is a thin helper reused from agent_test.go style — creates SQLite in-memory DB.
// We reuse newAgentTestDB defined in agent_test.go to avoid duplication.

// TestCreateUserActiveThreadReservation inserts a "creating" placeholder row.
func TestCreateUserActiveThreadReservation(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()

	guard, created, err := createUserActiveThreadReservation(db.DB, "u1", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !created {
		t.Fatal("expected created=true for first reservation")
	}
	if guard == nil || guard.userID != "u1" || guard.createToken == "" {
		t.Fatalf("unexpected guard: %+v", guard)
	}
	defer guard.Abort(db.DB)

	// Verify DB row exists.
	var row orm.AgentUserActiveThread
	if err := db.DB.Where("user_id = ?", "u1").First(&row).Error; err != nil {
		t.Fatalf("load row: %v", err)
	}
	if row.Status != userActiveThreadStatusCreating {
		t.Fatalf("status: got %q, want %q", row.Status, userActiveThreadStatusCreating)
	}
	if row.CreateToken != guard.createToken {
		t.Fatal("create_token mismatch")
	}
}

// TestCreateUserActiveThreadReservation_Duplicate returns created=false due to DoNothing.
func TestCreateUserActiveThreadReservation_Duplicate(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()

	// First reservation succeeds.
	guard1, created1, _ := createUserActiveThreadReservation(db.DB, "u1", now)
	if !created1 {
		t.Fatal("first reservation should succeed")
	}
	defer guard1.Abort(db.DB)

	// Second reservation for same user returns created=false.
	_, created2, err := createUserActiveThreadReservation(db.DB, "u1", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created2 {
		t.Fatal("duplicate should return created=false")
	}
}

// TestCreateUserActiveThreadActivation inserts an "active" row for a user.
func TestCreateUserActiveThreadActivation(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()

	created, err := createUserActiveThreadActivation(db.DB, "u1", "thread-1", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !created {
		t.Fatal("expected created=true")
	}

	var row orm.AgentUserActiveThread
	if err := db.DB.Where("user_id = ?", "u1").First(&row).Error; err != nil {
		t.Fatalf("load row: %v", err)
	}
	if row.Status != userActiveThreadStatusActive || row.ThreadID != "thread-1" {
		t.Fatalf("unexpected row: status=%q thread_id=%q", row.Status, row.ThreadID)
	}
}

// TestCreateUserActiveThreadActivation_Duplicate returns false due to DoNothing conflict.
func TestCreateUserActiveThreadActivation_Duplicate(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()

	createUserActiveThreadActivation(db.DB, "u1", "thread-1", now)
	created, err := createUserActiveThreadActivation(db.DB, "u1", "thread-2", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created {
		t.Fatal("duplicate activation should return false")
	}
}

func TestReconcileThreadFlowStatusPersistsTerminalAndReleasesActive(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentThread{
		ThreadID: "thread-1", CurrentTaskID: "repair", Status: "running",
		CreateUserID: "u1", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&orm.AgentUserActiveThread{
		UserID: "u1", ThreadID: "thread-1", Status: userActiveThreadStatusActive,
		LeaseUntil: now, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := reconcileThreadFlowStatus(db.DB, "thread-1", &threadFlowStatusResponse{
		Status: "failed", CurrentStep: "repair",
	}); err != nil {
		t.Fatal(err)
	}
	var thread orm.AgentThread
	if err := db.DB.First(&thread, "thread_id = ?", "thread-1").Error; err != nil {
		t.Fatal(err)
	}
	if thread.Status != "failed" || thread.CurrentTaskID != "" {
		t.Fatalf("unexpected reconciled thread: %#v", thread)
	}
	var active orm.AgentUserActiveThread
	if err := db.DB.First(&active, "user_id = ?", "u1").Error; err != nil {
		t.Fatal(err)
	}
	if active.Status != userActiveThreadStatusFinished {
		t.Fatalf("terminal flow must finish active reservation: %#v", active)
	}
}

func TestReconcileThreadFlowStatusKeepsNonTerminalActive(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentThread{
		ThreadID: "thread-1", Status: "created", CreateUserID: "u1",
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&orm.AgentUserActiveThread{
		UserID: "u1", ThreadID: "thread-1", Status: userActiveThreadStatusActive,
		LeaseUntil: now, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := reconcileThreadFlowStatus(db.DB, "thread-1", &threadFlowStatusResponse{
		Status: "running", CurrentStep: "eval",
	}); err != nil {
		t.Fatal(err)
	}
	var thread orm.AgentThread
	if err := db.DB.First(&thread, "thread_id = ?", "thread-1").Error; err != nil {
		t.Fatal(err)
	}
	if thread.Status != "running" || thread.CurrentTaskID != "eval" {
		t.Fatalf("unexpected reconciled thread: %#v", thread)
	}
	var active orm.AgentUserActiveThread
	if err := db.DB.First(&active, "user_id = ?", "u1").Error; err != nil {
		t.Fatal(err)
	}
	if active.Status != userActiveThreadStatusActive {
		t.Fatalf("non-terminal flow must stay active: %#v", active)
	}
}

// TestActivateUserThread upserts an active row, replacing any existing status.
func TestActivateUserThread(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()

	// First call inserts.
	if err := activateUserThread(db.DB, "u1", "thread-1", now); err != nil {
		t.Fatalf("first activate: %v", err)
	}
	var row orm.AgentUserActiveThread
	db.DB.Where("user_id = ?", "u1").First(&row)
	if row.ThreadID != "thread-1" || row.Status != userActiveThreadStatusActive {
		t.Fatalf("after first activate: %+v", row)
	}

	// Second call updates the existing row.
	if err := activateUserThread(db.DB, "u1", "thread-2", now); err != nil {
		t.Fatalf("second activate: %v", err)
	}
	db.DB.Where("user_id = ?", "u1").First(&row)
	if row.ThreadID != "thread-2" || row.Status != userActiveThreadStatusActive {
		t.Fatalf("after second activate: thread_id=%q status=%q", row.ThreadID, row.Status)
	}
}

// TestDeleteExpiredCreatingActiveThread removes expired "creating" rows.
func TestDeleteExpiredCreatingActiveThread(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()

	// Insert an expired creating row.
	db.DB.Create(&orm.AgentUserActiveThread{
		UserID:     "u1",
		Status:     userActiveThreadStatusCreating,
		LeaseUntil: now.Add(-1 * time.Minute), // expired
		CreatedAt:  now,
		UpdatedAt:  now,
	})

	if err := deleteExpiredCreatingActiveThread(db.DB, "u1", now); err != nil {
		t.Fatalf("delete expired: %v", err)
	}

	var count int64
	db.DB.Model(&orm.AgentUserActiveThread{}).Where("user_id = ?", "u1").Count(&count)
	if count != 0 {
		t.Fatalf("expected 0 rows after delete, got %d", count)
	}
}

// TestDeleteExpiredCreatingActiveThread_KeepsActiveLease does not delete rows with future lease.
func TestDeleteExpiredCreatingActiveThread_KeepsActiveLease(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()

	db.DB.Create(&orm.AgentUserActiveThread{
		UserID:     "u1",
		Status:     userActiveThreadStatusCreating,
		LeaseUntil: now.Add(5 * time.Minute), // still valid
		CreatedAt:  now,
		UpdatedAt:  now,
	})

	if err := deleteExpiredCreatingActiveThread(db.DB, "u1", now); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var count int64
	db.DB.Model(&orm.AgentUserActiveThread{}).Where("user_id = ?", "u1").Count(&count)
	if count != 1 {
		t.Fatalf("expected 1 row, got %d", count)
	}
}

// TestDeleteUserActiveThread removes rows for a user/thread pair.
func TestDeleteUserActiveThread(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()

	db.DB.Create(&orm.AgentUserActiveThread{
		UserID: "u1", ThreadID: "t1", Status: userActiveThreadStatusActive,
		LeaseUntil: now, CreatedAt: now, UpdatedAt: now,
	})

	if err := deleteUserActiveThread(db.DB, "u1", "t1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	var count int64
	db.DB.Model(&orm.AgentUserActiveThread{}).Where("user_id = ?", "u1").Count(&count)
	if count != 0 {
		t.Fatalf("expected 0 rows, got %d", count)
	}
}

// TestDeleteUserActiveThread_EmptyThreadID deletes all rows for user.
func TestDeleteUserActiveThread_EmptyThreadID(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()

	db.DB.Create(&orm.AgentUserActiveThread{
		UserID: "u1", ThreadID: "t1", Status: userActiveThreadStatusActive,
		LeaseUntil: now, CreatedAt: now, UpdatedAt: now,
	})
	db.DB.Create(&orm.AgentUserActiveThread{
		UserID: "u2", ThreadID: "t2", Status: userActiveThreadStatusActive,
		LeaseUntil: now, CreatedAt: now, UpdatedAt: now,
	})

	if err := deleteUserActiveThread(db.DB, "u1", ""); err != nil {
		t.Fatalf("delete: %v", err)
	}

	var count int64
	db.DB.Model(&orm.AgentUserActiveThread{}).Where("user_id = ?", "u1").Count(&count)
	if count != 0 {
		t.Fatalf("u1 should have 0 rows, got %d", count)
	}
	db.DB.Model(&orm.AgentUserActiveThread{}).Where("user_id = ?", "u2").Count(&count)
	if count != 1 {
		t.Fatalf("u2 should still have 1 row, got %d", count)
	}
}

// TestGuardCommit updates the reservation from creating to active.
func TestGuardCommit(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()

	guard, created, _ := createUserActiveThreadReservation(db.DB, "u1", now)
	if !created {
		t.Fatal("reservation failed")
	}

	if err := guard.Commit(db.DB, "thread-final"); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var row orm.AgentUserActiveThread
	db.DB.Where("user_id = ?", "u1").First(&row)
	if row.Status != userActiveThreadStatusActive || row.ThreadID != "thread-final" || row.CreateToken != "" {
		t.Fatalf("after commit: %+v", row)
	}
}

// TestGuardCommit_DoubleCommit is idempotent (second call is no-op).
func TestGuardCommit_DoubleCommit(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()

	guard, created, _ := createUserActiveThreadReservation(db.DB, "u1", now)
	if !created {
		t.Fatal("reservation failed")
	}
	guard.Commit(db.DB, "thread-1")

	// Second commit is no-op.
	if err := guard.Commit(db.DB, "thread-2"); err != nil {
		t.Fatalf("double commit should be no-op: %v", err)
	}
}

// TestGuardCommit_EmptyThreadID returns error.
func TestGuardCommit_EmptyThreadID(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()

	guard, created, _ := createUserActiveThreadReservation(db.DB, "u1", now)
	if !created {
		t.Fatal("reservation failed")
	}
	defer guard.Abort(db.DB)

	if err := guard.Commit(db.DB, ""); err == nil {
		t.Fatal("expected error for empty thread_id")
	}
}

// TestGuardAbort removes the creating reservation row.
func TestGuardAbort(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()

	guard, created, _ := createUserActiveThreadReservation(db.DB, "u1", now)
	if !created {
		t.Fatal("reservation failed")
	}
	guard.Abort(db.DB)

	var count int64
	db.DB.Model(&orm.AgentUserActiveThread{}).Where("user_id = ?", "u1").Count(&count)
	if count != 0 {
		t.Fatalf("expected 0 rows after abort, got %d", count)
	}
}

// TestGuardAbort_AfterCommit is no-op.
func TestGuardAbort_AfterCommit(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()

	guard, created, _ := createUserActiveThreadReservation(db.DB, "u1", now)
	if !created {
		t.Fatal("reservation failed")
	}
	guard.Commit(db.DB, "thread-1")
	guard.Abort(db.DB) // should not delete the committed row

	var row orm.AgentUserActiveThread
	if err := db.DB.Where("user_id = ?", "u1").First(&row).Error; err != nil {
		t.Fatalf("committed row should still exist: %v", err)
	}
}

// TestMarkUserActiveThreadFinished sets status to finished.
func TestMarkUserActiveThreadFinished(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()

	db.DB.Create(&orm.AgentUserActiveThread{
		UserID: "u1", ThreadID: "t1", Status: userActiveThreadStatusActive,
		LeaseUntil: now, CreatedAt: now, UpdatedAt: now,
	})

	if err := markUserActiveThreadFinished(db.DB, "t1"); err != nil {
		t.Fatalf("mark finished: %v", err)
	}

	var row orm.AgentUserActiveThread
	db.DB.Where("thread_id = ?", "t1").First(&row)
	if row.Status != userActiveThreadStatusFinished {
		t.Fatalf("status: got %q, want %q", row.Status, userActiveThreadStatusFinished)
	}
}

// TestMarkUserActiveThreadFinished_NilDB returns nil (no-op).
func TestMarkUserActiveThreadFinished_NilDB(t *testing.T) {
	if err := markUserActiveThreadFinished(nil, "t1"); err != nil {
		t.Fatalf("nil db: got error %v, want nil", err)
	}
}

// TestMarkUserActiveThreadFinished_EmptyThreadID returns nil (no-op).
func TestMarkUserActiveThreadFinished_EmptyThreadID(t *testing.T) {
	db := newAgentTestDB(t)
	if err := markUserActiveThreadFinished(db.DB, ""); err != nil {
		t.Fatalf("empty thread_id: got error %v, want nil", err)
	}
}

// TestReserveUserActiveThreadCreation_MissingUserID returns 400 error.
func TestReserveUserActiveThreadCreation_MissingUserID(t *testing.T) {
	db := newAgentTestDB(t)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	// No X-User-Id header.

	_, err := reserveUserActiveThreadCreation(context.Background(), db.DB, req)
	if err == nil {
		t.Fatal("expected error for missing user ID")
	}
	activeErr, ok := err.(*userActiveThreadError)
	if !ok || activeErr.statusCode != http.StatusBadRequest {
		t.Fatalf("expected userActiveThreadError with 400, got %v", err)
	}
}

// TestReserveUserActiveThreadCreation_PreExistingRecord creates and commits reservation.
func TestReserveUserActiveThreadCreation_PreExistingRecord(t *testing.T) {
	db := newAgentTestDB(t)

	// Pre-populate with a finished thread.
	now := time.Now().UTC()
	db.DB.Create(&orm.AgentUserActiveThread{
		UserID: "u1", ThreadID: "t0", Status: userActiveThreadStatusFinished,
		LeaseUntil: now, CreatedAt: now, UpdatedAt: now,
	})

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	req.Header.Set("X-User-Id", "u1")

	guard, err := reserveUserActiveThreadCreation(context.Background(), db.DB, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer guard.Abort(db.DB)

	// Should have created a new reservation.
	var row orm.AgentUserActiveThread
	db.DB.Where("user_id = ? AND status = ?", "u1", userActiveThreadStatusCreating).First(&row)
	if row.CreateToken == "" {
		t.Fatal("expected creating reservation")
	}
}
