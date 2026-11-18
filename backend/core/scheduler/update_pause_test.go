package scheduler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"lazymind/core/common/maintenancegate"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestSchedulerUpdatePause(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestSchedulerUpdatePauseChild$")
	cmd.Env = append(os.Environ(), "LAZYMIND_UNIT_PAUSE_CHILD=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pause child: %v\n%s", err, out)
	}
}

func TestSchedulerUpdatePauseChild(t *testing.T) {
	if os.Getenv("LAZYMIND_UNIT_PAUSE_CHILD") != "1" {
		return
	}
	t.Setenv("TEST_DB_DRIVER", "sqlite")
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, db.DB, nil)
	s := orm.UserSchedule{ID: "pause-schedule", UserID: "owner", Name: "pause fixture", CronExpr: "0 * * * *", Timezone: "UTC", PromptTemplate: "fixture", Enabled: true, NextRunAt: time.Now().UTC().Add(-time.Minute)}
	if err := CreateSchedule(t.Context(), db.DB, &s); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ConversationID string `json:"conversation_id"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid fixture request")
			return
		}
		enterOnce.Do(func() { close(entered) })
		<-release
		if err := db.Create(&orm.ChatHistory{ID: "pause-history-" + body.ConversationID, Seq: 1, ConversationID: body.ConversationID, Result: "fixture result"}).Error; err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CORE_SELF_URL", server.URL)
	u, _ := url.Parse(server.URL)
	http.DefaultClient = &http.Client{Transport: schedulerFixtureTransport{host: u.Host}}
	defer func() {
		unblock()
		select {
		case <-BeginDrain():
		case <-time.After(3 * time.Second):
			t.Error("scheduler fixture did not drain")
		}
	}()
	runNow := func() int {
		req := httptest.NewRequest(http.MethodPost, "/schedules/"+s.ID+":run-now", nil)
		req.Header.Set("X-User-Id", "owner")
		reply := httptest.NewRecorder()
		RunNowHandler(reply, req)
		return reply.Code
	}
	pauseOwner := func(epoch uint64) *maintenancegate.Ticket {
		p, err := PauseForUpdate(epoch)
		if err != nil || p == nil {
			t.Fatalf("pause: %v", err)
		}
		return p
	}
	waitIdle := func(p *maintenancegate.Ticket) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := p.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	first := pauseOwner(1)
	waitIdle(first)
	var before orm.UserSchedule
	if err := db.First(&before, "id = ?", s.ID).Error; err != nil {
		t.Fatal(err)
	}
	if code := runNow(); code != http.StatusServiceUnavailable {
		t.Fatalf("paused manual run: %d", code)
	}
	fireSchedules(context.Background(), db.DB, "")
	var count int64
	if err := db.Model(&orm.TaskCenterTask{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("pause created tasks: %d %v", count, err)
	}
	if err := db.Model(&orm.Conversation{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("pause created conversation preparation", count, err)
	}
	var paused orm.UserSchedule
	if err := db.First(&paused, "id = ?", s.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !paused.NextRunAt.Equal(before.NextRunAt) || paused.RunCount != before.RunCount || paused.LastRunAt != nil {
		t.Fatal("paused tick consumed schedule state")
	}
	waiting := orm.TaskCenterTask{ID: "pause-waiting", UserID: "owner", TaskType: "scheduled", Status: "waiting_inputs", DependencyStatus: "waiting", Attempt: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := db.Create(&waiting).Error; err != nil {
		t.Fatal(err)
	}
	resumeWaitingTasks(context.Background(), db.DB)
	if err := db.First(&waiting, "id = ?", waiting.ID).Error; err != nil {
		t.Fatal(err)
	}
	if waiting.Status != "waiting_inputs" || waiting.Attempt != 1 {
		t.Fatal("paused dependency admission changed persisted state")
	}
	if err := ResumeFromUpdate(1); err != nil {
		t.Fatal(err)
	}
	if first.Validate() != maintenancegate.ErrStale {
		t.Fatal("old receipt remained valid")
	}
	resumeWaitingTasks(context.Background(), db.DB)
	if err := db.First(&waiting, "id = ?", waiting.ID).Error; err != nil {
		t.Fatal(err)
	}
	if waiting.Status != "failed" || waiting.Attempt != 2 {
		t.Fatal("resume did not process the waiting dependency")
	}
	fireSchedules(context.Background(), db.DB, "")
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("resumed work did not execute")
	}
	second := pauseOwner(2)
	if second.Validate() != maintenancegate.ErrActive {
		t.Fatal("pause omitted active execution")
	}
	if err := ResumeFromUpdate(2); err != nil {
		t.Fatal(err)
	}
	third := pauseOwner(3)
	if third.Validate() != maintenancegate.ErrActive {
		t.Fatal("resume forgot admitted execution")
	}
	if err := ResumeFromUpdate(2); err != maintenancegate.ErrStale {
		t.Fatal("stale resume affected new epoch", err)
	}
	if code := runNow(); code != http.StatusServiceUnavailable {
		t.Fatal("stale resume reopened roots")
	}
	unblock()
	waitIdle(third)
	var task orm.TaskCenterTask
	if err := db.Where("schedule_id = ?", s.ID).First(&task).Error; err != nil || task.Status != "succeeded" {
		t.Fatalf("pause did not retain final write: %s %v", task.Status, err)
	}
	if err := ResumeFromUpdate(3); err != nil {
		t.Fatal(err)
	}
	if code := runNow(); code != http.StatusOK {
		t.Fatalf("resumed manual run: %d", code)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := db.Model(&orm.TaskCenterTask{}).Where("schedule_id = ? AND status = ?", s.ID, "succeeded").Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("resumed manual execution did not finalize")
		}
		time.Sleep(time.Millisecond)
	}
}
