package tasktiming

import (
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func TestChatCompletionWaitsForBackgroundSubagents(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "already_running", true: "starts_after_reply"}[pending], func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.SubAgentTask{}, &orm.TaskExecutionTiming{}, &orm.TaskAttemptTiming{})
			start := time.Date(2026, 10, 20, 0, 0, 0, 123456000, time.UTC)
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			must(Begin(t.Context(), db.DB, "root", "history", "conv", "owner", start))
			child := orm.SubAgentTask{ID: "child", ConversationID: "conv", TriggerHistoryID: "history", CreateUserID: "owner", AgentType: "research", Mode: "manual", InputSlots: []byte(`[]`), OutputSlots: []byte(`[]`), ExecutionID: "attempt", Status: "running", StartedAt: ptr(start.Add(time.Second))}
			if pending {
				child.Status, child.StartedAt = "pending", nil
			}
			must(db.Create(&child).Error)
			must(RecordSubagent(t.Context(), db.DB, child))
			must(FinishChat(t.Context(), db.DB, "root", "completed", start.Add(3*time.Second)))
			run, err := Snapshot(t.Context(), db.DB, "owner", "conv", "history", start.Add(10*time.Second))
			must(err)
			if run.Timing.FinishedAt != nil || *run.Timing.ElapsedMS != 10000 {
				t.Fatalf("reply ended background round: %+v", run)
			}
			if pending {
				child.Status, child.StartedAt = "running", ptr(start.Add(20*time.Second))
				must(db.Model(&child).Updates(map[string]any{"status": child.Status, "started_at": child.StartedAt}).Error)
				must(RecordSubagent(t.Context(), db.DB, child))
			}
			child.Status, child.FinishedAt = "succeeded", ptr(start.Add(60*time.Second))
			must(db.Model(&child).Updates(map[string]any{"status": child.Status, "finished_at": child.FinishedAt}).Error)
			must(RecordSubagent(t.Context(), db.DB, child))
			must(FinishChat(t.Context(), db.DB, "root", "completed", start.Add(120*time.Second)))
			run, err = Snapshot(t.Context(), db.DB, "owner", "conv", "history", start.Add(180*time.Second))
			must(err)
			want := int64(59000)
			if pending {
				want = 40000
			}
			if run.Timing.FinishedAt == nil || !run.Timing.FinishedAt.Equal(*child.FinishedAt) || *run.Timing.ElapsedMS != 60000 || *run.Timing.CumulativeExecutionMS != want || run.Timing.RunningAttemptCount != 0 {
				t.Fatalf("lost final background duration: %+v", run.Timing)
			}
		})
	}
}

func TestReplyAndBackgroundCompletionSerialize(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.SubAgentTask{}, &orm.TaskExecutionTiming{}, &orm.TaskAttemptTiming{})
	start := time.Date(2026, 10, 20, 0, 0, 0, 123456000, time.UTC)
	if err := Begin(t.Context(), db.DB, "root", "history", "conv", "owner", start); err != nil {
		t.Fatal(err)
	}
	child := orm.SubAgentTask{ID: "child", ConversationID: "conv", TriggerHistoryID: "history", CreateUserID: "owner", AgentType: "research", ExecutionID: "attempt", Status: "running", StartedAt: ptr(start.Add(time.Second)), InputSlots: []byte(`[]`), OutputSlots: []byte(`[]`)}
	if err := db.Create(&child).Error; err != nil {
		t.Fatal(err)
	}
	if err := RecordSubagent(t.Context(), db.DB, child); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	errors := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-ready
		errors <- FinishChat(t.Context(), db.DB, "root", "completed", start.Add(60*time.Second))
	}()
	go func() {
		defer wg.Done()
		<-ready
		errors <- transaction(t.Context(), db.DB, func(tx *gorm.DB) error {
			child.Status, child.FinishedAt = "succeeded", ptr(start.Add(60*time.Second))
			if err := tx.Model(&child).Updates(map[string]any{"status": child.Status, "finished_at": child.FinishedAt}).Error; err != nil {
				return err
			}
			return RecordSubagent(t.Context(), tx, child)
		})
	}()
	close(ready)
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	run, err := Snapshot(t.Context(), db.DB, "owner", "conv", "history", start.Add(120*time.Second))
	if err != nil || run.Timing.FinishedAt == nil || *run.Timing.CumulativeExecutionMS != 59000 || *run.Timing.ElapsedMS != 60000 {
		t.Fatalf("lost concurrent completion: %+v %v", run, err)
	}
}

func TestPendingBackgroundCancellationSettlesRound(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.SubAgentTask{}, &orm.TaskExecutionTiming{}, &orm.TaskAttemptTiming{})
	start := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	if err := Begin(t.Context(), db.DB, "root", "history", "conv", "owner", start); err != nil {
		t.Fatal(err)
	}
	child := orm.SubAgentTask{ID: "child", ConversationID: "conv", TriggerHistoryID: "history", CreateUserID: "owner", AgentType: "research", ExecutionID: "attempt", Status: "pending", InputSlots: []byte(`[]`), OutputSlots: []byte(`[]`)}
	if err := db.Create(&child).Error; err != nil {
		t.Fatal(err)
	}
	if err := FinishChat(t.Context(), db.DB, "root", "completed", start.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	child.Status, child.FinishedAt = "canceled", ptr(start.Add(10*time.Second))
	if err := db.Model(&child).Updates(map[string]any{"status": child.Status, "finished_at": child.FinishedAt}).Error; err != nil {
		t.Fatal(err)
	}
	if err := RecordSubagent(t.Context(), db.DB, child); err != nil {
		t.Fatal(err)
	}
	run, err := Snapshot(t.Context(), db.DB, "owner", "conv", "history", start.Add(20*time.Second))
	if err != nil || run.Timing.FinishedAt == nil || *run.Timing.ElapsedMS != 10000 || *run.Timing.CumulativeExecutionMS != 0 {
		t.Fatalf("pending cancellation left round active: %+v %v", run, err)
	}
}

func TestOnlyLastBackgroundChildFinishesRound(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.SubAgentTask{}, &orm.TaskExecutionTiming{}, &orm.TaskAttemptTiming{})
	start := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	if err := Begin(t.Context(), db.DB, "root", "history", "conv", "owner", start); err != nil {
		t.Fatal(err)
	}
	children := []orm.SubAgentTask{}
	for _, id := range []string{"first", "last"} {
		child := orm.SubAgentTask{ID: id, ConversationID: "conv", TriggerHistoryID: "history", CreateUserID: "owner", AgentType: "research", ExecutionID: id, Status: "running", StartedAt: ptr(start.Add(time.Second)), InputSlots: []byte(`[]`), OutputSlots: []byte(`[]`)}
		if err := db.Create(&child).Error; err != nil {
			t.Fatal(err)
		}
		if err := RecordSubagent(t.Context(), db.DB, child); err != nil {
			t.Fatal(err)
		}
		children = append(children, child)
	}
	if err := FinishChat(t.Context(), db.DB, "root", "completed", start.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	for i := range children {
		child := &children[i]
		child.Status, child.FinishedAt = "succeeded", ptr(start.Add(time.Duration(i+1)*30*time.Second))
		if err := db.Model(child).Updates(map[string]any{"status": child.Status, "finished_at": child.FinishedAt}).Error; err != nil {
			t.Fatal(err)
		}
		if err := RecordSubagent(t.Context(), db.DB, *child); err != nil {
			t.Fatal(err)
		}
		run, err := Snapshot(t.Context(), db.DB, "owner", "conv", "history", start.Add(90*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if (run.Timing.FinishedAt != nil) != (i == 1) {
			t.Fatalf("child %d incorrectly finalized round: %+v", i, run.Timing)
		}
		if i == 1 && (*run.Timing.ElapsedMS != 60000 || *run.Timing.CumulativeExecutionMS != 88000) {
			t.Fatalf("lost parallel execution: %+v", run.Timing)
		}
	}
}
