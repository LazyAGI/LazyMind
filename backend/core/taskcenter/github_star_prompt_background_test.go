package taskcenter

import (
	"fmt"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func newStarBackgroundFixture(t *testing.T) *starPromptFixture {
	t.Helper()
	f := &starPromptFixture{db: newNotificationTestDB(t), now: time.Date(2026, 11, 6, 3, 0, 0, 0, time.UTC)}
	if err := f.db.AutoMigrate(&orm.UserGitHubStarPrompt{}); err != nil {
		t.Fatal(err)
	}
	f.service = GitHubStarPromptService{DB: f.db.DB, Now: func() time.Time { return f.now },
		Config: GitHubStarPromptConfig{MinSuccessfulTasks: 3, Cooldown: 7 * 24 * time.Hour, MaxPrompts: 3}}
	return f
}

// Persist the same history identities and invoke the production completion
// boundary. Ordinary Work must not need a synthetic TaskRunOutput to qualify.
func completedStarBackground(t *testing.T, f *starPromptFixture, id string, results ...string) orm.TaskCenterTask {
	t.Helper()
	conv := orm.Conversation{ID: "conv-" + id, IsTaskConv: true,
		BaseModel: orm.BaseModel{CreateUserID: "owner", CreateUserName: "owner"}}
	if err := f.db.Create(&conv).Error; err != nil {
		t.Fatal(err)
	}
	task := orm.TaskCenterTask{ID: id, UserID: "owner", ConversationID: conv.ID,
		TaskType: "background_chat", Status: "running", CreatedAt: f.now.Add(-time.Minute),
		ProgressJSON: NewBackgroundExecutionProgress(nil)}
	if err := CreateTask(t.Context(), f.db.DB, &task); err != nil {
		t.Fatal(err)
	}
	parts := make([]BackgroundExecutionPart, len(results))
	for i := range results {
		parts[i] = BackgroundExecutionPart{HistoryID: fmt.Sprintf("history-%s-%d", id, i), RunID: fmt.Sprintf("run-%s-%d", id, i)}
	}
	if err := BindBackgroundExecution(t.Context(), f.db.DB, task.ID, conv.ID, parts); err != nil {
		t.Fatal(err)
	}
	for i, result := range results {
		history := orm.ChatHistory{ID: parts[i].HistoryID, ConversationID: conv.ID, RunID: parts[i].RunID,
			RunStatus: "completed", Result: result, TimeMixin: orm.TimeMixin{CreateTime: f.now.Add(-30 * time.Second), UpdateTime: f.now}}
		var err error
		if len(parts) == 2 {
			err = f.db.Create(&orm.MultiAnswersChatHistory{ID: history.ID, ConversationID: history.ConversationID,
				RunID: history.RunID, RunStatus: history.RunStatus, Result: history.Result, TimeMixin: history.TimeMixin}).Error
		} else {
			err = f.db.Create(&history).Error
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, part := range parts {
		if err := FinalizeBackgroundExecution(t.Context(), f.db.DB, conv.ID, part.HistoryID, part.RunID); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.First(&task, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "succeeded" {
		t.Fatalf("production completion did not succeed: %s", task.Status)
	}
	assertStarBackgroundHasNoSnapshots(t, f)
	return task
}

func assertStarBackgroundHasNoSnapshots(t *testing.T, f *starPromptFixture) {
	t.Helper()
	var count int64
	if err := f.db.Model(&orm.TaskRunOutput{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("ordinary Work unexpectedly acquired output snapshots: count=%d err=%v", count, err)
	}
}

func TestGitHubStarPromptBackgroundCompletionThreshold(t *testing.T) {
	f := newStarBackgroundFixture(t)
	for i := 1; i <= 3; i++ {
		task := completedStarBackground(t, f, fmt.Sprintf("work-%d", i), "<think>plan</think>Final result with a downloadable document")
		decision := f.claim(t, "owner", task.ID)
		if decision.Show != (i == 3) {
			t.Fatalf("real completed Work count=%d show=%v reason=%s", i, decision.Show, decision.Reason)
		}
		if i == 3 && f.claim(t, "owner", task.ID).Show {
			t.Fatal("one completed root granted a second invitation")
		}
	}
	assertStarBackgroundHasNoSnapshots(t, f)
}

func TestGitHubStarPromptBackgroundRejectsUnusableOrUnboundResults(t *testing.T) {
	for _, scenario := range []string{"empty", "process-only", "unfinished-process", "failed-history", "canceled-history", "foreign-run", "foreign-conversation", "unbound", "incomplete-checkpoint"} {
		t.Run(scenario, func(t *testing.T) {
			f := newStarBackgroundFixture(t)
			completedStarBackground(t, f, "first", "first complete result")
			task := completedStarBackground(t, f, "candidate", "complete result")
			var err error
			switch scenario {
			case "empty":
				err = f.db.Model(&orm.ChatHistory{}).Where("id = ?", "history-candidate-0").Update("result", " \n\t ").Error
			case "process-only":
				err = f.db.Model(&orm.ChatHistory{}).Where("id = ?", "history-candidate-0").Update("result", "<think>planning</think><tool_call>working</tool_call><tool_result>intermediate</tool_result>").Error
			case "unfinished-process":
				err = f.db.Model(&orm.ChatHistory{}).Where("id = ?", "history-candidate-0").Update("result", "<think>still planning").Error
			case "failed-history", "canceled-history":
				status := "failed"
				if scenario == "canceled-history" {
					status = "cancelled"
				}
				err = f.db.Model(&orm.ChatHistory{}).Where("id = ?", "history-candidate-0").Update("run_status", status).Error
			case "foreign-run":
				err = f.db.Model(&orm.ChatHistory{}).Where("id = ?", "history-candidate-0").Update("run_id", "different-execution").Error
			case "foreign-conversation":
				err = f.db.Model(&orm.ChatHistory{}).Where("id = ?", "history-candidate-0").Update("conversation_id", "different-conversation").Error
			case "unbound":
				err = f.db.Model(&task).Update("progress_json", NewBackgroundExecutionProgress(nil)).Error
			case "incomplete-checkpoint":
				err = f.db.Model(&task).Update("progress_json", withBackgroundExecution(nil, backgroundExecution{Version: 1,
					Parts: []BackgroundExecutionPart{{HistoryID: "history-candidate-0", RunID: "run-candidate-0"}}})).Error
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := f.db.First(&task, "id = ?", task.ID).Error; err != nil {
				t.Fatal(err)
			}
			if _, ready, err := githubStarRoot(t.Context(), f.db.DB, task); err != nil || ready {
				t.Fatalf("%s supplied an invalid current result: ready=%v err=%v", scenario, ready, err)
			}
			if f.claim(t, "owner", task.ID).Show {
				t.Fatalf("%s incorrectly supplied a third usable result", scenario)
			}
			second := completedStarBackground(t, f, "second", "second complete result")
			if f.claim(t, "owner", second.ID).Show {
				t.Fatalf("%s incorrectly raised the historical success count", scenario)
			}
			valid := completedStarBackground(t, f, "valid-third", "third complete result")
			if !f.claim(t, "owner", valid.ID).Show {
				t.Fatal("valid production-completed roots did not supply the threshold")
			}
			assertStarBackgroundHasNoSnapshots(t, f)
		})
	}
}

func TestGitHubStarPromptBackgroundArtifactOwnership(t *testing.T) {
	for _, scenario := range []string{"current", "foreign-owner", "foreign-history", "foreign-conversation", "before-run", "after-completion"} {
		t.Run(scenario, func(t *testing.T) {
			f := newStarBackgroundFixture(t)
			completedStarBackground(t, f, "first", "first result")
			completedStarBackground(t, f, "second", "second result")
			task := completedStarBackground(t, f, "artifact-only", "")
			artifact := orm.ConversationArtifact{ID: "artifact", ConversationID: task.ConversationID,
				HistoryID: "history-artifact-only-0", CreateUserID: "owner", Filename: "result.md", Slot: "result.md",
				ContentType: "text", Value: []byte(`{"text":"A complete generated document"}`), CreatedAt: f.now.Add(-time.Second)}
			switch scenario {
			case "foreign-owner":
				artifact.CreateUserID = "other"
			case "foreign-history":
				artifact.HistoryID = "another-history"
			case "foreign-conversation":
				artifact.ConversationID = "another-conversation"
			case "before-run":
				artifact.CreatedAt = task.CreatedAt.Add(-time.Second)
			case "after-completion":
				artifact.CreatedAt = f.now.Add(time.Second)
			}
			if err := f.db.Create(&artifact).Error; err != nil {
				t.Fatal(err)
			}
			if got := f.claim(t, "owner", task.ID).Show; got != (scenario == "current") {
				t.Fatalf("artifact=%s show=%v", scenario, got)
			}
			assertStarBackgroundHasNoSnapshots(t, f)
		})
	}
}

func TestGitHubStarPromptBackgroundDualHistory(t *testing.T) {
	for _, scenario := range []string{"both-results", "one-result", "foreign-run", "single-history-shadow"} {
		t.Run(scenario, func(t *testing.T) {
			f := newStarBackgroundFixture(t)
			completedStarBackground(t, f, "first", "first result")
			completedStarBackground(t, f, "second", "second result")
			secondary := "secondary result"
			if scenario == "one-result" {
				secondary = ""
			}
			task := completedStarBackground(t, f, "dual", "primary result", secondary)
			if scenario == "foreign-run" {
				if err := f.db.Model(&orm.MultiAnswersChatHistory{}).Where("id = ?", "history-dual-1").Update("run_id", "another-execution").Error; err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "single-history-shadow" {
				if err := f.db.Model(&orm.MultiAnswersChatHistory{}).Where("conversation_id = ?", task.ConversationID).Update("result", "").Error; err != nil {
					t.Fatal(err)
				}
				if err := f.db.Create(&orm.ChatHistory{ID: "history-dual-0", ConversationID: task.ConversationID,
					RunID: "run-dual-0", RunStatus: "completed", Result: "A shadow copy must not replace authoritative dual results"}).Error; err != nil {
					t.Fatal(err)
				}
			}
			want := scenario == "both-results" || scenario == "one-result"
			if got := f.claim(t, "owner", task.ID).Show; got != want {
				t.Fatalf("dual=%s show=%v want=%v", scenario, got, want)
			}
			assertStarBackgroundHasNoSnapshots(t, f)
		})
	}
}
