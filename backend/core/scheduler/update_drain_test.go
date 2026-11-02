package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type schedulerFixtureTransport struct{ host string }

func (transport schedulerFixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host != transport.host {
		return nil, errors.New("request escaped scheduler fixture")
	}
	return http.DefaultTransport.RoundTrip(request)
}

// A shutdown admission boundary is process-wide. Each case uses its own test
// process, rather than reopening the production gate or sharing global stores.
func TestSchedulerUpdateDrain(t *testing.T) {
	for _, scenario := range []string{"scheduled", "manual", "dependent", "failure", "reject-new", "draining-rejects-new", "prepare-manual", "prepare-dependency"} {
		t.Run(scenario, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, exe, "-test.run=^TestSchedulerUpdateDrainChild$", "-test.v")
			cmd.Env = append(os.Environ(), "LAZYMIND_UNIT_SCHEDULER_DRAIN="+scenario)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("isolated scheduler case: %v\n%s", err, out)
			}
		})
	}
}

func TestSchedulerUpdateDrainChild(t *testing.T) {
	scenario := os.Getenv("LAZYMIND_UNIT_SCHEDULER_DRAIN")
	if scenario == "" {
		return
	}
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, db.DB, nil)
	now := time.Now().UTC().Truncate(time.Microsecond)
	schedule := orm.UserSchedule{ID: "drain-schedule", UserID: "owner", Name: "Drain fixture", CronExpr: "0 * * * *", Timezone: "UTC", PromptTemplate: "fixture", Enabled: true, NextRunAt: now.Add(-time.Minute)}
	if err := CreateSchedule(t.Context(), db.DB, &schedule); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce, enteredOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ConversationID string `json:"conversation_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		enteredOnce.Do(func() { close(entered) })
		<-release
		if scenario == "failure" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if err := db.Create(&orm.ChatHistory{ID: "drain-history-" + request.ConversationID, Seq: 1, ConversationID: request.ConversationID, Result: "fixture result"}).Error; err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(server.Close)
	defer unblock()
	t.Setenv("LAZYMIND_CORE_SELF_URL", server.URL)
	fixtureURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	// This test runs alone in its child process. Even red/panic cleanup cannot
	// fall back to a real application endpoint when environment cleanup runs.
	http.DefaultClient = &http.Client{Transport: schedulerFixtureTransport{host: fixtureURL.Host}}
	// Registered after Setenv so detached writes finish before URL restoration.
	t.Cleanup(func() {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			var count int64
			if err := db.Model(&orm.TaskCenterTask{}).Where("status = ?", "running").Count(&count).Error; err != nil || count == 0 {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Error("fixture scheduled work did not finish")
	})
	finalizing, finishWrites := make(chan struct{}), make(chan struct{})
	var finalOnce, writeOnce sync.Once
	var armed atomic.Bool
	finish := func() { writeOnce.Do(func() { close(finishWrites) }) }
	defer finish()
	blockFinalWrite := func(tx *gorm.DB) {
		failure := scenario == "failure" || scenario == "prepare-dependency"
		if armed.Load() && ((failure && tx.Statement.Table == "task_center_tasks") || (!failure && tx.Statement.Table == "task_run_outputs")) {
			finalOnce.Do(func() { close(finalizing) })
			<-finishWrites
		}
	}
	if err := db.Callback().Create().Before("gorm:create").Register("unit:hold-output", blockFinalWrite); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Update().Before("gorm:update").Register("unit:hold-outcome", blockFinalWrite); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if scenario == "prepare-manual" || scenario == "prepare-dependency" {
		prepareEntered, prepareRelease := make(chan struct{}), make(chan struct{})
		var prepareOnce, prepareReleaseOnce sync.Once
		releasePrepare := func() { prepareReleaseOnce.Do(func() { close(prepareRelease) }) }
		defer releasePrepare()
		block := func(tx *gorm.DB) {
			if (scenario == "prepare-manual" && tx.Statement.Table == "conversations") || (scenario == "prepare-dependency" && tx.Statement.Table == "task_center_tasks") {
				prepareOnce.Do(func() { close(prepareEntered); <-prepareRelease })
			}
		}
		if err := db.Callback().Create().Before("gorm:create").Register("unit:hold-preparation", func(tx *gorm.DB) {
			if scenario == "prepare-manual" {
				block(tx)
			}
		}); err != nil {
			t.Fatal(err)
		}
		if err := db.Callback().Update().Before("gorm:update").Register("unit:hold-claim", func(tx *gorm.DB) {
			if scenario == "prepare-dependency" {
				block(tx)
			}
		}); err != nil {
			t.Fatal(err)
		}
		preparationDone := make(chan struct{})
		defer func() {
			releasePrepare()
			unblock()
			finish()
			select {
			case <-preparationDone:
			case <-time.After(3 * time.Second):
				t.Error("fixture preparation did not finish")
			}
		}()
		if scenario == "prepare-manual" {
			go func() {
				defer close(preparationDone)
				request := httptest.NewRequest(http.MethodPost, "/schedules/"+schedule.ID+":run-now", nil)
				request.Header.Set("X-User-Id", "owner")
				reply := httptest.NewRecorder()
				RunNowHandler(reply, request)
				if reply.Code != http.StatusOK {
					t.Errorf("already admitted manual preparation was rejected: %d", reply.Code)
				}
			}()
		} else {
			// A malformed dependency is claimed and finalized without HTTP.
			waiting := orm.TaskCenterTask{ID: "preparing-dependency", UserID: "owner", TaskType: "scheduled", Status: "waiting_inputs", DependencyStatus: "waiting", Attempt: 1, CreatedAt: now, UpdatedAt: now}
			if err := db.Create(&waiting).Error; err != nil {
				t.Fatal(err)
			}
			go func() { defer close(preparationDone); resumeWaitingTasks(context.Background(), db.DB) }()
		}
		select {
		case <-prepareEntered:
		case <-time.After(3 * time.Second):
			t.Fatal("preparation did not reach database barrier")
		}
		drained := BeginDrain()
		if drained == nil {
			t.Fatal("BeginDrain returned a nil completion channel")
		}
		done := RunScheduler(ctx, db.DB, "")
		cancel()
		select {
		case <-drained:
			t.Error("drain omitted pre-HTTP preparation")
		case <-time.After(30 * time.Millisecond):
		}
		select {
		case <-done:
			t.Error("Done omitted pre-HTTP preparation")
		default:
		}
		armed.Store(true)
		releasePrepare()
		if scenario == "prepare-manual" {
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("admitted manual task never reached HTTP")
			}
			select {
			case <-drained:
				t.Error("drain lost handoff from preparation to execution")
			default:
			}
		}
		unblock()
		select {
		case <-finalizing:
		case <-time.After(3 * time.Second):
			t.Fatal("prepared task did not reach final database write")
		}
		select {
		case <-drained:
			t.Error("BeginDrain completed before prepared task final write")
		case <-time.After(30 * time.Millisecond):
		}
		select {
		case <-done:
			t.Error("Done completed before prepared task final write")
		default:
		}
		finish()
		select {
		case <-preparationDone:
		case <-time.After(3 * time.Second):
			t.Fatal("preparation did not finish")
		}
		select {
		case <-drained:
		case <-time.After(3 * time.Second):
			t.Fatal("BeginDrain did not complete after prepared task final write")
		}
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("prepared work did not drain")
		}
		var task orm.TaskCenterTask
		if err := db.First(&task).Error; err != nil {
			t.Fatal(err)
		}
		want := "succeeded"
		if scenario == "prepare-dependency" {
			want = "failed"
		}
		if task.Status != want {
			t.Fatalf("Done preceded prepared task finalization: %s", task.Status)
		}
		return
	}
	if scenario == "reject-new" {
		cancel()
		done := RunScheduler(ctx, db.DB, "")
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("idle scheduler did not stop")
		}
		fireSchedules(context.Background(), db.DB, "")
		request := httptest.NewRequest(http.MethodPost, "/schedules/"+schedule.ID+":run-now", nil)
		request.Header.Set("X-User-Id", "owner")
		reply := httptest.NewRecorder()
		RunNowHandler(reply, request)
		if reply.Code != http.StatusServiceUnavailable {
			t.Errorf("new manual run accepted after shutdown: %d", reply.Code)
		}
		var count int64
		if err := db.Model(&orm.Conversation{}).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("new conversations created after shutdown: %d", count)
		}
		var saved orm.UserSchedule
		if err := db.First(&saved, "id = ?", schedule.ID).Error; err != nil {
			t.Fatal(err)
		}
		if saved.RunCount != 0 || !saved.NextRunAt.Equal(schedule.NextRunAt) {
			t.Error("shutdown consumed a scheduled trigger")
		}
		waiting := orm.TaskCenterTask{ID: "drain-waiting", UserID: "owner", TaskType: "scheduled", Status: "waiting_inputs", DependencyStatus: "waiting", Attempt: 1, CreatedAt: now, UpdatedAt: now}
		if err := db.Create(&waiting).Error; err != nil {
			t.Fatal(err)
		}
		resumeWaitingTasks(context.Background(), db.DB)
		if err := db.First(&waiting, "id = ?", waiting.ID).Error; err != nil {
			t.Fatal(err)
		}
		if waiting.Attempt != 1 || waiting.Status != "waiting_inputs" {
			t.Error("shutdown claimed or mutated a waiting dependency")
		}
		// Release any incorrectly launched requests before closing their DB.
		unblock()
		return
	}
	if scenario == "manual" {
		request := httptest.NewRequest(http.MethodPost, "/schedules/"+schedule.ID+":run-now", strings.NewReader(""))
		request.Header.Set("X-User-Id", "owner")
		reply := httptest.NewRecorder()
		RunNowHandler(reply, request)
		if reply.Code != http.StatusOK {
			t.Fatalf("initial manual run failed: %d %s", reply.Code, reply.Body.String())
		}
	} else if scenario == "dependent" {
		start := now.Add(-time.Hour)
		task := orm.TaskCenterTask{ID: "dependent-run", UserID: "owner", TaskType: "scheduled", Status: "waiting_inputs", ScheduleID: &schedule.ID, WindowStart: &start, WindowEnd: &now, DependencyStatus: "checking", Attempt: 3, CreatedAt: now, UpdatedAt: now}
		if err := db.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
		collection := dependencyInputCollection{contextText: "fixture context", inputs: []orm.TaskRunInput{{ID: "fixture-input", DownstreamTaskID: task.ID, UpstreamTaskID: "fixture-upstream", DependencyID: "fixture-dependency", OutputID: "fixture-output", OutputContentHash: "fixture-hash", SnapshotJSON: []byte(`{}`), CreatedAt: now}}}
		if err := launchDependentTask(db.DB, schedule, task, task.Attempt, collection); err != nil {
			t.Fatal(err)
		}
	} else {
		fireOne(context.Background(), db.DB, schedule, now)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("admitted task did not reach chat boundary")
	}
	armed.Store(true)
	drained := BeginDrain()
	if drained == nil {
		t.Fatal("BeginDrain returned a nil completion channel")
	}
	if scenario == "draining-rejects-new" {
		var beforeConversations int64
		if err := db.Model(&orm.Conversation{}).Count(&beforeConversations).Error; err != nil {
			t.Fatal(err)
		}
		var beforeSchedule orm.UserSchedule
		if err := db.First(&beforeSchedule, "id = ?", schedule.ID).Error; err != nil {
			t.Fatal(err)
		}
		// A fresh due trigger must not be consumed while the previous run drains.
		if err := db.Model(&orm.UserSchedule{}).Where("id = ?", schedule.ID).Update("next_run_at", now.Add(-time.Minute)).Error; err != nil {
			t.Fatal(err)
		}
		fireSchedules(context.Background(), db.DB, "")
		request := httptest.NewRequest(http.MethodPost, "/schedules/"+schedule.ID+":run-now", nil)
		request.Header.Set("X-User-Id", "owner")
		reply := httptest.NewRecorder()
		RunNowHandler(reply, request)
		if reply.Code != http.StatusServiceUnavailable {
			t.Errorf("new manual work admitted during drain: %d", reply.Code)
		}
		waiting := orm.TaskCenterTask{ID: "draining-waiting", UserID: "owner", TaskType: "scheduled", Status: "waiting_inputs", DependencyStatus: "waiting", Attempt: 1, CreatedAt: now, UpdatedAt: now}
		if err := db.Create(&waiting).Error; err != nil {
			t.Fatal(err)
		}
		resumeWaitingTasks(context.Background(), db.DB)
		if err := db.First(&waiting, "id = ?", waiting.ID).Error; err != nil {
			t.Fatal(err)
		}
		if waiting.Attempt != 1 || waiting.Status != "waiting_inputs" {
			t.Error("dependency was claimed during drain")
		}
		var afterConversations int64
		if err := db.Model(&orm.Conversation{}).Count(&afterConversations).Error; err != nil {
			t.Fatal(err)
		}
		var afterSchedule orm.UserSchedule
		if err := db.First(&afterSchedule, "id = ?", schedule.ID).Error; err != nil {
			t.Fatal(err)
		}
		if afterConversations != beforeConversations || afterSchedule.RunCount != beforeSchedule.RunCount || !afterSchedule.NextRunAt.Equal(now.Add(-time.Minute)) {
			t.Error("new execution data changed during drain")
		}
		select {
		case <-drained:
			t.Error("drain reported completion while original task still running")
		default:
		}
	}
	done := RunScheduler(ctx, db.DB, "")
	cancel()
	select {
	case <-done:
		t.Error("scheduler reported drained while admitted task still running")
	case <-time.After(30 * time.Millisecond):
	}
	unblock()
	select {
	case <-finalizing:
	case <-time.After(3 * time.Second):
		t.Fatal("task did not reach its final DB write")
	}
	select {
	case <-done:
		t.Error("scheduler reported drained before final DB write")
	case <-time.After(30 * time.Millisecond):
	}
	select {
	case <-drained:
		t.Error("BeginDrain completed before final DB write")
	case <-time.After(30 * time.Millisecond):
	}
	finish()
	select {
	case <-drained:
	case <-time.After(3 * time.Second):
		t.Fatal("BeginDrain did not complete after final DB write")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler did not finish after admitted task")
	}
	var task orm.TaskCenterTask
	if err := db.Where("schedule_id = ?", schedule.ID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	want := "succeeded"
	if scenario == "failure" {
		want = "failed"
	}
	if task.Status != want {
		t.Fatalf("Done preceded final outcome write: got %s, want %s", task.Status, want)
	}
	if scenario != "failure" {
		var output orm.TaskRunOutput
		if err := db.Where("task_id = ?", task.ID).First(&output).Error; err != nil || output.OutputStatus != "ready" {
			t.Fatalf("Done preceded final output: %s %v", output.OutputStatus, err)
		}
	}
}
