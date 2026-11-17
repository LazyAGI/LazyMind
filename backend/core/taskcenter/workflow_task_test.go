package taskcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/workflow/graphengine"
)

func TestHistoricalBackgroundTaskResolvesWorkflowStepsAndStatus(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.TaskCenterTask{}, &orm.Conversation{}, &orm.UserSchedule{},
		&orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.SubAgentTask{}, &orm.SubAgentArtifact{})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC().Add(-time.Minute)
	finished := now.Add(30 * time.Second)
	conversation := orm.Conversation{ID: "conv", BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}
	task := orm.TaskCenterTask{ID: "background", UserID: "owner", ConversationID: "conv", TaskType: "background_chat",
		Status: "succeeded", CreatedAt: now, UpdatedAt: finished, FinishedAt: &finished}
	session := orm.WorkflowSession{ID: "writer", ConversationID: "conv", CreateUserID: "owner", WorkflowID: "writer",
		Status: "waiting", CreatedAt: now.Add(10 * time.Second), UpdatedAt: now.Add(20 * time.Second)}
	step := orm.WorkflowSessionStep{ID: "prepare-attempt", SessionID: "writer", StepID: "prepare", TaskID: "prepare-task",
		Status: "succeeded", CreatedAt: now, UpdatedAt: now}
	for _, value := range []any{&conversation, &task, &session, &step} {
		if err := db.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/task-center/tasks?status=failed", "/task-center/workbench", "/task-center/tasks/background"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-User-Id", "owner")
		rec := httptest.NewRecorder()
		var got taskResponse
		if path == "/task-center/tasks?status=failed" {
			ListTasks(rec, req)
			var response struct {
				Items  []taskResponse `json:"items"`
				Counts map[string]int `json:"status_counts"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Items) != 1 || response.Counts["failed"] != 1 {
				t.Fatalf("non-approval waiting must be failed: %s", rec.Body.String())
			}
			got = response.Items[0]
		} else if path == "/task-center/workbench" {
			Workbench(rec, req)
			var response struct {
				Groups map[string]*workbenchGroup `json:"groups"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			group := response.Groups["failed"]
			if group == nil || group.Total != 1 || len(group.Items) != 1 {
				t.Fatalf("workbench must use live workflow state: %s", rec.Body.String())
			}
			got = group.Items[0]
		} else {
			GetTaskByID(rec, req)
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
		}
		if rec.Code != http.StatusOK || got.WorkflowSessionID == nil || *got.WorkflowSessionID != "writer" ||
			got.Status != "failed" || got.FinishedAt == nil || len(got.Steps) != 1 || got.Steps[0].StepID != "prepare" {
			t.Fatalf("%s: incorrect task detail: %#v", path, got)
		}
	}
	var stored orm.TaskCenterTask
	if err := db.First(&stored, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.WorkflowSessionID != nil || stored.Status != "succeeded" {
		t.Fatal("read-time compatibility must not rewrite historical tasks")
	}
}

func TestHistoricalWorkflowMatchingDoesNotStealOtherRuns(t *testing.T) {
	for _, scenario := range []string{"before", "after", "other_owner", "next_task", "already_linked", "ambiguous", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.TaskCenterTask{}, &orm.WorkflowSession{})
			now := time.Now().UTC().Add(-time.Minute)
			finished := now.Add(30 * time.Second)
			task := orm.TaskCenterTask{ID: "background", UserID: "owner", ConversationID: "conv", TaskType: "background_chat",
				Status: "succeeded", CreatedAt: now, UpdatedAt: finished, FinishedAt: &finished}
			session := orm.WorkflowSession{ID: "writer", ConversationID: "conv", CreateUserID: "owner", WorkflowID: "writer",
				Status: "waiting", CreatedAt: now.Add(10 * time.Second), UpdatedAt: now}
			switch scenario {
			case "before":
				session.CreatedAt = now.Add(-time.Second)
			case "after":
				session.CreatedAt = finished.Add(time.Second)
			case "other_owner":
				session.CreateUserID = "someone-else"
			case "canceled":
				task.Status = "canceled"
			case "next_task":
				task.FinishedAt = nil
				other := task
				other.ID = "next"
				other.CreatedAt = now.Add(time.Second)
				if err := db.Create(&other).Error; err != nil {
					t.Fatal(err)
				}
			case "already_linked":
				other := task
				other.ID = "linked"
				other.WorkflowSessionID = &session.ID
				other.CreatedAt = session.CreatedAt
				if err := db.Create(&other).Error; err != nil {
					t.Fatal(err)
				}
			case "ambiguous":
				other := session
				other.ID = "second-workflow"
				if err := db.Create(&other).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&session).Error; err != nil {
				t.Fatal(err)
			}
			got, err := resolveTaskForResponse(context.Background(), db.DB, task)
			if err != nil {
				t.Fatal(err)
			}
			if got.WorkflowSessionID != nil || got.Status != task.Status {
				t.Fatalf("incorrect association in %s: %#v", scenario, got)
			}
		})
	}
}

func TestLinkedTaskUsesWorkflowLifecycleWithoutRevivingCanceledTasks(t *testing.T) {
	for _, sample := range []struct{ task, workflow, want string }{
		{"succeeded", "waiting", "running"}, {"succeeded", "active", "running"},
		{"running", "completed", "succeeded"}, {"running", "failed", "failed"},
		{"running", "stopped", "paused"}, {"canceled", "active", "canceled"},
	} {
		t.Run(sample.task+"_"+sample.workflow, func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.TaskCenterTask{}, &orm.WorkflowSession{}, &orm.WorkflowSessionStep{})
			now := time.Now().UTC()
			session := orm.WorkflowSession{ID: "writer", ConversationID: "conv", CreateUserID: "owner", WorkflowID: "writer",
				Status: sample.workflow, CreatedAt: now.Add(-time.Minute), UpdatedAt: now}
			if err := db.Create(&session).Error; err != nil {
				t.Fatal(err)
			}
			task := orm.TaskCenterTask{ID: "background", UserID: "owner", ConversationID: "conv", TaskType: "background_chat",
				WorkflowSessionID: &session.ID, Status: sample.task, CreatedAt: session.CreatedAt, UpdatedAt: now, FinishedAt: &now}
			got, err := resolveTaskForResponse(t.Context(), db.DB, task)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != sample.want {
				t.Fatalf("status=%q want=%q", got.Status, sample.want)
			}
			if !isTerminal(got.Status) && got.FinishedAt != nil {
				t.Fatal("unfinished workflow must not have a finish time")
			}
		})
	}
}

func TestLinkedTaskUsesDurableSessionStatusWhenHistoricalRevisionWasRemoved(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.TaskCenterTask{}, &orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.WorkflowRevision{})
	now := time.Now().UTC()
	session := orm.WorkflowSession{
		ID: "historical-session", ConversationID: "conv", CreateUserID: "owner", WorkflowID: "removed-workflow",
		WorkflowRevisionID: "removed-revision", Status: "waiting", CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
	}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSessionStep{
		ID: "completed-step", SessionID: session.ID, StepID: "review", TaskID: "task", Attempt: 1,
		Status: "succeeded", Validity: "effective", CreatedAt: now.Add(-time.Second), UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	task := orm.TaskCenterTask{
		ID: "historical-task", UserID: "owner", ConversationID: "conv", TaskType: "workflow_run",
		WorkflowSessionID: &session.ID, Status: "running", CreatedAt: session.CreatedAt, UpdatedAt: session.CreatedAt,
	}
	got, err := resolveTaskForResponse(t.Context(), db.DB, task)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "waiting" {
		t.Fatalf("status=%q want waiting", got.Status)
	}
}

func TestStoppedWorkflowIsPausedUntilExplicitRetry(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.TaskCenterTask{}, &orm.Conversation{}, &orm.UserSchedule{},
		&orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.SubAgentTask{}, &orm.SubAgentArtifact{})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC().Add(-time.Minute)
	session := orm.WorkflowSession{ID: "stopped-workflow", ConversationID: "conv", CreateUserID: "owner", WorkflowID: "writer", Status: "waiting", CreatedAt: now, UpdatedAt: now}
	task := orm.TaskCenterTask{ID: "stopped-task", UserID: "owner", ConversationID: "conv", TaskType: "workflow_run", WorkflowSessionID: &session.ID, Status: "running", CreatedAt: now, UpdatedAt: now}
	conversation := orm.Conversation{ID: "conv", BaseModel: orm.BaseModel{CreateUserID: "owner"}}
	stopped := orm.WorkflowSessionStep{ID: "stopped-attempt", SessionID: session.ID, StepID: "write", TaskID: "subtask", Attempt: 1, Status: "interrupted", TerminalCode: "WORKFLOW_STOPPED", CreatedAt: now, UpdatedAt: now}
	for _, value := range []any{&session, &task, &conversation, &stopped} {
		if err := db.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, filter := range []string{"waiting", "paused"} {
		req := httptest.NewRequest(http.MethodGet, "/task-center/tasks?status="+filter, nil)
		req.Header.Set("X-User-Id", "owner")
		rec := httptest.NewRecorder()
		ListTasks(rec, req)
		var response struct {
			Items  []taskResponse `json:"items"`
			Counts map[string]int `json:"status_counts"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusOK || response.Counts["waiting"] != 0 || response.Counts["paused"] != 1 || response.Counts["canceled"] != 0 {
			t.Fatalf("%s: %s", filter, rec.Body.String())
		}
		if filter == "waiting" && len(response.Items) != 0 {
			t.Fatal("stopped workflow leaked into approvals")
		}
		if filter == "paused" && (len(response.Items) != 1 || response.Items[0].FinishedAt != nil) {
			t.Fatal("paused workflow must remain resumable")
		}
	}
	// Read-time status must not permanently cancel the resumable workflow task.
	var persisted orm.TaskCenterTask
	if err := db.First(&persisted, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Status != "running" {
		t.Fatalf("read mutated stored task to %q", persisted.Status)
	}
	// An untouched parallel branch retains the earlier stop after retrying write.
	stoppedBranch := stopped
	stoppedBranch.ID, stoppedBranch.StepID, stoppedBranch.TaskID = "stopped-branch", "outline", "outline-task"
	if err := db.Create(&stoppedBranch).Error; err != nil {
		t.Fatal(err)
	}
	// Explicit retry creates a newer attempt; old cancellation must not mask it.
	retry := stopped
	retry.ID = "retry-attempt"
	retry.TaskID = "retry-task"
	retry.Attempt = 2
	retry.Status = "running"
	retry.TerminalCode = ""
	retry.CreatedAt = now.Add(time.Second)
	if err := db.Create(&retry).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&session).Update("status", "active").Error; err != nil {
		t.Fatal(err)
	}
	if got, err := resolveTaskForResponse(t.Context(), db.DB, task); err != nil || got.Status != "running" {
		t.Fatalf("retry status=%s", got.Status)
	}
	if err := db.Model(&retry).Update("status", "succeeded").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&session).Update("status", "waiting").Error; err != nil {
		t.Fatal(err)
	}
	if got, err := resolveTaskForResponse(t.Context(), db.DB, task); err != nil || got.Status != "failed" {
		t.Fatalf("waiting without an approval checkpoint status=%s error=%v", got.Status, err)
	}
}

func TestRecoverExternalWorkflowTasksPreservesOwnerAndArchivedRecords(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.TaskCenterTask{}, &orm.WorkflowSession{})
	now := time.Now().UTC()
	for _, session := range []orm.WorkflowSession{
		{ID: "visible", CreateUserID: "owner", OriginHost: "external-agent", ControllerHost: "external-agent", IntentContext: `{"text":"四页论文总结\n参数"}`, Status: "completed", CreatedAt: now, UpdatedAt: now},
		{ID: "other", CreateUserID: "other", OriginHost: "external-agent", ControllerHost: "external-agent", CreatedAt: now, UpdatedAt: now},
		{ID: "dismissed", CreateUserID: "owner", OriginHost: "external-agent", ControllerHost: "external-agent", Dismissed: true, CreatedAt: now, UpdatedAt: now},
		{ID: "archived", CreateUserID: "owner", OriginHost: "external-agent", ControllerHost: "external-agent", CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(&session).Error; err != nil {
			t.Fatal(err)
		}
	}
	archived := "archived"
	if err := db.Create(&orm.TaskCenterTask{ID: "old-task", UserID: "owner", WorkflowSessionID: &archived, TaskType: "workflow_run", Status: "succeeded", ArchivedAt: &now, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	// An unrelated conversation-free trigger must not be claimed by recovery.
	if err := db.Create(&orm.TaskCenterTask{ID: "trigger", UserID: "owner", TaskType: "scheduled", Status: "running", CreatedAt: now.Add(-time.Minute), UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := RecoverExternalWorkflowTasks(t.Context(), db.DB, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	var tasks []orm.TaskCenterTask
	if err := db.Find(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 3 {
		t.Fatalf("unexpected task recovery: %#v", tasks)
	}
	var found bool
	for _, task := range tasks {
		if task.ID == "trigger" && task.WorkflowSessionID != nil {
			t.Fatal("unrelated trigger was claimed")
		}
		if task.WorkflowSessionID != nil && *task.WorkflowSessionID == "visible" {
			found = true
			if task.ConversationID != "" || task.Title == nil || *task.Title != "四页论文总结" {
				t.Fatalf("invalid recovered task: %#v", task)
			}
		}
	}
	if !found {
		t.Fatal("historical external run was not recovered")
	}
}

func TestWorkflowInputCheckpointBelongsToAttention(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.TaskCenterTask{}, &orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.WorkflowRevision{}, &orm.WorkflowApprovalPreference{}, &orm.WorkflowReviewCheckpoint{}, &orm.WorkflowSlotRevision{})
	now := time.Now().UTC()
	session := orm.WorkflowSession{ID: "input-session", ConversationID: "conv", CreateUserID: "owner", WorkflowID: "workflow", WorkflowRevisionID: "revision", ControllerHost: "lazymind", Status: "active", CreatedAt: now, UpdatedAt: now}
	graph := graphengine.CompiledStateGraph{Nodes: map[string]graphengine.CompiledNode{"collect": {ID: "collect", Mode: "auto"}}, Runtime: graphengine.RuntimePolicy{InputCheckpoint: &graphengine.InputCheckpoint{AfterStep: "collect", BeforeSteps: []string{"outline"}, Slot: "questions"}}}
	for _, row := range []any{&session, &orm.WorkflowRevision{ID: "revision", CompiledGraph: graph.JSON()}, &orm.WorkflowSessionStep{ID: "attempt", SessionID: session.ID, StepID: "collect", Status: "succeeded", Validity: "effective", CreatedAt: now}, &orm.WorkflowSlotRevision{ID: "slot", SessionID: session.ID, SlotID: "questions", Slot: "questions", Selected: true, Validity: "effective", ContentSnapshot: json.RawMessage(`{"checkpoint_token":"card","questions":[{"text":"Date?"}]}`)}} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	task := orm.TaskCenterTask{ID: "task", UserID: "owner", ConversationID: "conv", WorkflowSessionID: &session.ID, Status: "running", TaskType: "workflow_run", CreatedAt: now}
	got, err := resolveTaskForResponse(context.Background(), db.DB, task)
	if err != nil || got.Status != "waiting_inputs" || !taskMatchesStatusFilter(got.Status, "attention") || workbenchCategory(got, now) != "attention" {
		t.Fatalf("input task must belong to attention: %+v err=%v", got, err)
	}
}
