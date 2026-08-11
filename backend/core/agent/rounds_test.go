package agent

import (
	"testing"
	"time"

	"lazymind/core/common/orm"
)

// TestDeleteThreadLocalRows removes thread, rounds, records, and active rows in a transaction.
func TestDeleteThreadLocalRows(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()

	// Insert a thread with related rows.
	db.DB.Create(&orm.AgentThread{
		ThreadID: "thread-delete", CreateUserID: "u1", ThreadPayload: "{}",
		CreatedAt: now, UpdatedAt: now,
	})
	db.DB.Create(&orm.AgentThreadRound{ThreadID: "thread-delete", RoundID: "r1", CreatedAt: now, UpdatedAt: now})
	db.DB.Create(&orm.AgentThreadRound{ThreadID: "thread-delete", RoundID: "r2", CreatedAt: now, UpdatedAt: now})
	db.DB.Create(&orm.AgentThreadRecord{ThreadID: "thread-delete", ID: "rec1", CreatedAt: now, UpdatedAt: now})
	db.DB.Create(&orm.AgentUserActiveThread{
		UserID: "u1", ThreadID: "thread-delete", Status: userActiveThreadStatusActive,
		LeaseUntil: now, CreatedAt: now, UpdatedAt: now,
	})

	result, err := deleteThreadLocalRows(db.DB, "thread-delete")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["thread_id"] != "thread-delete" {
		t.Fatalf("thread_id in result: %v", result)
	}
	if d, ok := result["deleted_rounds"].(int64); !ok || d != 2 {
		t.Fatalf("deleted_rounds: got %v, want 2", result["deleted_rounds"])
	}
	if d, ok := result["deleted_records"].(int64); !ok || d != 1 {
		t.Fatalf("deleted_records: got %v, want 1", result["deleted_records"])
	}
	if d, ok := result["deleted_threads"].(int64); !ok || d != 1 {
		t.Fatalf("deleted_threads: got %v, want 1", result["deleted_threads"])
	}
	if d, ok := result["deleted_active_threads"].(int64); !ok || d != 1 {
		t.Fatalf("deleted_active_threads: got %v, want 1", result["deleted_active_threads"])
	}

	// Verify all rows are gone.
	var count int64
	db.DB.Model(&orm.AgentThread{}).Where("thread_id = ?", "thread-delete").Count(&count)
	if count != 0 {
		t.Fatalf("threads remaining: %d", count)
	}
	db.DB.Model(&orm.AgentThreadRound{}).Where("thread_id = ?", "thread-delete").Count(&count)
	if count != 0 {
		t.Fatalf("rounds remaining: %d", count)
	}
	db.DB.Model(&orm.AgentThreadRecord{}).Where("thread_id = ?", "thread-delete").Count(&count)
	if count != 0 {
		t.Fatalf("records remaining: %d", count)
	}
	db.DB.Model(&orm.AgentUserActiveThread{}).Where("thread_id = ?", "thread-delete").Count(&count)
	if count != 0 {
		t.Fatalf("active threads remaining: %d", count)
	}
}

// TestDeleteThreadLocalRows_NoRelatedRows returns zero counts, no error.
func TestDeleteThreadLocalRows_NoRelatedRows(t *testing.T) {
	db := newAgentTestDB(t)

	result, err := deleteThreadLocalRows(db.DB, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, key := range []string{"deleted_threads", "deleted_rounds", "deleted_records", "deleted_active_threads"} {
		if d, ok := result[key].(int64); !ok || d != 0 {
			t.Fatalf("%s: got %v, want 0", key, result[key])
		}
	}
}
