package workflow

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/subagent"
	"lazymind/core/workflow/attempt"
	"lazymind/core/workflow/executor"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func dispatchDB(t *testing.T, expanded bool) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "dispatch.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []any{&orm.WorkflowSessionStep{}, &orm.SubAgentTask{}}
	if expanded {
		models = append(models, &orm.WorkflowOutbox{}, &orm.WorkflowEvent{})
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedDispatchStep(t *testing.T, db *gorm.DB, id string) {
	t.Helper()
	now := time.Now().UTC()
	if err := db.Create(&orm.SubAgentTask{ID: "task-" + id, ConversationID: "conversation", AgentType: "workflow_step", Title: "step", Objective: "make report", Mode: "manual", Status: "pending", Params: json.RawMessage(`{"output_slot_types":{"report":"file"}}`), InputSlots: json.RawMessage(`[]`), OutputSlots: json.RawMessage(`["report"]`), CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSessionStep{ID: id, SessionID: "session", StepID: "step", Attempt: 1, TaskID: "task-" + id, Status: StepStatusPending, Validity: "effective", ProgressJSON: "{}", ResultJSON: "{}", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
}

func requestFor(id string) subagent.RunRequest {
	return subagent.RunRequest{TaskID: "task-" + id, AgentType: "workflow_step", WorkspacePath: "/host/private", DBDSN: "secret", LLMConfig: map[string]any{"api_key": "secret"}, Params: map[string]any{"operation": "execute", "objective": "make report"}}
}

func TestCanonicalQueueContainsNeutralContext(t *testing.T) {
	db := dispatchDB(t, true)
	seedDispatchStep(t, db, "a1")
	if err := enqueueWorkflowAttemptRunner(context.Background(), db, requestFor("a1")); err != nil {
		t.Fatal(err)
	}
	var row orm.WorkflowOutbox
	if err := db.First(&row, "attempt_id = ?", "a1").Error; err != nil {
		t.Fatal(err)
	}
	var value executor.AttemptContext
	if err := json.Unmarshal(row.PayloadJSON, &value); err != nil {
		t.Fatal(err)
	}
	if value.AttemptID != "a1" || value.Operation != "execute" || value.DeclaredOutputTypes["report"] != "file" {
		t.Fatalf("context=%#v", value)
	}
	text := string(row.PayloadJSON)
	for _, secret := range []string{"/host/private", "api_key", "secret", "llm_config", "db_dsn"} {
		if strings.Contains(text, secret) {
			t.Fatalf("host private value leaked: %s", text)
		}
	}
}

func TestAlgorithmOutageLeavesQueuedAndRestartCanClaim(t *testing.T) {
	db := dispatchDB(t, true)
	seedDispatchStep(t, db, "a2")
	if err := enqueueWorkflowAttemptRunner(context.Background(), db, requestFor("a2")); err != nil {
		t.Fatal(err)
	}
	// No algorithm process is contacted. A fresh service instance after restart
	// claims the same durable Attempt.
	var queued orm.WorkflowSessionStep
	if err := db.First(&queued, "id = ?", "a2").Error; err != nil || queued.Status != "queued" {
		t.Fatalf("queued=%#v err=%v", queued, err)
	}
	restarted := attempt.New(db, attempt.Config{LeaseDuration: time.Minute})
	claim, err := restarted.Claim(context.Background(), "executor-after-restart")
	if err != nil {
		t.Fatal(err)
	}
	if claim.AttemptID != "a2" || claim.FencingGeneration != 1 {
		t.Fatalf("claim=%#v", claim)
	}
}
