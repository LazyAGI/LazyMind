package taskcenter

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func injectedStarWorkflow(t *testing.T, f *starPromptFixture, id string, legacy bool) orm.TaskCenterTask {
	t.Helper()
	task := f.task(t, id, "owner", "succeeded", true)
	marker := map[string]any{
		"bundle_id":   "recorded-" + id,
		"injected_at": f.now.Add(-time.Hour).Format(time.RFC3339Nano),
	}
	if !legacy {
		marker["fingerprint"] = "fixture-bundle-fingerprint"
	}
	ext, err := json.Marshal(map[string]any{"_lazymind_history_injection": marker})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&orm.Conversation{}).Where("id = ?", task.ConversationID).Updates(map[string]any{
		"display_name": "Recorded workflow", "is_task_conv": false, "ext": ext,
	}).Error; err != nil {
		t.Fatal(err)
	}
	task.CreatedAt = f.now.Add(-2 * time.Hour)
	finished := f.now.Add(-90 * time.Minute)
	task.FinishedAt = &finished
	session := orm.WorkflowSession{ID: "session-" + id, ConversationID: task.ConversationID,
		CreateUserID: "owner", WorkflowID: "writer", Status: "completed", CreatedAt: task.CreatedAt, UpdatedAt: finished}
	if err := f.db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	task.TaskType = "workflow_run"
	task.WorkflowSessionID = &session.ID
	if err := f.db.Save(&task).Error; err != nil {
		t.Fatal(err)
	}
	return task
}

func TestGitHubStarPromptInjectedSamplesDoNotRaiseThreshold(t *testing.T) {
	f := newStarPromptFixture(t)
	var sampleIDs []string
	for i := range 5 {
		sample := injectedStarWorkflow(t, f, fmt.Sprintf("sample-%d", i), i == 0)
		sampleIDs = append(sampleIDs, sample.ID)
	}
	for count := 1; count <= 3; count++ {
		task := f.task(t, fmt.Sprintf("real-%d", count), "owner", "succeeded", true)
		decision := f.claim(t, "owner", task.ID)
		if decision.Show != (count == 3) || decision.MinSuccessfulTasks != 3 {
			t.Fatalf("five injected samples plus %d real roots: show=%v threshold=%d", count, decision.Show, decision.MinSuccessfulTasks)
		}
	}
	if got := f.state(t, "owner").PromptedTaskIDs; got != `["task:real-3"]` {
		t.Fatalf("invitation history contains something other than the third real root: %s", got)
	}
	var count int64
	if err := f.db.Model(&orm.TaskCenterTask{}).Where("id IN ?", sampleIDs).Count(&count).Error; err != nil || count != 5 {
		t.Fatalf("sample tasks changed or disappeared: count=%d err=%v", count, err)
	}
	if err := f.db.Model(&orm.TaskRunOutput{}).Where("task_id IN ?", sampleIDs).Count(&count).Error; err != nil || count != 5 {
		t.Fatalf("sample results changed or disappeared: count=%d err=%v", count, err)
	}
}

func TestGitHubStarPromptInjectedSampleCannotTrigger(t *testing.T) {
	for _, recreatedTask := range []bool{false, true} {
		t.Run(fmt.Sprintf("recreated_task_%t", recreatedTask), func(t *testing.T) {
			f := newStarPromptFixture(t)
			f.successes(t, "owner", 3)
			sample := injectedStarWorkflow(t, f, "sample", true)
			if recreatedTask {
				// Recreating a task row does not create a new Workflow execution root.
				sample.CreatedAt = f.now
				if err := f.db.Save(&sample).Error; err != nil {
					t.Fatal(err)
				}
			}
			if f.claim(t, "owner", sample.ID).Show {
				t.Fatal("an imported sample root triggered an invitation")
			}
			if !f.claim(t, "owner", "owner-3").Show {
				t.Fatal("rejecting the sample consumed a real root's invitation or cooldown")
			}
		})
	}
}

func TestGitHubStarPromptSampleTitleAndInvalidMarkerStillCount(t *testing.T) {
	for name, ext := range map[string]json.RawMessage{
		"absent":      nil,
		"unrelated":   json.RawMessage(`{"source":"history-injection"}`),
		"wrong_shape": json.RawMessage(`{"_lazymind_history_injection":"sample"}`),
		"blank_bundle": json.RawMessage(`{"_lazymind_history_injection":{
			"bundle_id":" ","injected_at":"2026-11-04T01:00:00Z"}}`),
		"invalid_time": json.RawMessage(`{"_lazymind_history_injection":{
			"bundle_id":"recorded-example","injected_at":"yesterday"}}`),
	} {
		t.Run(name, func(t *testing.T) {
			f := newStarPromptFixture(t)
			f.successes(t, "owner", 2)
			task := f.task(t, "real-history", "owner", "succeeded", true)
			task.CreatedAt = f.now.Add(-2 * time.Hour)
			if err := f.db.Save(&task).Error; err != nil {
				t.Fatal(err)
			}
			if err := f.db.Model(&orm.Conversation{}).Where("id = ?", task.ConversationID).Updates(map[string]any{
				"display_name": "样例：我创建的真实历史任务", "is_task_conv": false, "ext": ext,
			}).Error; err != nil {
				t.Fatal(err)
			}
			if !f.claim(t, "owner", task.ID).Show {
				t.Fatal("a real historical root was excluded by its title or invalid metadata")
			}
		})
	}
}

func TestGitHubStarPromptNewRootsInSampleConversationStillCount(t *testing.T) {
	for _, newWorkflow := range []bool{false, true} {
		t.Run(fmt.Sprintf("new_workflow_%t", newWorkflow), func(t *testing.T) {
			f := newStarPromptFixture(t)
			f.successes(t, "owner", 2)
			sample := injectedStarWorkflow(t, f, "sample", false)
			task := f.task(t, "continued-real", "owner", "succeeded", true)
			task.ConversationID = sample.ConversationID
			if newWorkflow {
				session := orm.WorkflowSession{ID: "new-workflow", ConversationID: task.ConversationID,
					CreateUserID: "owner", WorkflowID: "writer", Status: "completed",
					CreatedAt: task.CreatedAt, UpdatedAt: f.now}
				if err := f.db.Create(&session).Error; err != nil {
					t.Fatal(err)
				}
				task.TaskType = "workflow_run"
				task.WorkflowSessionID = &session.ID
			}
			if err := f.db.Save(&task).Error; err != nil {
				t.Fatal(err)
			}
			if err := f.db.Model(&orm.TaskRunOutput{}).Where("task_id = ?", task.ID).
				Update("conversation_id", task.ConversationID).Error; err != nil {
				t.Fatal(err)
			}
			if !f.claim(t, "owner", task.ID).Show {
				t.Fatal("a new real execution after injection was excluded with the old sample")
			}
		})
	}
}
