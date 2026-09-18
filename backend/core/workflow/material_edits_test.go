package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlstore"
	"testing"
	"time"
)

func TestMaterialEditPreservesProducerAndIndependentExecution(t *testing.T) {
	for _, controlled := range []bool{false, true} {
		name := "native"
		if controlled {
			name = "controlled"
		}
		t.Run(name, func(t *testing.T) {
			db, _ := setupBatchTransitionSession(t)
			if err := db.AutoMigrate(&orm.WorkflowReviewCheckpoint{}, &orm.WorkflowHostAction{}); err != nil {
				t.Fatal(err)
			}
			if controlled {
				db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Update("control_protocol", "workflow.control.v1")
			}
			expires := time.Now().Add(time.Hour)
			for _, row := range []orm.WorkflowSessionStep{
				{ID: "source", SessionID: "batch-session", StepID: "branch_b", TaskID: "source-task", Attempt: 1, Status: "succeeded", Validity: "effective"},
				{ID: "consumer", SessionID: "batch-session", StepID: "needs_b", TaskID: "consumer-task", Attempt: 1, Status: "running", Validity: "effective", LeaseToken: "old", LeaseExpiresAt: &expires},
				{ID: "independent", SessionID: "batch-session", StepID: "blocked_d", TaskID: "independent-task", Attempt: 1, Status: "running", Validity: "effective", LeaseToken: "keep", LeaseExpiresAt: &expires},
			} {
				if err := db.Create(&row).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Create(&orm.WorkflowSlotRevision{ID: "source-output", SessionID: "batch-session", SlotID: "source-material", StepID: "branch_b", Attempt: 1, ProducerAttemptID: "source", Revision: 1, Selected: true, Validity: "effective"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.WorkflowAttemptInputBinding{ID: "binding", AttemptID: "consumer", MaterialID: "source-material", MaterialRevisionID: "source-output"}).Error; err != nil {
				t.Fatal(err)
			}
			var session orm.WorkflowSession
			db.First(&session, "id = ?", "batch-session")
			if err := db.Transaction(func(tx *gorm.DB) error { return controlstore.PrepareMaterialEdit(tx, &session, "source-material") }); err != nil {
				t.Fatal(err)
			}
			var source, consumer, independent orm.WorkflowSessionStep
			db.First(&source, "id = ?", "source")
			db.First(&consumer, "id = ?", "consumer")
			db.First(&independent, "id = ?", "independent")
			if source.Status != "succeeded" || source.Validity != "effective" {
				t.Fatalf("producer was discarded: %+v", source)
			}
			if consumer.Status != "cancelled" || consumer.Validity != "stale" || consumer.LeaseToken != "" {
				t.Fatalf("consumer not fenced: %+v", consumer)
			}
			if independent.Status != "running" || independent.Validity != "effective" || independent.LeaseToken != "keep" {
				t.Fatalf("independent changed: %+v", independent)
			}
			db.First(&session, "id = ?", "batch-session")
			expectControlCode(t, controlstore.GuardBegin(db.DB, session), "EDITS_PENDING_CONTINUE")
			if err := controlstore.ValidateExecution(db.DB, session, "consumer", "old"); err == nil {
				t.Fatal("old writer admitted")
			}
			if err := controlstore.ValidateExecution(db.DB, session, "independent", "keep"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStoppedMaterialEditAndFailedSave(t *testing.T) {
	svc, _ := hostControlFixture(t)
	db := svc.DB
	if err := db.AutoMigrate(&orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{}, &orm.WorkflowSlotOrder{}); err != nil {
		t.Fatal(err)
	}
	db.Model(&orm.WorkflowSession{}).Where("id = ?", "run").Update("status", "stopped")
	if err := db.Create(&orm.WorkflowSessionStep{ID: "partial", SessionID: "run", StepID: "draft", TaskID: "task", Attempt: 1, Status: "cancelled", Validity: "effective"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSlotRevision{ID: "partial-output", SessionID: "run", SlotID: "draft", StepID: "draft", Attempt: 1, ProducerAttemptID: "partial", Selected: true, Validity: "effective"}).Error; err != nil {
		t.Fatal(err)
	}
	// The draft fast path must roll back preparation when it falls back to
	// creating an immutable revision, rather than invalidating before a real save.
	if _, updated, err := UpdateSelectedHumanArtifactValue(context.Background(), db, "run", "draft", nil, "text", json.RawMessage(`{"text":"edited"}`), nil); err != nil || updated {
		t.Fatalf("draft fallback: %v %v", updated, err)
	}
	var beforeSave orm.WorkflowSession
	db.First(&beforeSave, "id = ?", "run")
	if controlstore.EditPaused(beforeSave) {
		t.Fatal("draft fallback committed invalidation without saving")
	}
	failure := errors.New("save failed")
	if err := reviewMaterialTransaction(context.Background(), db, "run", "draft", func(*gorm.DB) error { return failure }); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	var session orm.WorkflowSession
	db.First(&session, "id = ?", "run")
	if controlstore.EditPaused(session) {
		t.Fatal("failed save committed pause")
	}
	if err := reviewMaterialTransaction(context.Background(), db, "run", "draft", func(*gorm.DB) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var partial orm.WorkflowSessionStep
	db.First(&partial, "id = ?", "partial")
	if partial.Status != "cancelled" {
		t.Fatal("partial output was treated as complete")
	}
	db.First(&session, "id = ?", "run")
	if !controlstore.EditPaused(session) || session.Status != "stopped" {
		t.Fatalf("unexpected saved state: %+v", session)
	}
	action := orm.WorkflowHostAction{ID: "cancel", SessionID: "run", Kind: "cancel", Status: "pending", BindingGeneration: 1}
	var binding controlstore.Binding
	_ = json.Unmarshal([]byte(session.ControlBindingJSON), &binding)
	action.BindingGeneration = binding.Generation
	if err := db.Create(&action).Error; err != nil {
		t.Fatal(err)
	}
	expectControlCode(t, controlstore.GuardMaterialEdit(db, session, "draft"), "DELIVERY_PENDING")
}

func TestContinueSavedEditsDoesNotRegenerateProducer(t *testing.T) {
	for _, protocol := range []string{"", "workflow.control.v1"} {
		t.Run("protocol="+protocol, func(t *testing.T) {
			db, _ := setupBatchTransitionSession(t)
			if err := db.AutoMigrate(&orm.WorkflowReviewCheckpoint{}, &orm.WorkflowHostAction{}, &orm.WorkflowCommand{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{}); err != nil {
				t.Fatal(err)
			}
			binding := `{"edit_paused":true}`
			if protocol != "" {
				binding = `{"edit_paused":true,"connector_id":"connector","driver_session_id":"driver"}`
			}
			if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Updates(map[string]any{"control_protocol": protocol, "control_binding_json": binding, "status": "waiting"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.WorkflowSessionStep{ID: "source", SessionID: "batch-session", StepID: "branch_b", TaskID: "source-task", Attempt: 1, Status: "succeeded", Validity: "effective"}).Error; err != nil {
				t.Fatal(err)
			}
			svc := WorkflowControlService{DB: db.DB}
			command := WorkflowControlCommand{CommandID: "continue-edits", Kind: "continue", StateVersion: 4}
			result, err := svc.Execute(context.Background(), "batch-user", "batch-session", command)
			if err != nil {
				t.Fatal(err)
			}
			var next orm.WorkflowSessionStep
			if err := db.First(&next, "id = ?", result.Receipt.ExecutionID).Error; err != nil {
				t.Fatal(err)
			}
			if next.StepID != "branch_c" {
				t.Fatalf("continued wrong step: %+v", next)
			}
			var count int64
			db.Model(&orm.WorkflowSessionStep{}).Where("session_id = ? AND step_id = ?", "batch-session", "branch_b").Count(&count)
			if count != 1 {
				t.Fatal("saved producer was regenerated")
			}
			replay, err := svc.Execute(context.Background(), "batch-user", "batch-session", command)
			if err != nil || replay.Receipt != result.Receipt {
				t.Fatalf("replayed save continuation: %+v %v", replay, err)
			}
		})
	}
}
