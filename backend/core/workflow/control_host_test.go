package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	workflowattempt "lazymind/core/workflow/attempt"
	"lazymind/core/workflow/controlpolicy"
	"lazymind/core/workflow/controlstore"
)

func hostControlFixture(t *testing.T) (WorkflowControlService, WorkflowHostIdentity) {
	t.Helper()
	db := newTestDB(t).DB
	if err := db.AutoMigrate(&orm.WorkflowReviewCheckpoint{}, &orm.WorkflowHostAction{}, &orm.WorkflowCommand{}, &orm.WorkflowEvent{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSession{ID: "run", CreateUserID: "owner", Status: "active", StateVersion: 1, ControllerHost: "external-agent", ControlProtocol: controlpolicy.Protocol, ControlBindingJSON: `{"required":true,"provider":"deepseek-harness"}`}).Error; err != nil {
		t.Fatal(err)
	}
	svc := WorkflowControlService{DB: db}
	identity := WorkflowHostIdentity{ConnectorID: "connector", Credential: strings.Repeat("x", 64), InstanceID: "process-1"}
	_, err := svc.Bind(context.Background(), "owner", "run", WorkflowHostBindingRequest{ConnectorID: identity.ConnectorID, Credential: identity.Credential, Provider: "deepseek-harness", DriverSessionID: "driver"})
	if err != nil {
		t.Fatal(err)
	}
	return svc, identity
}
func expectControlCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *controlstore.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}
func currentControl(t *testing.T, db *gorm.DB) *controlstore.Snapshot {
	t.Helper()
	var s orm.WorkflowSession
	if err := db.First(&s, "id = ?", "run").Error; err != nil {
		t.Fatal(err)
	}
	c, err := controlstore.Read(db, s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestHostBindingCannotTransferDriverOrClearPendingReview(t *testing.T) {
	svc, id := hostControlFixture(t)
	if err := svc.DB.Create(&orm.WorkflowReviewCheckpoint{ID: "review", SessionID: "run", AttemptID: "a", Status: "pending", Version: 1, ManifestHash: "hash", SlotsJSON: "[]", ManifestJSON: `{"items":[],"orders":{}}`}).Error; err != nil {
		t.Fatal(err)
	}
	c, err := svc.Bind(context.Background(), "owner", "run", WorkflowHostBindingRequest{ConnectorID: id.ConnectorID, Credential: id.Credential, Provider: "deepseek-harness", DriverSessionID: "driver"})
	if err != nil || c.Continuation != "awaiting_user" {
		t.Fatalf("binding cleared review: %+v %v", c, err)
	}
	_, err = svc.Bind(context.Background(), "owner", "run", WorkflowHostBindingRequest{ConnectorID: id.ConnectorID, Credential: id.Credential, Provider: "deepseek-harness", DriverSessionID: "other"})
	expectControlCode(t, err, "BINDING_CONFLICT")
}
func TestHostDeliveryUnknownDoesNotResendAndReconcilesExactReceipt(t *testing.T) {
	svc, id := hostControlFixture(t)
	ctx := context.Background()
	c := currentControl(t, svc.DB)
	result, err := svc.Execute(ctx, "owner", "run", WorkflowControlCommand{CommandID: "continue-1", Kind: "continue", StateVersion: c.StateVersion})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := svc.ClaimHostAction(ctx, "owner", result.Receipt.ActionID, id)
	if err != nil || claim.DispatchToken == "" {
		t.Fatalf("claim: %+v %v", claim, err)
	}
	other := id
	other.InstanceID = "process-2"
	_, err = svc.ClaimHostAction(ctx, "owner", claim.Action.ID, other)
	expectControlCode(t, err, "DELIVERY_PENDING")
	if err := svc.DB.Model(&orm.WorkflowHostAction{}).Where("id = ?", claim.Action.ID).Update("dispatch_expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	unknown, err := svc.ClaimHostAction(ctx, "owner", claim.Action.ID, other)
	if err != nil || unknown.Action.Status != "unknown" || unknown.DispatchToken != "" {
		t.Fatalf("expired dispatch was resent: %+v %v", unknown, err)
	}
	_, err = svc.Execute(ctx, "owner", "run", WorkflowControlCommand{CommandID: "continue-2", Kind: "continue", StateVersion: unknown.Control.StateVersion})
	expectControlCode(t, err, "DELIVERY_UNKNOWN")
	accepted, err := svc.SettleHostAction(ctx, "owner", claim.Action.ID, WorkflowHostReceipt{ConnectorID: other.ConnectorID, Credential: other.Credential, InstanceID: other.InstanceID, Status: "accepted", NativeEventSeq: 42})
	if err != nil || accepted.Status != "accepted" {
		t.Fatalf("reconcile: %+v %v", accepted, err)
	}
	page, err := svc.HostActions(ctx, "owner", id, "")
	if err != nil || len(page.Actions) != 0 {
		t.Fatalf("accepted action still dispatches: %+v %v", page, err)
	}
}
func TestStopFencesOldWritesAndResumePreservesReview(t *testing.T) {
	svc, id := hostControlFixture(t)
	ctx := context.Background()
	if err := svc.DB.Create(&orm.WorkflowSessionStep{ID: "attempt", SessionID: "run", StepID: "step", TaskID: "attempt", Status: "running", Validity: "effective", LeaseToken: "old-handle", LeaseExpiresAt: func() *time.Time { v := time.Now().Add(time.Hour); return &v }()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DB.Create(&orm.WorkflowReviewCheckpoint{ID: "review", SessionID: "run", AttemptID: "previous", Status: "pending", SlotsJSON: "[]", ManifestJSON: `{"items":[],"orders":{}}`}).Error; err != nil {
		t.Fatal(err)
	}
	stopped, err := svc.Execute(ctx, "owner", "run", WorkflowControlCommand{CommandID: "stop-1", Kind: "stop"})
	if err != nil {
		t.Fatal(err)
	}
	var row orm.WorkflowSessionStep
	svc.DB.First(&row, "id = ?", "attempt")
	if row.Status != "cancelled" || row.LeaseToken != "" {
		t.Fatalf("old grant survived stop: %+v", row)
	}
	_, err = svc.Execute(ctx, "owner", "run", WorkflowControlCommand{CommandID: "resume-1", Kind: "resume", StateVersion: stopped.Control.StateVersion})
	expectControlCode(t, err, "DELIVERY_PENDING")
	claim, err := svc.ClaimHostAction(ctx, "owner", stopped.Receipt.ActionID, id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.SettleHostAction(ctx, "owner", claim.Action.ID, WorkflowHostReceipt{ConnectorID: id.ConnectorID, Credential: id.Credential, InstanceID: id.InstanceID, DispatchToken: claim.DispatchToken, Status: "accepted", NativeEventSeq: 10})
	if err != nil {
		t.Fatal(err)
	}
	c := currentControl(t, svc.DB)
	resumed, err := svc.Execute(ctx, "owner", "run", WorkflowControlCommand{CommandID: "resume-2", Kind: "resume", StateVersion: c.StateVersion})
	if err != nil || resumed.Control.Continuation != "awaiting_user" {
		t.Fatalf("resume bypassed review: %+v %v", resumed, err)
	}
	_, err = svc.HostAction(ctx, "owner", claim.Action.ID, id)
	expectControlCode(t, err, "BINDING_STALE")
}

func TestControlledRetryCreatesOneReplacementForCancelledAttempt(t *testing.T) {
	db, _ := setupBatchTransitionSession(t)
	if err := db.AutoMigrate(&orm.WorkflowReviewCheckpoint{}, &orm.WorkflowHostAction{}, &orm.WorkflowCommand{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Updates(map[string]any{
		"control_protocol": controlpolicy.Protocol, "control_binding_json": `{"required":true,"provider":"deepseek-harness"}`,
		"origin_host": "external-agent", "controller_host": "external-agent", "status": "waiting",
	}).Error; err != nil {
		t.Fatal(err)
	}
	svc := WorkflowControlService{DB: db.DB}
	ctx := context.Background()
	control, err := svc.Bind(ctx, "batch-user", "batch-session", WorkflowHostBindingRequest{ConnectorID: "connector", Credential: strings.Repeat("x", 64), Provider: "deepseek-harness", DriverSessionID: "driver"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSessionStep{ID: "cancelled", SessionID: "batch-session", StepID: "branch_b", TaskID: "cancelled", Attempt: 1, Status: "cancelled", Validity: "effective"}).Error; err != nil {
		t.Fatal(err)
	}
	index := 0
	if err := db.Create(&orm.WorkflowSlotRevision{ID: "old-page", SessionID: "batch-session", SlotID: "pages", Slot: "pages", ProducerAttemptID: "cancelled", StepID: "branch_b", Attempt: 1, ListIndex: &index, Selected: true, Validity: "effective", ContentSnapshot: []byte(`"old page"`)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSlotOrder{SessionID: "batch-session", SlotID: "pages", OrderList: []byte(`[0]`)}).Error; err != nil {
		t.Fatal(err)
	}
	command := WorkflowControlCommand{CommandID: "retry-cancelled", Kind: "retry", StepID: "branch_b", StateVersion: control.StateVersion}
	result, err := svc.Execute(ctx, "batch-user", "batch-session", command)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := svc.Execute(ctx, "batch-user", "batch-session", command)
	if err != nil || replay.Receipt != result.Receipt {
		t.Fatalf("replay diverged: %+v %v", replay, err)
	}
	var rows []orm.WorkflowSessionStep
	db.Where("session_id = ? AND step_id = ?", "batch-session", "branch_b").Order("attempt").Find(&rows)
	if len(rows) != 2 || rows[0].Validity != "stale" || rows[1].ID != result.Receipt.ExecutionID || rows[1].Status != "queued" {
		t.Fatalf("wrong replacement attempts: %+v", rows)
	}
	display, err := LoadDisplaySlots(ctx, db.DB, "batch-session")
	if err != nil || len(display) != 0 {
		t.Fatalf("stale page is visible in the current workbench: %+v %v", display, err)
	}
	var order orm.WorkflowSlotOrder
	db.First(&order, "session_id = ? AND slot_id = ?", "batch-session", "pages")
	if string(order.OrderList) != "[]" {
		t.Fatalf("stale page retained in export order: %s", order.OrderList)
	}

	var action orm.WorkflowHostAction
	db.First(&action, "id = ?", result.Receipt.ActionID)
	if action.ExecutionID != rows[1].ID {
		t.Fatal("host was not given the exact replacement grant")
	}
}

func TestAdmissionKeepsLegacySemanticsAndAllowsGrantedWorkToDrain(t *testing.T) {
	for _, controlled := range []bool{false, true} {
		name := "legacy"
		if controlled {
			name = "controlled"
		}
		t.Run(name, func(t *testing.T) {
			svc, _ := hostControlFixture(t)
			if err := svc.DB.Create(&orm.WorkflowReviewCheckpoint{ID: "pending-review", SessionID: "run", AttemptID: "finished", Status: "pending"}).Error; err != nil {
				t.Fatal(err)
			}
			var session orm.WorkflowSession
			if err := svc.DB.First(&session, "id = ?", "run").Error; err != nil {
				t.Fatal(err)
			}
			if !controlled {
				session.ControlProtocol = ""
				if err := svc.DB.Model(&session).Update("control_protocol", "").Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := svc.DB.Model(&session).Update("controller_host", "external-agent").Error; err != nil {
				t.Fatal(err)
			}
			if err := svc.DB.Create(&orm.WorkflowSessionStep{ID: "granted", TaskID: "granted", SessionID: "run", StepID: "draft", Status: "queued", Validity: "effective"}).Error; err != nil {
				t.Fatal(err)
			}
			err := controlstore.GuardBegin(svc.DB, session)
			if controlled {
				expectControlCode(t, err, "WORKFLOW_ADMISSION_DENIED")
			} else if err != nil {
				t.Fatalf("controlled review rules affected legacy admission: %v", err)
			}
			if err := controlstore.GuardClaim(svc.DB, session); err != nil {
				t.Fatalf("existing grant cannot drain: %v", err)
			}
			if _, err := workflowattempt.New(svc.DB, workflowattempt.Config{}).ClaimQueuedAttemptForHost(context.Background(), "granted", "executor", "external-agent"); err != nil {
				t.Fatalf("issued grant could not be claimed: %v", err)
			}
			session.Status = "stopped"
			expectControlCode(t, controlstore.GuardBegin(svc.DB, session), "SESSION_STOPPED")
			expectControlCode(t, controlstore.GuardClaim(svc.DB, session), "SESSION_STOPPED")
		})
	}
}

func TestNativeChatStopUsesControlledLifecycle(t *testing.T) {
	svc, _ := hostControlFixture(t)
	if err := svc.DB.Create(&orm.WorkflowSessionStep{ID: "active-attempt", TaskID: "active-attempt", SessionID: "run", Status: "running", Validity: "effective", LeaseToken: "old-handle"}).Error; err != nil {
		t.Fatal(err)
	}
	var session orm.WorkflowSession
	if err := svc.DB.First(&session, "id = ?", "run").Error; err != nil {
		t.Fatal(err)
	}
	stopWorkflowSession(context.Background(), svc.DB, nil, &session)
	control := currentControl(t, svc.DB)
	if control.Continuation != "stopped" || control.Delivery == nil || control.Delivery.Kind != "cancel" {
		t.Fatalf("chat stop did not request controlled cancellation: %+v", control)
	}
	var attempt orm.WorkflowSessionStep
	if err := svc.DB.First(&attempt, "id = ?", "active-attempt").Error; err != nil || attempt.Status != "cancelled" || attempt.LeaseToken != "" {
		t.Fatalf("chat stop did not fence the attempt: %+v %v", attempt, err)
	}
}

func TestStopFencesNativeExecutorAndUpdatesOriginalTask(t *testing.T) {
	svc, _ := hostControlFixture(t)
	expires := time.Now().Add(time.Minute)
	if err := svc.DB.Create(&orm.SubAgentTask{ID: "native-task", Status: "running", InputSlots: json.RawMessage(`[]`), OutputSlots: json.RawMessage(`[]`)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DB.Create(&orm.WorkflowSessionStep{ID: "native-attempt", SessionID: "run", StepID: "write", TaskID: "native-task", ExecutorHost: "lazymind", Status: "running", LeaseToken: "old-handle", LeaseExpiresAt: &expires}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Execute(context.Background(), "owner", "run", WorkflowControlCommand{CommandID: "stop-native", Kind: "stop"}); err != nil {
		t.Fatal(err)
	}
	var task orm.SubAgentTask
	if err := svc.DB.First(&task, "id = ?", "native-task").Error; err != nil || task.Status != "interrupted" {
		t.Fatalf("original task still running: %+v %v", task, err)
	}
	if err := workflowattempt.New(svc.DB, workflowattempt.Config{}).ValidateLease(context.Background(), "native-attempt", "old-handle"); err == nil {
		t.Fatal("stopped native executor retained its lease")
	}
}

func TestPanelContinueConsumesDeliveredNativeResult(t *testing.T) {
	svc, _ := hostControlFixture(t)
	var session orm.WorkflowSession
	if err := svc.DB.First(&session, "id = ?", "run").Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DB.Create(&orm.WorkflowSessionStep{ID: "native-done", SessionID: "run", StepID: "outline", TaskID: "native-task", ExecutorHost: "lazymind", Status: "succeeded"}).Error; err != nil {
		t.Fatal(err)
	}
	previous, err := controlstore.EnqueueHostAction(svc.DB, session, "submit:native-done", "continue", "native-done")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DB.Model(&orm.WorkflowHostAction{}).Where("id = ?", previous).Update("status", "accepted").Error; err != nil {
		t.Fatal(err)
	}
	result, err := svc.Execute(context.Background(), "owner", "run", WorkflowControlCommand{CommandID: "panel-continue", Kind: "continue", StateVersion: session.StateVersion})
	if err != nil || result.Receipt.ActionID == previous {
		t.Fatalf("panel continuation blocked by delivered result: %+v %v", result, err)
	}
	var old orm.WorkflowHostAction
	if err := svc.DB.First(&old, "id = ?", previous).Error; err != nil || old.ConsumedAt == nil {
		t.Fatalf("old notification was not consumed: %+v %v", old, err)
	}
}

func TestControlledRecoveryBindingModes(t *testing.T) {
	for _, kind := range []string{"retry", "rewind"} {
		for _, mode := range []string{"unbound", "bound", "required"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				db, _ := setupBatchTransitionSession(t)
				if err := db.AutoMigrate(&orm.WorkflowReviewCheckpoint{}, &orm.WorkflowHostAction{}, &orm.WorkflowCommand{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{}); err != nil {
					t.Fatal(err)
				}
				binding := `{}`
				if mode == "required" {
					binding = `{"required":true}`
				}
				if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Updates(map[string]any{
					"control_protocol": controlpolicy.Protocol, "control_binding_json": binding,
					"origin_host": "external-agent", "controller_host": "external-agent", "status": "waiting",
				}).Error; err != nil {
					t.Fatal(err)
				}
				svc := WorkflowControlService{DB: db.DB}
				ctx := context.Background()
				if mode == "bound" {
					if _, err := svc.Bind(ctx, "batch-user", "batch-session", WorkflowHostBindingRequest{ConnectorID: "connector", Credential: strings.Repeat("x", 64), Provider: "deepseek-harness", DriverSessionID: "driver"}); err != nil {
						t.Fatal(err)
					}
				}
				status := "failed"
				if kind == "rewind" {
					status = "succeeded"
				}
				if err := db.Create(&orm.WorkflowSessionStep{ID: "failed", SessionID: "batch-session", StepID: "branch_b", TaskID: "failed", Attempt: 1, Status: status, Validity: "effective"}).Error; err != nil {
					t.Fatal(err)
				}
				var session orm.WorkflowSession
				if err := db.First(&session, "id = ?", "batch-session").Error; err != nil {
					t.Fatal(err)
				}
				command := WorkflowControlCommand{CommandID: "recover", Kind: kind, StepID: "branch_b", StateVersion: session.StateVersion}
				result, err := svc.Execute(ctx, "batch-user", session.ID, command)
				if mode == "required" {
					expectControlCode(t, err, "BINDING_REQUIRED")
					var count int64
					db.Model(&orm.WorkflowSessionStep{}).Where("session_id = ?", session.ID).Count(&count)
					if count != 1 {
						t.Fatalf("rejected recovery left %d attempts", count)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				var replacement orm.WorkflowSessionStep
				if err := db.First(&replacement, "id = ?", result.Receipt.ExecutionID).Error; err != nil {
					t.Fatal(err)
				}
				if replacement.Status != "queued" {
					t.Fatalf("replacement not queued: %+v", replacement)
				}
				if (result.Receipt.ActionID != "") != (mode == "bound") {
					t.Fatalf("wrong host delivery: %+v", result.Receipt)
				}
				replay, err := svc.Execute(ctx, "batch-user", session.ID, command)
				if err != nil || replay.Receipt != result.Receipt {
					t.Fatalf("replay diverged: %+v %v", replay, err)
				}
			})
		}
	}
}

func TestRewindCancelsOnlyDependentActiveExecutions(t *testing.T) {
	for _, host := range []string{"external-agent"} {
		for _, status := range []string{"queued", "running"} {
			t.Run(host+"/"+status, func(t *testing.T) {
				db, _ := setupBatchTransitionSession(t)
				if err := db.AutoMigrate(&orm.WorkflowReviewCheckpoint{}, &orm.WorkflowHostAction{}, &orm.WorkflowCommand{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{}, &orm.SubAgentTask{}); err != nil {
					t.Fatal(err)
				}
				if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Updates(map[string]any{
					"control_protocol": controlpolicy.Protocol, "control_binding_json": `{"connector_id":"connector","driver_session_id":"driver","generation":1}`, "origin_host": "external-agent", "controller_host": "external-agent", "status": "active",
				}).Error; err != nil {
					t.Fatal(err)
				}
				expires := time.Now().Add(time.Hour)
				for _, row := range []orm.WorkflowSessionStep{
					{ID: "source", SessionID: "batch-session", StepID: "branch_b", TaskID: "source-task", Attempt: 1, Status: "succeeded", Validity: "effective"},
					{ID: "dependent", SessionID: "batch-session", StepID: "branch_c", TaskID: "dependent-task", Attempt: 1, Status: status, Validity: "effective", ExecutorHost: host, LeaseToken: "old-lease", LeaseExpiresAt: &expires},
					{ID: "independent", SessionID: "batch-session", StepID: "blocked_d", TaskID: "independent-task", Attempt: 1, Status: "running", Validity: "effective", ExecutorHost: "external-agent", LeaseToken: "kept-lease", LeaseExpiresAt: &expires},
				} {
					if err := db.Create(&row).Error; err != nil {
						t.Fatal(err)
					}
				}
				if err := db.Create(&orm.WorkflowSlotRevision{ID: "source-output", SessionID: "batch-session", SlotID: "source-material", StepID: "branch_b", Attempt: 1, ProducerAttemptID: "source", Revision: 1, Selected: true, Validity: "effective"}).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&orm.WorkflowAttemptInputBinding{ID: "input-binding", AttemptID: "dependent", MaterialID: "source-material", MaterialRevisionID: "source-output"}).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&orm.WorkflowOutbox{ID: "outbox", SessionID: "batch-session", AttemptID: "dependent", Status: "pending", PayloadJSON: []byte(`{}`)}).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&orm.SubAgentTask{ID: "dependent-task", Status: "running", InputSlots: json.RawMessage(`[]`), OutputSlots: json.RawMessage(`[]`)}).Error; err != nil {
					t.Fatal(err)
				}
				var session orm.WorkflowSession
				if err := db.First(&session, "id = ?", "batch-session").Error; err != nil {
					t.Fatal(err)
				}
				before, err := controlstore.Read(db.DB, session)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(strings.Join(before.AvailableActions, ","), "rewind") {
					t.Fatal("active execution hides rewind capability")
				}
				svc := WorkflowControlService{DB: db.DB}
				command := WorkflowControlCommand{CommandID: "regenerate", Kind: "rewind", StepID: "branch_b", StateVersion: session.StateVersion}
				result, err := svc.Execute(context.Background(), "batch-user", session.ID, command)
				if err != nil {
					t.Fatal(err)
				}
				if result.Receipt.ActionID == "" {
					t.Fatal("missing recovery host delivery")
				}
				var old, independent, replacement orm.WorkflowSessionStep
				db.First(&old, "id = ?", "dependent")
				db.First(&independent, "id = ?", "independent")
				db.First(&replacement, "id = ?", result.Receipt.ExecutionID)
				if old.Validity != "stale" || old.Status != "cancelled" || old.LeaseToken != "" || old.LeaseExpiresAt != nil || old.FencingGeneration != 1 {
					t.Fatalf("dependent execution not fenced: %+v", old)
				}
				if independent.Validity != "effective" || independent.Status != "running" || independent.LeaseToken != "kept-lease" {
					t.Fatalf("unrelated branch changed: %+v", independent)
				}
				if replacement.Status != "queued" || replacement.StepID != "branch_b" || replacement.Attempt != 2 {
					t.Fatalf("replacement: %+v", replacement)
				}
				var outbox orm.WorkflowOutbox
				var task orm.SubAgentTask
				db.First(&outbox, "id = ?", "outbox")
				db.First(&task, "id = ?", "dependent-task")
				if outbox.Status != "cancelled" || task.Status != "interrupted" {
					t.Fatalf("worker cancellation not persisted: %s / %s", outbox.Status, task.Status)
				}
				expectControlCode(t, controlstore.ValidateExecution(db.DB, session, old.ID, "old-lease"), "EXECUTION_FENCED")
				if err := controlstore.ValidateExecution(db.DB, session, independent.ID, "kept-lease"); err != nil {
					t.Fatalf("unrelated writer lost access: %v", err)
				}
				replay, err := svc.Execute(context.Background(), "batch-user", session.ID, command)
				if err != nil || replay.Receipt != result.Receipt {
					t.Fatalf("regeneration replay: %+v %v", replay, err)
				}
			})
		}
	}
}

func TestContinueAfterStopSchedulesExecution(t *testing.T) {
	for _, host := range []string{"external-agent"} {
		for _, running := range []bool{false, true} {
			name := host + "/between-steps"
			if running {
				name = host + "/interrupted"
			}
			t.Run(name, func(t *testing.T) {
				db, _ := setupBatchTransitionSession(t)
				if err := db.AutoMigrate(&orm.WorkflowReviewCheckpoint{}, &orm.WorkflowHostAction{}, &orm.WorkflowCommand{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{}, &orm.SubAgentTask{}); err != nil {
					t.Fatal(err)
				}
				if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Updates(map[string]any{
					"control_protocol": controlpolicy.Protocol, "control_binding_json": `{}`, "controller_host": host, "status": "active",
				}).Error; err != nil {
					t.Fatal(err)
				}
				svc := WorkflowControlService{DB: db.DB}
				ctx := context.Background()
				identity := WorkflowHostIdentity{ConnectorID: "connector", Credential: strings.Repeat("x", 64), InstanceID: "host"}
				if _, err := svc.Bind(ctx, "batch-user", "batch-session", WorkflowHostBindingRequest{ConnectorID: identity.ConnectorID, Credential: identity.Credential, Provider: "deepseek-harness", DriverSessionID: "driver"}); err != nil {
					t.Fatal(err)
				}
				if running {
					expires := time.Now().Add(time.Hour)
					if err := db.Create(&orm.WorkflowSessionStep{ID: "old", SessionID: "batch-session", StepID: "branch_b", TaskID: "old-task", Status: "running", Attempt: 1, Validity: "effective", ExecutorHost: host, LeaseToken: "old-lease", LeaseExpiresAt: &expires}).Error; err != nil {
						t.Fatal(err)
					}
				}
				stopped, err := svc.Execute(ctx, "batch-user", "batch-session", WorkflowControlCommand{CommandID: "stop", Kind: "stop"})
				if err != nil {
					t.Fatal(err)
				}
				claim, err := svc.ClaimHostAction(ctx, "batch-user", stopped.Receipt.ActionID, identity)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := svc.SettleHostAction(ctx, "batch-user", claim.Action.ID, WorkflowHostReceipt{ConnectorID: identity.ConnectorID, Credential: identity.Credential, InstanceID: identity.InstanceID, DispatchToken: claim.DispatchToken, Status: "accepted", NativeEventSeq: 10}); err != nil {
					t.Fatal(err)
				}
				var session orm.WorkflowSession
				db.First(&session, "id = ?", "batch-session")
				command := WorkflowControlCommand{CommandID: "continue-after-stop", Kind: "resume", StateVersion: session.StateVersion}
				resumed, err := svc.Execute(ctx, "batch-user", session.ID, command)
				if err != nil {
					t.Fatal(err)
				}
				if resumed.Receipt.ExecutionID == "" || resumed.Receipt.ActionID == "" {
					t.Fatalf("resume did not schedule work: %+v", resumed.Receipt)
				}
				var replacement orm.WorkflowSessionStep
				if err := db.First(&replacement, "id = ?", resumed.Receipt.ExecutionID).Error; err != nil {
					t.Fatal(err)
				}
				if replacement.Status != "queued" || replacement.Validity != "effective" || replacement.ExecutorHost != host {
					t.Fatalf("invalid replacement: %+v", replacement)
				}
				if err := db.First(&session, "id = ?", "batch-session").Error; err != nil {
					t.Fatal(err)
				}
				if running {
					if replacement.StepID != "branch_b" || replacement.Attempt != 2 {
						t.Fatalf("wrong resumed step: %+v", replacement)
					}
					expectControlCode(t, controlstore.ValidateExecution(db.DB, session, "old", "old-lease"), "EXECUTION_FENCED")
				}
				action, err := svc.ClaimHostAction(ctx, "batch-user", resumed.Receipt.ActionID, identity)
				if err != nil || action.Action.ExecutionID != replacement.ID {
					t.Fatalf("recovery delivery cannot be claimed: %+v %v", action, err)
				}
				replay, err := svc.Execute(ctx, "batch-user", session.ID, command)
				if err != nil || replay.Receipt != resumed.Receipt {
					t.Fatalf("duplicate continue changed execution: %+v %v", replay, err)
				}
			})
		}
	}
}
