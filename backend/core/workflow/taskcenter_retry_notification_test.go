package workflow

import (
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/taskcenter"
	"lazymind/core/workflow/graphengine"
)

func TestHostedRetryReopensTrackedTaskForSuccessNotification(t *testing.T) {
	for _, outcome := range []string{"succeeded", "failed"} {
		t.Run(outcome, func(t *testing.T) { testHostedRetryCompletionNotification(t, outcome) })
	}
}

func testHostedRetryCompletionNotification(t *testing.T, outcome string) {
	t.Helper()
	db := orm.MigrateAllModelsForTest(t).DB
	if err := db.AutoMigrate(&orm.WorkflowInputBinding{}, &orm.WorkflowApprovalPreference{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	graph := graphengine.CompiledStateGraph{
		SchemaVersion: graphengine.SchemaVersion, GraphHash: "retry-graph", StartRoute: "all",
		Nodes: map[string]graphengine.CompiledNode{"write": {ID: "write"}},
		ControlEdges: []graphengine.CompiledEdge{
			{ID: "start-write", From: "__start__", To: "write"},
			{ID: "write-end", From: "write", To: "__end__"},
		},
		StaticOrder: []string{"write"},
	}
	if err := db.Create(&orm.WorkflowRevision{ID: "retry-revision", WorkflowResourceID: "resource", RevisionNo: 1,
		GraphHash: graph.GraphHash, GraphSchemaVersion: graph.SchemaVersion, CompiledGraph: graph.JSON()}).Error; err != nil {
		t.Fatal(err)
	}
	session := orm.WorkflowSession{ID: "retry-session", ConversationID: "conversation", WorkflowID: "writer",
		WorkflowRevisionID: "retry-revision", GraphHash: graph.GraphHash, GraphSchemaVersion: graph.SchemaVersion,
		OriginHost: "external-agent", ControllerHost: "external-agent", CreateUserID: "owner",
		Status: "active", StateVersion: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSessionStep{ID: "failed-attempt", SessionID: session.ID, StepID: "write",
		TaskID: "failed-attempt", Attempt: 1, Status: "failed", Validity: "effective", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	tasks := []orm.TaskCenterTask{
		{ID: "tracked", UserID: "owner", ConversationID: session.ConversationID, TaskType: "workflow_run", WorkflowSessionID: &session.ID, Status: "running", CreatedAt: now, UpdatedAt: now},
		{ID: "canceled", UserID: "owner", ConversationID: session.ConversationID, TaskType: "workflow_run", WorkflowSessionID: &session.ID, Status: "canceled", FinishedAt: &now, CreatedAt: now, UpdatedAt: now},
		{ID: "archived", UserID: "owner", ConversationID: session.ConversationID, TaskType: "workflow_run", WorkflowSessionID: &session.ID, Status: "failed", FinishedAt: &now, ArchivedAt: &now, CreatedAt: now, UpdatedAt: now},
		{ID: "other-owner", UserID: "other", ConversationID: session.ConversationID, TaskType: "workflow_run", WorkflowSessionID: &session.ID, Status: "failed", FinishedAt: &now, CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if err := FinalizeHostAttempt(t.Context(), db, session.ID, "write", "failed-attempt", "failed"); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&session, "id = ?", session.ID).Error; err != nil {
		t.Fatal(err)
	}
	request := transitionCommandRequest{CommandID: "retry-command", Operation: "retry", RetryOrigin: "user", TargetStepID: "write",
		TaskID: "replacement-attempt", ExpectedStateVersion: session.StateVersion, GraphHash: graph.GraphHash}
	var response transitionCommandResponse
	if err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		response, _, _, err = applyWorkflowTransition(t.Context(), tx, session.ID, request)
		return err
	}); err != nil || !response.Accepted {
		t.Fatalf("retry was not accepted: response=%#v err=%v", response, err)
	}
	if err := db.First(&session, "id = ?", session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if session.Status != SessionStatusActive {
		t.Fatalf("accepted hosted retry retained terminal session status: %s", session.Status)
	}
	if _, err := taskcenter.ReconcileOrdinaryNotifications(t.Context(), db, ""); err != nil {
		t.Fatal(err)
	}
	var tracked orm.TaskCenterTask
	if err := db.First(&tracked, "id = ?", "tracked").Error; err != nil {
		t.Fatal(err)
	}
	if tracked.Status != "running" || tracked.FinishedAt != nil {
		t.Fatalf("accepted hosted retry did not reopen its tracked task: %#v", tracked)
	}
	for _, id := range []string{"canceled", "archived", "other-owner"} {
		var task orm.TaskCenterTask
		if err := db.First(&task, "id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
		want := "failed"
		if id == "canceled" {
			want = "canceled"
		}
		if task.Status != want || task.FinishedAt == nil {
			t.Fatalf("retry reopened protected task %s: %#v", id, task)
		}
	}
	if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "replacement-attempt").Update("status", outcome).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := FinalizeHostAttempt(t.Context(), db, session.ID, "write", "replacement-attempt", outcome); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.First(&tracked, "id = ?", tracked.ID).Error; err != nil {
		t.Fatal(err)
	}
	if tracked.Status != outcome || tracked.FinishedAt == nil {
		t.Fatalf("retried hosted completion was not persisted: %#v", tracked)
	}
	var notices []orm.TaskNotification
	if err := db.Order("created_at,id").Find(&notices).Error; err != nil {
		t.Fatal(err)
	}
	if len(notices) != 2 || notices[0].TaskID != tracked.ID || notices[0].Event != "failed" ||
		notices[1].TaskID != tracked.ID || notices[1].Event != outcome || notices[1].Channel != "desktop" {
		t.Fatalf("retry completion notices=%#v, want initial failure and replacement %s desktop notices", notices, outcome)
	}
}
