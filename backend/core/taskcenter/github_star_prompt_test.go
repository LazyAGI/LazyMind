package taskcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type starPromptFixture struct {
	db      *orm.DB
	now     time.Time
	service GitHubStarPromptService
}

func newStarPromptFixture(t *testing.T) *starPromptFixture {
	t.Helper()
	f := &starPromptFixture{db: orm.MigrateTestDB(t,
		&orm.UserGitHubStarPrompt{}, &orm.TaskCenterTask{}, &orm.Conversation{},
		&orm.TaskRunOutput{}, &orm.WorkflowSession{}, &orm.WorkflowSessionStep{},
	), now: time.Date(2026, 11, 4, 2, 0, 0, 0, time.UTC)}
	f.service = GitHubStarPromptService{DB: f.db.DB, Now: func() time.Time { return f.now },
		Config: GitHubStarPromptConfig{MinSuccessfulTasks: 3, Cooldown: 7 * 24 * time.Hour, MaxPrompts: 3}}
	return f
}

func (f *starPromptFixture) task(t *testing.T, id, owner, status string, resultReady bool) orm.TaskCenterTask {
	t.Helper()
	conv := orm.Conversation{ID: "conv-" + id, ChannelID: "default", IsTaskConv: true,
		BaseModel: orm.BaseModel{CreateUserID: owner, CreateUserName: owner, CreatedAt: f.now, UpdatedAt: f.now}}
	if err := f.db.Create(&conv).Error; err != nil {
		t.Fatal(err)
	}
	finished := f.now
	task := orm.TaskCenterTask{ID: id, UserID: owner, ConversationID: conv.ID, TaskType: "background_chat",
		Status: status, CreatedAt: f.now.Add(-time.Minute), UpdatedAt: f.now, FinishedAt: &finished}
	if err := f.db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	if resultReady {
		output := orm.TaskRunOutput{ID: "out-" + id, TaskID: id, ConversationID: conv.ID,
			FinalAnswerText: "A complete final result", ArtifactManifestJSON: []byte("[]"),
			OutputStatus: "ready", ContentHash: "fixture-hash", CreatedAt: f.now, UpdatedAt: f.now}
		if err := f.db.Create(&output).Error; err != nil {
			t.Fatal(err)
		}
	}
	return task
}

func (f *starPromptFixture) successes(t *testing.T, owner string, count int) {
	t.Helper()
	for i := 1; i <= count; i++ {
		f.task(t, fmt.Sprintf("%s-%d", owner, i), owner, "succeeded", true)
	}
}

func (f *starPromptFixture) claim(t *testing.T, owner, id string) GitHubStarPromptDecision {
	t.Helper()
	result, err := f.service.Claim(context.Background(), owner, id)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	return result
}

func (f *starPromptFixture) state(t *testing.T, owner string) orm.UserGitHubStarPrompt {
	t.Helper()
	var row orm.UserGitHubStarPrompt
	if err := f.db.Where("user_id = ?", owner).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row
}

func TestGitHubStarPromptFixtureResolvesCurrentWorkflowState(t *testing.T) {
	f := newStarPromptFixture(t)
	task := f.task(t, "stale", "owner", "succeeded", true)
	session := orm.WorkflowSession{ID: "session", ConversationID: task.ConversationID, CreateUserID: "owner",
		WorkflowID: "writer", Status: "active", CreatedAt: f.now.Add(-time.Minute), UpdatedAt: f.now}
	if err := f.db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	task.WorkflowSessionID = &session.ID
	resolved, err := resolveTaskForResponse(t.Context(), f.db.DB, task)
	if err != nil || resolved.Status == "succeeded" {
		t.Fatalf("fixture must expose live workflow reopening: status=%s err=%v", resolved.Status, err)
	}
}

func TestGitHubStarPromptThreshold(t *testing.T) {
	for _, count := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			f := newStarPromptFixture(t)
			f.successes(t, "owner", count)
			got := f.claim(t, "owner", fmt.Sprintf("owner-%d", count))
			if got.Show != (count >= 3) {
				t.Fatalf("successes=%d show=%v", count, got.Show)
			}
		})
	}
}

func TestGitHubStarPromptDoesNotCountOtherUsers(t *testing.T) {
	f := newStarPromptFixture(t)
	f.successes(t, "owner", 2)
	f.successes(t, "other", 5)
	if f.claim(t, "owner", "owner-2").Show {
		t.Fatal("another user's successful tasks contributed to this user's threshold")
	}
}

func TestGitHubStarPromptOnlySuccessfulCompleteRootTasksQualify(t *testing.T) {
	for _, status := range []string{"pending", "running", "waiting", "waiting_inputs", "paused", "failed", "canceled", "skipped"} {
		t.Run(status, func(t *testing.T) {
			f := newStarPromptFixture(t)
			f.successes(t, "owner", 3)
			f.task(t, "candidate", "owner", status, true)
			if f.claim(t, "owner", "candidate").Show {
				t.Fatalf("status=%s incorrectly invited", status)
			}
		})
	}
}

func TestGitHubStarPromptNoFinalResultDoesNotQualify(t *testing.T) {
	f := newStarPromptFixture(t)
	f.successes(t, "owner", 2)
	f.task(t, "empty", "owner", "succeeded", false)
	if f.claim(t, "owner", "empty").Show {
		t.Fatal("a task without a usable final result was treated as complete")
	}
}

func TestGitHubStarPromptIncompleteOrChildRowsDoNotRaiseSuccessCount(t *testing.T) {
	for _, scenario := range []string{"empty", "child"} {
		t.Run(scenario, func(t *testing.T) {
			f := newStarPromptFixture(t)
			f.successes(t, "owner", 1)
			extra := f.task(t, "extra", "owner", "succeeded", scenario != "empty")
			if scenario == "child" {
				if err := f.db.Model(&extra).Update("task_type", "workflow_step").Error; err != nil {
					t.Fatal(err)
				}
			}
			f.task(t, "candidate", "owner", "succeeded", true)
			if f.claim(t, "owner", "candidate").Show {
				t.Fatal("an incomplete or child task was counted as a third successful root")
			}
		})
	}
}

func TestGitHubStarPromptReopenedWorkflowCannotUseStaleSuccess(t *testing.T) {
	f := newStarPromptFixture(t)
	f.successes(t, "owner", 3)
	task := f.task(t, "reopened", "owner", "succeeded", true)
	session := orm.WorkflowSession{ID: "reopened-session", ConversationID: task.ConversationID,
		CreateUserID: "owner", WorkflowID: "writer", Status: "active", CreatedAt: f.now, UpdatedAt: f.now}
	if err := f.db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&task).Update("plugin_session_id", session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if f.claim(t, "owner", task.ID).Show {
		t.Fatal("persisted succeeded hid the authoritative reopened workflow")
	}
}

func TestGitHubStarPromptDuplicateWorkflowAssociationsCountOnce(t *testing.T) {
	f := newStarPromptFixture(t)
	first := f.task(t, "duplicate-a", "owner", "succeeded", true)
	second := f.task(t, "duplicate-b", "owner", "succeeded", true)
	session := orm.WorkflowSession{ID: "shared-session", ConversationID: first.ConversationID,
		CreateUserID: "owner", WorkflowID: "writer", Status: "completed", CreatedAt: f.now, UpdatedAt: f.now}
	if err := f.db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&orm.TaskCenterTask{}).Where("id IN ?", []string{first.ID, second.ID}).Updates(map[string]any{
		"plugin_session_id": session.ID, "conversation_id": first.ConversationID, // workflow-naming: persistence
	}).Error; err != nil {
		t.Fatal(err)
	}
	f.task(t, "second-root", "owner", "succeeded", true)
	if f.claim(t, "owner", "second-root").Show {
		t.Fatal("one workflow was counted twice toward the three-root threshold")
	}
}

func TestGitHubStarPromptCooldownAndClosingConsumeQualification(t *testing.T) {
	f := newStarPromptFixture(t)
	f.successes(t, "owner", 4)
	if !f.claim(t, "owner", "owner-3").Show {
		t.Fatal("initial invitation was suppressed")
	}
	// The UI may close immediately; no follow-up acknowledgement resets the claim.
	f.now = f.now.Add(7*24*time.Hour - time.Millisecond)
	if f.claim(t, "owner", "owner-4").Show {
		t.Fatal("invited before seven full days")
	}
	f.now = f.now.Add(time.Millisecond)
	if !f.claim(t, "owner", "owner-4").Show {
		t.Fatal("did not invite at the seven-day boundary")
	}
}

func TestGitHubStarPromptMaximumAndRestartPersistence(t *testing.T) {
	f := newStarPromptFixture(t)
	f.successes(t, "owner", 6)
	for _, id := range []string{"owner-3", "owner-4", "owner-5"} {
		if !f.claim(t, "owner", id).Show {
			t.Fatal("expected one of the first three invitations")
		}
		f.now = f.now.Add(7 * 24 * time.Hour)
	}
	f.service = GitHubStarPromptService{DB: f.db.DB, Now: func() time.Time { return f.now }, Config: f.service.Config}
	if f.claim(t, "owner", "owner-6").Show {
		t.Fatal("a new service instance lost the three-invitation limit")
	}
	var seen []string
	if err := json.Unmarshal([]byte(f.state(t, "owner").PromptedTaskIDs), &seen); err != nil || len(seen) != 3 {
		t.Fatalf("persisted invitations=%v err=%v", seen, err)
	}
}

func TestGitHubStarPromptSameRootDoesNotInviteAgainAfterCooldown(t *testing.T) {
	f := newStarPromptFixture(t)
	f.successes(t, "owner", 3)
	if !f.claim(t, "owner", "owner-3").Show {
		t.Fatal("expected first invitation")
	}
	f.now = f.now.Add(14 * 24 * time.Hour)
	if f.claim(t, "owner", "owner-3").Show {
		t.Fatal("revisiting the same completed task created another invitation")
	}
}

func TestGitHubStarPromptConcurrentWindowsOnlyOneClaim(t *testing.T) {
	f := newStarPromptFixture(t)
	f.successes(t, "owner", 6)
	start := make(chan struct{})
	results := make(chan GitHubStarPromptDecision, 8)
	errors := make(chan error, 8)
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			<-start
			result, err := f.service.Claim(context.Background(), "owner", fmt.Sprintf("owner-%d", 3+i%4))
			results <- result
			errors <- err
		}(i)
	}
	close(start)
	group.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	shown := 0
	for result := range results {
		if result.Show {
			shown++
		}
	}
	if shown != 1 {
		t.Fatalf("concurrent eligible windows obtained %d invitations, want 1", shown)
	}
}

func TestGitHubStarPromptSupportedIsPersistentAndOwnerScoped(t *testing.T) {
	f := newStarPromptFixture(t)
	f.successes(t, "owner", 3)
	f.successes(t, "other", 3)
	for i := 0; i < 2; i++ {
		if err := f.service.MarkSupported(t.Context(), "owner"); err != nil {
			t.Fatal(err)
		}
	}
	if f.claim(t, "owner", "owner-3").Show {
		t.Fatal("supported user was invited")
	}
	if !f.claim(t, "other", "other-3").Show {
		t.Fatal("one user's support state suppressed another user")
	}
	if !f.state(t, "owner").Supported {
		t.Fatal("support state was not persisted")
	}
}

func TestGitHubStarPromptArchivedOrDeletedWorkDoesNotQualify(t *testing.T) {
	for _, scenario := range []string{"archived", "deleted"} {
		t.Run(scenario, func(t *testing.T) {
			f := newStarPromptFixture(t)
			f.successes(t, "owner", 2)
			task := f.task(t, "hidden", "owner", "succeeded", true)
			if scenario == "archived" {
				if err := f.db.Model(&task).Update("archived_at", f.now).Error; err != nil {
					t.Fatal(err)
				}
			} else if err := f.db.Model(&orm.Conversation{}).Where("id = ?", task.ConversationID).Update("deleted_at", f.now).Error; err != nil {
				t.Fatal(err)
			}
			if f.claim(t, "owner", task.ID).Show {
				t.Fatal("hidden task qualified for an invitation")
			}
		})
	}
}

func TestGitHubStarPromptConfigurationDefaultsAndOverrides(t *testing.T) {
	keys := []string{"LAZYMIND_GITHUB_STAR_MIN_SUCCESSFUL_TASKS", "LAZYMIND_GITHUB_STAR_COOLDOWN_DAYS", "LAZYMIND_GITHUB_STAR_MAX_PROMPTS"}
	for _, key := range keys {
		t.Setenv(key, "")
	}
	got, err := GitHubStarPromptConfigFromEnv()
	if err != nil || got.MinSuccessfulTasks != 3 || got.Cooldown != 7*24*time.Hour || got.MaxPrompts != 3 {
		t.Fatalf("defaults=%+v err=%v", got, err)
	}
	for i, value := range []string{"4", "14", "2"} {
		t.Setenv(keys[i], value)
	}
	got, err = GitHubStarPromptConfigFromEnv()
	if err != nil || got.MinSuccessfulTasks != 4 || got.Cooldown != 14*24*time.Hour || got.MaxPrompts != 2 {
		t.Fatalf("overrides=%+v err=%v", got, err)
	}
}

func TestGitHubStarPromptInvalidConfigurationFailsClosed(t *testing.T) {
	for _, key := range []string{"LAZYMIND_GITHUB_STAR_MIN_SUCCESSFUL_TASKS", "LAZYMIND_GITHUB_STAR_COOLDOWN_DAYS", "LAZYMIND_GITHUB_STAR_MAX_PROMPTS"} {
		for _, value := range []string{"0", "-1", "invalid", "9999999999999999999999"} {
			t.Run(key+"/"+value, func(t *testing.T) {
				t.Setenv(key, value)
				if _, err := GitHubStarPromptConfigFromEnv(); err == nil {
					t.Fatal("invalid configuration silently enabled invitations")
				}
			})
		}
	}
}

func TestGitHubStarPromptHandlersValidateOwnerAndInput(t *testing.T) {
	f := newStarPromptFixture(t)
	f.successes(t, "owner", 3)
	f.task(t, "foreign", "other", "succeeded", true)
	store.Init(f.db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	for _, sample := range []struct {
		owner, body string
		want        int
	}{
		{"", `{"task_id":"owner-3"}`, http.StatusUnauthorized},
		{"owner", `{"task_id":"foreign"}`, http.StatusNotFound},
		{"owner", `{}`, http.StatusBadRequest},
		{"owner", `{"task_id":"owner-3","user_id":"other"}`, http.StatusBadRequest},
	} {
		t.Run(fmt.Sprintf("%d/%s", sample.want, sample.owner), func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/task-center/github-star-prompt/claim", strings.NewReader(sample.body))
			req.Header.Set("X-User-Id", sample.owner)
			rec := httptest.NewRecorder()
			ClaimGitHubStarPrompt(rec, req)
			if rec.Code != sample.want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, sample.want, rec.Body.String())
			}
		})
	}
}

func TestGitHubStarPromptClaimHandlerReturnsConfiguredEligibility(t *testing.T) {
	f := newStarPromptFixture(t)
	f.successes(t, "owner", 3)
	store.Init(f.db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	req := httptest.NewRequest(http.MethodPost, "/task-center/github-star-prompt/claim", strings.NewReader(`{"task_id":"owner-3"}`))
	req.Header.Set("X-User-Id", "owner")
	rec := httptest.NewRecorder()
	ClaimGitHubStarPrompt(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var result struct {
		Show               bool `json:"show"`
		MinSuccessfulTasks int  `json:"min_successful_tasks"`
		CooldownDays       int  `json:"cooldown_days"`
		MaxPrompts         int  `json:"max_prompts"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil || !result.Show || result.MinSuccessfulTasks != 3 || result.CooldownDays != 7 || result.MaxPrompts != 3 {
		t.Fatalf("claim response=%+v err=%v", result, err)
	}
}

func TestGitHubStarPromptSupportHandlerRequiresSelfAndTrue(t *testing.T) {
	f := newStarPromptFixture(t)
	store.Init(f.db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	for _, sample := range []struct {
		owner, body string
		want        int
	}{
		{"", `{"supported":true}`, http.StatusUnauthorized},
		{"owner", `{"supported":false}`, http.StatusBadRequest},
		{"owner", `{"supported":true,"user_id":"other"}`, http.StatusBadRequest},
		{"owner", `{"supported":true}`, http.StatusOK},
	} {
		req := httptest.NewRequest(http.MethodPut, "/user/github-star-prompt/support", strings.NewReader(sample.body))
		req.Header.Set("X-User-Id", sample.owner)
		rec := httptest.NewRecorder()
		SupportGitHubStarPrompt(rec, req)
		if rec.Code != sample.want {
			t.Errorf("body=%s status=%d want=%d", sample.body, rec.Code, sample.want)
		}
	}
}
