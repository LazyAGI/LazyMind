package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/attempt"
	"lazymind/core/workflow/executor"
	"lazymind/core/workflow/graphengine"
	workflowstore "lazymind/core/workflow/store"
)

func TestHostedAttemptBeginResumeSubmitAndReplay(t *testing.T) {
	service, db := hostedTestService(t)
	ctx := context.Background()

	begun, err := service.Begin(ctx, "owner", "session-1", "attempt-1")
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if begun.ExecutionID != "attempt-1" || begun.StepContract.Prompt != "write report" ||
		len(begun.StepContract.RequiredOutputs) != 1 || begun.StepContract.Metadata != nil {
		t.Fatalf("public execution contract: %+v", begun)
	}
	resumed, err := service.Resume(ctx, "owner", "session-1", "attempt-1")
	if err != nil || resumed.ExecutionID != begun.ExecutionID || resumed.StepContract.WorkflowRevision != "revision-1" {
		t.Fatalf("resume: %+v err=%v", resumed, err)
	}

	submission := Submission{Outcome: "succeeded", Summary: "done", ExecutorRef: "codex-task-1",
		Artifacts: []executor.Artifact{{Slot: "report", ContentType: "text/plain", Seq: 1, Value: json.RawMessage(`{"text":"result"}`)}},
	}
	result, err := service.Submit(ctx, "owner", "session-1", "attempt-1", submission)
	if err != nil || result.AttemptStatus != "succeeded" || result.AlreadyTerminal {
		t.Fatalf("submit: %+v err=%v", result, err)
	}
	replayed, err := service.Submit(ctx, "owner", "session-1", "attempt-1", submission)
	if err != nil || !replayed.AlreadyTerminal || replayed.AttemptStatus != "succeeded" {
		t.Fatalf("terminal replay: %+v err=%v", replayed, err)
	}
	var session orm.WorkflowSession
	if err := db.First(&session, "id = ?", "session-1").Error; err != nil || session.Status != "completed" {
		t.Fatalf("session projection: %+v err=%v", session, err)
	}
	var artifacts []orm.WorkflowSlotRevision
	if err := db.Where("session_id = ? AND selected = ?", "session-1", true).Find(&artifacts).Error; err != nil ||
		len(artifacts) != 1 || artifacts[0].ProducerAttemptID != "attempt-1" {
		t.Fatalf("artifacts: %+v err=%v", artifacts, err)
	}
	if _, err := service.Begin(ctx, "other", "session-1", "attempt-1"); !errors.Is(err, workflowstore.ErrPermissionDenied) {
		t.Fatalf("owner isolation: %v", err)
	}
}

func TestHostedSubmissionRejectsUndeclaredOutputBeforeWrite(t *testing.T) {
	service, db := hostedTestService(t)
	if _, err := service.Begin(context.Background(), "owner", "session-1", "attempt-1"); err != nil {
		t.Fatal(err)
	}
	_, err := service.Submit(context.Background(), "owner", "session-1", "attempt-1", Submission{
		Outcome: "succeeded", Artifacts: []executor.Artifact{{Slot: "secret", Value: json.RawMessage(`{"value":1}`), Seq: 1}},
	})
	var protocolErr *ProtocolError
	if !errors.As(err, &protocolErr) || protocolErr.Code != "OUTPUT_SLOT_UNDECLARED" {
		t.Fatalf("undeclared output error: %v", err)
	}
	var count int64
	if err := db.Model(&orm.WorkflowSlotRevision{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("invalid output was persisted: count=%d err=%v", count, err)
	}
}

func TestHostedCompletionUpdatesTrackedTaskAndDesktopNotice(t *testing.T) {
	for _, outcome := range []string{"succeeded", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			service, db := hostedTestService(t)
			if err := db.AutoMigrate(&orm.TaskCenterTask{}, &orm.UserNotificationPreferences{}, &orm.TaskNotification{}); err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "session-1").Update("conversation_id", "conversation").Error; err != nil {
				t.Fatal(err)
			}
			sessionID := "session-1"
			task := orm.TaskCenterTask{ID: "workflow", UserID: "owner", ConversationID: "conversation", TaskType: "workflow_run", WorkflowSessionID: &sessionID, Status: "running"}
			if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := service.Begin(t.Context(), "owner", sessionID, "attempt-1"); err != nil {
				t.Fatal(err)
			}
			submission := Submission{Outcome: outcome, Summary: "private provider diagnostic"}
			if outcome == "succeeded" {
				submission.Artifacts = []executor.Artifact{{Slot: "report", ContentType: "text/plain", Seq: 1, Value: json.RawMessage(`{"text":"result"}`)}}
			}
			for range 2 {
				if _, err := service.Submit(t.Context(), "owner", sessionID, "attempt-1", submission); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
				t.Fatal(err)
			}
			if task.Status != outcome || task.FinishedAt == nil {
				t.Fatalf("hosted completion left task active: %#v", task)
			}
			var notices []orm.TaskNotification
			if err := db.Find(&notices).Error; err != nil {
				t.Fatal(err)
			}
			if len(notices) != 1 || notices[0].TaskID != task.ID || notices[0].Channel != "desktop" || notices[0].Event != outcome {
				t.Fatalf("hosted desktop completion notices=%#v", notices)
			}
		})
	}
}

func hostedTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db := orm.OpenTestDB(t).DB
	models := []any{
		&orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.WorkflowOutbox{}, &orm.WorkflowEvent{},
		&orm.WorkflowCommand{}, &orm.WorkflowRevision{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{},
		&orm.WorkflowAttemptInputBinding{}, &orm.WorkflowInputBinding{}, &orm.WorkflowSlotRevision{},
		&orm.WorkflowHumanArtifact{}, &orm.WorkflowSlotOrder{}, &orm.WorkflowRouteDecision{}, &orm.SubAgentArtifact{},
		&orm.WorkflowStepIntent{}, &orm.WorkflowApprovalPreference{},
		&orm.UserSelectedModel{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{},
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	graph := graphengine.CompiledStateGraph{
		SchemaVersion: graphengine.SchemaVersion, GraphHash: "graph-1", StartRoute: "all",
		Nodes: map[string]graphengine.CompiledNode{"write": {
			ID: "write", Prompt: "write report", Outputs: []string{"report"}, RequiredOutputs: []string{"report"},
		}},
		ControlEdges: []graphengine.CompiledEdge{
			{ID: "start-write", From: "__start__", To: "write"},
			{ID: "write-end", From: "write", To: "__end__"},
		},
		MaterialProducers: map[string]graphengine.ProducerRef{"report": {Kind: "step", StepID: "write"}},
		InputExpressions:  map[string]graphengine.Expression{}, OptionalInputs: map[string][]graphengine.MaterialRef{},
		StaticOrder: []string{"write"},
	}
	if err := db.Create(&orm.WorkflowRevision{ID: "revision-1", WorkflowResourceID: "resource-1", RevisionNo: 1,
		GraphHash: graph.GraphHash, GraphSchemaVersion: graph.SchemaVersion, CompiledGraph: graph.JSON(), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSession{ID: "session-1", OriginHost: HostName, ControllerHost: HostName,
		WorkflowID: "writer", WorkflowRevisionID: "revision-1", GraphHash: graph.GraphHash,
		GraphSchemaVersion: graph.SchemaVersion, Status: "active", StateVersion: 1,
		CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	attempts := attempt.New(db, attempt.Config{LeaseDuration: time.Minute})
	payload, _ := json.Marshal(executor.AttemptContext{Operation: "advance"})
	if _, err := attempts.Queue(context.Background(), attempt.QueueRequest{AttemptID: "attempt-1", SessionID: "session-1",
		StepID: "write", AttemptNo: 1, Payload: payload, OwnerUserID: "owner"}); err != nil {
		t.Fatal(err)
	}
	return &Service{DB: db, Store: workflowstore.New(db), Attempts: attempts,
		Contexts: executor.DBContextLoader{DB: db}, Artifacts: executor.DBArtifactSink{DB: db}}, db
}
