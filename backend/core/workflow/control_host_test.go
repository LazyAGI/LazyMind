package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlpolicy"
	"lazymind/core/workflow/controlstore"
)

func hostControlFixture(t *testing.T) (WorkflowControlService, WorkflowHostIdentity) {
	t.Helper()
	db := newTestDB(t).DB
	if err := db.AutoMigrate(&orm.WorkflowReviewCheckpoint{}, &orm.WorkflowHostAction{}, &orm.WorkflowCommand{}, &orm.WorkflowEvent{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSession{ID: "run", CreateUserID: "owner", Status: "active", StateVersion: 1, ControlProtocol: controlpolicy.Protocol, ControlBindingJSON: `{"required":true,"provider":"deepseek-harness"}`}).Error; err != nil {
		t.Fatal(err)
	}
	svc := WorkflowControlService{DB: db}
	identity := WorkflowHostIdentity{ConnectorID: "connector", Credential: strings.Repeat("x", 64), InstanceID: "process-1"}
	_, err := svc.Bind(context.Background(), "owner", "run", WorkflowHostBindingRequest{WorkflowHostIdentity: identity, Provider: "deepseek-harness", DriverSessionID: "driver"})
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
	c, err := svc.Bind(context.Background(), "owner", "run", WorkflowHostBindingRequest{WorkflowHostIdentity: id, Provider: "deepseek-harness", DriverSessionID: "driver"})
	if err != nil || c.Continuation != "awaiting_user" {
		t.Fatalf("binding cleared review: %+v %v", c, err)
	}
	_, err = svc.Bind(context.Background(), "owner", "run", WorkflowHostBindingRequest{WorkflowHostIdentity: id, Provider: "deepseek-harness", DriverSessionID: "other"})
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
	accepted, err := svc.SettleHostAction(ctx, "owner", claim.Action.ID, WorkflowHostReceipt{WorkflowHostIdentity: other, Status: "accepted", NativeEventSeq: 42})
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
	_, err = svc.SettleHostAction(ctx, "owner", claim.Action.ID, WorkflowHostReceipt{WorkflowHostIdentity: id, DispatchToken: claim.DispatchToken, Status: "accepted", NativeEventSeq: 10})
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
	control, err := svc.Bind(ctx, "batch-user", "batch-session", WorkflowHostBindingRequest{WorkflowHostIdentity: WorkflowHostIdentity{ConnectorID: "connector", Credential: strings.Repeat("x", 64)}, Provider: "deepseek-harness", DriverSessionID: "driver"})
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
