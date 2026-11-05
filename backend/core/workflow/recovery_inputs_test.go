package workflow

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/artifactfile"
	"lazymind/core/workflow/controlpolicy"
	"lazymind/core/workflow/graphengine"
)

func recoveryFixture(t *testing.T, controlled bool) (*orm.DB, *graphengine.CompiledStateGraph) {
	t.Helper()
	db, _ := setupBatchTransitionSession(t)
	if err := db.AutoMigrate(&orm.WorkflowInputResource{}, &orm.WorkflowHumanArtifact{},
		&orm.WorkflowReviewCheckpoint{}, &orm.WorkflowHostAction{}, &orm.WorkflowCommand{}); err != nil {
		t.Fatal(err)
	}
	var revision orm.WorkflowRevision
	if err := db.First(&revision, "id = ?", "batch-revision").Error; err != nil {
		t.Fatal(err)
	}
	var graph graphengine.CompiledStateGraph
	if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
		t.Fatal(err)
	}
	graph.Runtime.RecoveryInputs = map[string]map[string]string{
		"branch_b": {"recovery_pool": "feedback_pool", "recovery_document": "feedback_document"},
	}
	node := graph.Nodes["branch_b"]
	node.Outputs = []string{"draft"}
	node.OptionalInputs = []graphengine.MaterialRef{{Material: "recovery_pool"}, {Material: "recovery_document"}}
	graph.Nodes["branch_b"] = node
	graph.Nodes["feedback"] = graphengine.CompiledNode{ID: "feedback", Input: &graphengine.Expression{Material: "draft"}}
	graph.MaterialProducers["draft"] = graphengine.ProducerRef{Kind: "step", StepID: "branch_b"}
	graph.ControlEdges = append(graph.ControlEdges, graphengine.CompiledEdge{From: "branch_b", To: "feedback"})
	graph.MaterialProducers["recovery_pool"] = graphengine.ProducerRef{Kind: "external"}
	graph.MaterialProducers["recovery_document"] = graphengine.ProducerRef{Kind: "external"}
	graph.MaterialProducers["feedback_pool"] = graphengine.ProducerRef{Kind: "step", StepID: "feedback"}
	graph.MaterialProducers["feedback_document"] = graphengine.ProducerRef{Kind: "step", StepID: "feedback"}
	if err := db.Model(&revision).Update("compiled_graph", graph.JSON()).Error; err != nil {
		t.Fatal(err)
	}
	if controlled {
		if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Updates(map[string]any{
			"control_protocol": controlpolicy.Protocol, "control_binding_json": "{}",
			"origin_host": "external-agent", "controller_host": "external-agent",
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&orm.WorkflowSessionStep{ID: "original", SessionID: "batch-session", StepID: "branch_b",
		TaskID: "original", Attempt: 1, Status: "succeeded", Validity: "effective"}).Error; err != nil {
		t.Fatal(err)
	}
	return db, &graph
}

func putRecoveryFeedback(t *testing.T, db *orm.DB, round int) {
	t.Helper()
	attempt := fmt.Sprintf("feedback-%d", round)
	var producer orm.WorkflowSessionStep
	if err := db.Where("session_id = ? AND step_id = ? AND validity = ?", "batch-session", "branch_b", "effective").Order("attempt DESC").First(&producer).Error; err != nil {
		t.Fatal(err)
	}
	draftID := fmt.Sprintf("draft-%d", round)
	if err := db.Create(&orm.WorkflowSlotRevision{ID: draftID, SessionID: "batch-session", SlotID: "draft", Slot: "draft", StepID: "branch_b", ProducerAttemptID: producer.ID, Attempt: producer.Attempt, Revision: round, Selected: true, Validity: "effective", ContentSnapshot: []byte(`"draft"`)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSessionStep{ID: attempt, SessionID: "batch-session", StepID: "feedback",
		TaskID: attempt, Attempt: round, Status: "succeeded", Validity: "effective"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowAttemptInputBinding{ID: fmt.Sprintf("feedback-input-%d", round), SessionID: "batch-session", AttemptID: attempt, MaterialID: "draft", MaterialRevisionID: draftID, SourceType: "artifact"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, slot := range []string{"feedback_pool", "feedback_document"} {
		human := fmt.Sprintf("%s-human-%d", slot, round)
		value, _ := json.Marshal(map[string]any{"text": fmt.Sprintf("user-edited-%d", round)})
		if slot == "feedback_pool" {
			value = json.RawMessage(fmt.Sprintf("{\"sequence\":%d}", round))
		}
		if err := db.Create(&orm.WorkflowHumanArtifact{ID: human, SessionID: "batch-session", Slot: slot,
			ContentType: "json", Value: value}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&orm.WorkflowSlotRevision{}).Where("session_id = ? AND slot_id = ?", "batch-session", slot).
			Update("selected", false).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.WorkflowSlotRevision{ID: fmt.Sprintf("%s-%d", slot, round), SessionID: "batch-session",
			SlotID: slot, Slot: slot, StepID: "feedback", ProducerAttemptID: attempt, Attempt: round,
			Revision: round, HumanArtifactID: &human, Selected: true, Validity: "effective"}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestRecoveryOptOutDoesNotTouchStorage(t *testing.T) {
	// nil DB proves an existing workflow exits before querying/writing anything.
	if err := snapshotRecoveryInputs(context.Background(), nil, &orm.WorkflowSession{},
		&graphengine.CompiledStateGraph{}, "command", "draft"); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverySnapshotSurvivesRealRewindAndPinsTenRounds(t *testing.T) {
	for _, controlled := range []bool{false, true} {
		t.Run(fmt.Sprintf("controlled-%t", controlled), func(t *testing.T) {
			db, _ := recoveryFixture(t, controlled)
			for round := 1; round <= 10; round++ {
				putRecoveryFeedback(t, db, round)
				var session orm.WorkflowSession
				if err := db.First(&session, "id = ?", "batch-session").Error; err != nil {
					t.Fatal(err)
				}
				task := fmt.Sprintf("rerun-%d", round)
				req := transitionCommandRequest{CommandID: fmt.Sprintf("rewind-%d", round), Operation: "rewind",
					TargetStepID: "branch_b", TaskID: task, ExpectedStateVersion: session.StateVersion,
					UserInput: "继续，使用已确认反馈", RetryOrigin: "user", HandOff: true, controlAuthorized: controlled}
				err := db.Transaction(func(tx *gorm.DB) error {
					response, _, _, err := applyWorkflowTransition(context.Background(), tx, session.ID, req)
					if err == nil && !response.Accepted {
						return fmt.Errorf("not accepted: %+v", response)
					}
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
				var current []orm.WorkflowInputBinding
				if err := db.Where("workflow_session_id = ? AND validity = ?", session.ID, "effective").Find(&current).Error; err != nil {
					t.Fatal(err)
				}
				if len(current) != 2 {
					t.Fatalf("expected one pool+document pair: %+v", current)
				}
				var execution orm.WorkflowSessionStep
				if err := db.First(&execution, "task_id = ?", task).Error; err != nil {
					t.Fatal(err)
				}
				for _, binding := range current {
					var resource orm.WorkflowInputResource
					if err := db.First(&resource, "id = ?", binding.ResourceID).Error; err != nil {
						t.Fatal(err)
					}
					if resource.OwnerUserID != session.CreateUserID {
						t.Fatal("resource crossed owner boundary")
					}
					expected := fmt.Sprintf("{\"sequence\":%d}", round)
					if binding.MaterialID == "recovery_document" {
						expected = fmt.Sprintf("user-edited-%d", round)
					}
					if string(resource.Content) != expected {
						t.Fatalf("lost edited feedback: %s", resource.Content)
					}
					var count int64
					db.Model(&orm.WorkflowAttemptInputBinding{}).Where("attempt_id = ? AND material_revision_id = ?", execution.ID, binding.ID).Count(&count)
					if count != 1 {
						t.Fatalf("rerun did not pin external snapshot: %+v", binding)
					}
				}
				var source orm.WorkflowSlotRevision
				db.First(&source, "id = ?", fmt.Sprintf("feedback_document-%d", round))
				if source.Validity != "stale" || source.Selected {
					t.Fatalf("rewind must still invalidate downstream: %+v", source)
				}
				if err := db.Model(&execution).Update("status", "succeeded").Error; err != nil {
					t.Fatal(err)
				}
			}
			var count int64
			db.Model(&orm.WorkflowInputBinding{}).Where("workflow_session_id = ?", "batch-session").Count(&count)
			if count != 20 {
				t.Fatalf("lost immutable binding history: %d", count)
			}
		})
	}
}

func TestRecoverySnapshotIdempotencyAndRejectionRollback(t *testing.T) {
	db, graph := recoveryFixture(t, false)
	putRecoveryFeedback(t, db, 1)
	var session orm.WorkflowSession
	db.First(&session, "id = ?", "batch-session")
	for i := 0; i < 2; i++ {
		if err := db.Transaction(func(tx *gorm.DB) error {
			return snapshotRecoveryInputs(context.Background(), tx, &session, graph, "same-command", "branch_b")
		}); err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	db.Model(&orm.WorkflowInputBinding{}).Count(&count)
	if count != 2 {
		t.Fatalf("duplicate snapshot bindings: %d", count)
	}
	putRecoveryFeedback(t, db, 2)
	db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "original").Update("status", "failed")
	err := db.Transaction(func(tx *gorm.DB) error {
		_, _, _, err := applyWorkflowTransition(context.Background(), tx, session.ID, transitionCommandRequest{
			CommandID: "invalid-rewind", Operation: "rewind", TargetStepID: "branch_b", TaskID: "invalid-task", ExpectedStateVersion: session.StateVersion,
		})
		return err
	})
	if err == nil {
		t.Fatal("failed attempt allowed rewind")
	}
	db.Model(&orm.WorkflowInputBinding{}).Count(&count)
	if count != 2 {
		t.Fatalf("rejected rewind committed feedback: %d", count)
	}
	db.Model(&orm.WorkflowInputResource{}).Count(&count)
	if count != 2 {
		t.Fatalf("rejected rewind committed resources: %d", count)
	}
}

func TestRecoveryDoesNotCopyUnapprovedControlledFeedback(t *testing.T) {
	db, graph := recoveryFixture(t, true)
	putRecoveryFeedback(t, db, 1)
	db.Model(&orm.WorkflowSessionStep{}).Where("step_id = ?", "feedback").Update("review_required", true)
	var session orm.WorkflowSession
	db.First(&session, "id = ?", "batch-session")
	if err := snapshotRecoveryInputs(context.Background(), db.DB, &session, graph, "unapproved", "branch_b"); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&orm.WorkflowInputBinding{}).Count(&count)
	if count != 0 {
		t.Fatal("pending user approval copied to recovery inputs")
	}
	// Explicitly accepted revisions become eligible without changing their contents.
	manifest := "{\"items\":[{\"revision_id\":\"feedback_pool-1\"},{\"revision_id\":\"feedback_document-1\"}]}"
	if err := db.Create(&orm.WorkflowReviewCheckpoint{ID: "accepted", SessionID: session.ID, AttemptID: "feedback-1",
		Status: "accepted", ManifestJSON: manifest}).Error; err != nil {
		t.Fatal(err)
	}
	if err := snapshotRecoveryInputs(context.Background(), db.DB, &session, graph, "approved", "branch_b"); err != nil {
		t.Fatal(err)
	}
	db.Model(&orm.WorkflowInputBinding{}).Count(&count)
	if count != 2 {
		t.Fatal("accepted feedback not copied")
	}
	var resources []orm.WorkflowInputResource
	db.Find(&resources)
	for _, resource := range resources {
		if strings.Contains(string(resource.Content), "accepted") {
			t.Fatal("copied approval envelope instead of content")
		}
	}
}

func TestRecoveryFreezesManagedFileContentsNotWorkspacePaths(t *testing.T) {
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	db, graph := recoveryFixture(t, false)
	graph.MaterialTypes = map[string]string{"recovery_document": "file"}
	putRecoveryFeedback(t, db, 1)
	content := []byte(strings.Repeat("已确认的长稿件。", 6000))
	carrier, err := json.Marshal(map[string]any{"storage": "inline_base64", "name": "draft.txt", "size": len(content), "content_base64": base64.StdEncoding.EncodeToString(content)})
	if err != nil {
		t.Fatal(err)
	}
	managed, _, err := artifactfile.Materialize("batch-session", "large-feedback", carrier)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "feedback_document-1").Updates(map[string]any{"human_artifact_id": nil, "content_snapshot": managed}).Error; err != nil {
		t.Fatal(err)
	}
	var session orm.WorkflowSession
	db.First(&session, "id = ?", "batch-session")
	if err := db.Transaction(func(tx *gorm.DB) error {
		return snapshotRecoveryInputs(context.Background(), tx, &session, graph, "large-command", "branch_b")
	}); err != nil {
		t.Fatal(err)
	}
	var binding orm.WorkflowInputBinding
	db.First(&binding, "material_id = ?", "recovery_document")
	var resource orm.WorkflowInputResource
	if err := db.First(&resource, "id = ?", binding.ResourceID).Error; err != nil {
		t.Fatal(err)
	}
	if string(resource.Content) != string(content) {
		t.Fatal("recovery copied a file locator instead of frozen bytes")
	}
}
