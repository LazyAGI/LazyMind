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
	for _, path := range []string{"/task-center/tasks?status=waiting", "/task-center/tasks/background"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-User-Id", "owner")
		rec := httptest.NewRecorder()
		var got taskResponse
		if path == "/task-center/tasks?status=waiting" {
			ListTasks(rec, req)
			var response struct {
				Items  []taskResponse `json:"items"`
				Counts map[string]int `json:"status_counts"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Items) != 1 || response.Counts["waiting"] != 1 {
				t.Fatalf("missing waiting workflow: %s", rec.Body.String())
			}
			got = response.Items[0]
		} else {
			GetTaskByID(rec, req)
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
		}
		if rec.Code != http.StatusOK || got.WorkflowSessionID == nil || *got.WorkflowSessionID != "writer" ||
			got.Status != "waiting" || got.FinishedAt != nil || len(got.Steps) != 1 || got.Steps[0].StepID != "prepare" {
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
			got := resolveTaskForResponse(context.Background(), db.DB, task)
			if got.WorkflowSessionID != nil || got.Status != task.Status {
				t.Fatalf("incorrect association in %s: %#v", scenario, got)
			}
		})
	}
}

func TestLinkedTaskUsesWorkflowLifecycleWithoutRevivingCanceledTasks(t *testing.T) {
	for _, sample := range []struct{ task, workflow, want string }{
		{"succeeded", "waiting", "waiting"}, {"succeeded", "active", "running"},
		{"running", "completed", "succeeded"}, {"running", "failed", "failed"},
		{"running", "stopped", "canceled"}, {"canceled", "active", "canceled"},
	} {
		t.Run(sample.task+"_"+sample.workflow, func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.TaskCenterTask{}, &orm.WorkflowSession{})
			now := time.Now().UTC()
			session := orm.WorkflowSession{ID: "writer", ConversationID: "conv", CreateUserID: "owner", WorkflowID: "writer",
				Status: sample.workflow, CreatedAt: now.Add(-time.Minute), UpdatedAt: now}
			if err := db.Create(&session).Error; err != nil {
				t.Fatal(err)
			}
			task := orm.TaskCenterTask{ID: "background", UserID: "owner", ConversationID: "conv", TaskType: "background_chat",
				WorkflowSessionID: &session.ID, Status: sample.task, CreatedAt: session.CreatedAt, UpdatedAt: now, FinishedAt: &now}
			got := resolveTaskForResponse(t.Context(), db.DB, task)
			if got.Status != sample.want {
				t.Fatalf("status=%q want=%q", got.Status, sample.want)
			}
			if !isTerminal(got.Status) && got.FinishedAt != nil {
				t.Fatal("unfinished workflow must not have a finish time")
			}
		})
	}
}

func TestStoppedWorkflowIsCanceledUntilExplicitRetry(t *testing.T) {
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
	for _, filter := range []string{"waiting", "canceled"} {
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
		if rec.Code != http.StatusOK || response.Counts["waiting"] != 0 || response.Counts["canceled"] != 1 {
			t.Fatalf("%s: %s", filter, rec.Body.String())
		}
		if filter == "waiting" && len(response.Items) != 0 {
			t.Fatal("stopped workflow leaked into approvals")
		}
		if filter == "canceled" && (len(response.Items) != 1 || response.Items[0].FinishedAt == nil) {
			t.Fatal("stopped workflow missing canceled result")
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
	if got := resolveTaskForResponse(t.Context(), db.DB, task); got.Status != "running" {
		t.Fatalf("retry status=%s", got.Status)
	}
	if err := db.Model(&retry).Update("status", "succeeded").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&session).Update("status", "waiting").Error; err != nil {
		t.Fatal(err)
	}
	if got := resolveTaskForResponse(t.Context(), db.DB, task); got.Status != "waiting" {
		t.Fatalf("new approval status=%s", got.Status)
	}
}
