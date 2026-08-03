package orm

import (
	"testing"
)

func TestAgentModelsAutoMigrate(t *testing.T) {
	db := MigrateTestDB(t, &AgentThread{}, &AgentUserActiveThread{}, &AgentThreadRecord{}, &AgentThreadStep{}, &AgentThreadRound{})

	for _, model := range []any{
		&AgentThread{},
		&AgentUserActiveThread{},
		&AgentThreadRecord{},
		&AgentThreadStep{},
		&AgentThreadRound{},
	} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("expected table for %T to exist", model)
		}
	}

	if !db.Migrator().HasColumn(&AgentThread{}, "thread_id") {
		t.Fatal("expected agent_threads.thread_id column")
	}
	if !db.Migrator().HasColumn(&AgentThread{}, "status") {
		t.Fatal("expected agent_threads.status column")
	}

	if !db.Migrator().HasIndex(&AgentUserActiveThread{}, "idx_agent_user_active_threads_status_lease") {
		t.Fatal("expected idx_agent_user_active_threads_status_lease index")
	}

	if !db.Migrator().HasIndex(&AgentThreadRecord{}, "uk_agent_thread_records_record_key") {
		t.Fatal("expected uk_agent_thread_records_record_key unique index")
	}

	if !db.Migrator().HasColumn(&AgentThreadStep{}, "thread_id") {
		t.Fatal("expected agent_thread_steps.thread_id column")
	}
	if !db.Migrator().HasColumn(&AgentThreadStep{}, "step_id") {
		t.Fatal("expected agent_thread_steps.step_id column")
	}
	if !db.Migrator().HasIndex(&AgentThreadStep{}, "idx_agent_thread_steps_thread_order") {
		t.Fatal("expected idx_agent_thread_steps_thread_order index")
	}
	if !db.Migrator().HasIndex(&AgentThreadStep{}, "idx_agent_thread_steps_thread_active") {
		t.Fatal("expected idx_agent_thread_steps_thread_active index")
	}

	if !db.Migrator().HasColumn(&AgentThreadRound{}, "thread_id") {
		t.Fatal("expected agent_thread_rounds.thread_id column")
	}
	if !db.Migrator().HasColumn(&AgentThreadRound{}, "round_id") {
		t.Fatal("expected agent_thread_rounds.round_id column")
	}
	if !db.Migrator().HasColumn(&AgentThreadRound{}, "status") {
		t.Fatal("expected agent_thread_rounds.status column")
	}
}
