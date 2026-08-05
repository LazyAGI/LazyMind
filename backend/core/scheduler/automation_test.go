package scheduler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func automationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.UserSchedule{}, &orm.ScheduleDependency{}, &orm.TaskCenterTask{}, &orm.ChatHistory{}, &orm.ConversationArtifact{}, &orm.TaskRunOutput{}, &orm.TaskRunInput{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestDependencyCollectionSnapshotsActualOutputsPerRun(t *testing.T) {
	db := automationTestDB(t)
	ctx := context.Background()
	end := time.Now().UTC().Truncate(time.Second)
	start := end.Add(-7 * 24 * time.Hour)
	source := orm.UserSchedule{ID: "daily", UserID: "u", Name: "Github调研", CronExpr: "0 12 * * *", Timezone: "UTC", PromptTemplate: "daily", KbIDs: "[]", FileIDs: "[]", Enabled: true, NextRunAt: end.Add(time.Hour), CreatedAt: start.Add(-time.Hour)}
	target := orm.UserSchedule{ID: "weekly", UserID: "u", Name: "调研周报", CronExpr: "0 12 * * 4", Timezone: "UTC", PromptTemplate: "weekly", KbIDs: "[]", FileIDs: "[]", Enabled: true, NextRunAt: end.Add(24 * time.Hour), CreatedAt: start.Add(-time.Hour)}
	if err := db.Create(&source).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	dep := orm.ScheduleDependency{ID: "dep", UserID: "u", SourceScheduleID: source.ID, TargetScheduleID: target.ID, Enabled: true, CreatedAt: start, UpdatedAt: start}
	if err := db.Create(&dep).Error; err != nil {
		t.Fatal(err)
	}
	firedAt := end.Add(-24 * time.Hour)
	upstream := orm.TaskCenterTask{ID: "upstream", UserID: "u", ConversationID: "conv", TaskType: "scheduled", Status: "succeeded", ScheduleID: &source.ID, ScheduledFireAt: &firedAt, TriggerType: "manual", CreatedAt: firedAt, UpdatedAt: firedAt}
	if err := db.Create(&upstream).Error; err != nil {
		t.Fatal(err)
	}
	output := orm.TaskRunOutput{ID: "output", TaskID: upstream.ID, ConversationID: "conv", FinalAnswerText: "daily result", SummaryText: "daily result", ArtifactManifestJSON: []byte("[]"), OutputStatus: "ready", ContentHash: "hash", CreatedAt: firedAt, UpdatedAt: firedAt}
	if err := db.Create(&output).Error; err != nil {
		t.Fatal(err)
	}

	ready, contextText, count := collectDependencyInputs(ctx, db, target, "downstream-1", start, end, false)
	if !ready || count != 1 || !strings.Contains(contextText, "daily result") {
		t.Fatalf("expected actual output to be collected, ready=%v count=%d context=%q", ready, count, contextText)
	}
	ready, _, count = collectDependencyInputs(ctx, db, target, "downstream-2", start, end, false)
	if !ready || count != 1 {
		t.Fatalf("expected each downstream run to snapshot matching output, ready=%v count=%d", ready, count)
	}
}

func TestDependencyCollectionWaitsForSameTickActualTask(t *testing.T) {
	db := automationTestDB(t)
	ctx := context.Background()
	end := time.Now().UTC().Truncate(time.Second)
	start := end.Add(-7 * 24 * time.Hour)
	source := orm.UserSchedule{ID: "daily-same-tick", UserID: "u", Name: "日报", CronExpr: "0 9 * * *", Timezone: "UTC", PromptTemplate: "daily", KbIDs: "[]", FileIDs: "[]", Enabled: true, NextRunAt: end.Add(24 * time.Hour), CreatedAt: start}
	target := orm.UserSchedule{ID: "weekly-same-tick", UserID: "u", Name: "周报", CronExpr: "0 9 * * 1", Timezone: "UTC", PromptTemplate: "weekly", KbIDs: "[]", FileIDs: "[]", Enabled: true, NextRunAt: end.Add(7 * 24 * time.Hour), CreatedAt: start}
	for _, schedule := range []orm.UserSchedule{source, target} {
		if err := db.Create(&schedule).Error; err != nil {
			t.Fatal(err)
		}
	}
	dep := orm.ScheduleDependency{ID: "same-tick-dep", UserID: "u", SourceScheduleID: source.ID, TargetScheduleID: target.ID, Enabled: true, CreatedAt: start, UpdatedAt: start}
	if err := db.Create(&dep).Error; err != nil {
		t.Fatal(err)
	}
	upstream := orm.TaskCenterTask{ID: "same-tick-upstream", UserID: "u", ConversationID: "same-tick-conv", TaskType: "scheduled", Status: "running", ScheduleID: &source.ID, ScheduledFireAt: &end, TriggerType: "scheduled", CreatedAt: end, UpdatedAt: end}
	if err := db.Create(&upstream).Error; err != nil {
		t.Fatal(err)
	}

	ready, _, count := collectDependencyInputs(ctx, db, target, "same-tick-downstream", start, end, false)
	if ready || count != 0 {
		t.Fatalf("expected downstream to wait for same-tick running source, ready=%v count=%d", ready, count)
	}
	output := orm.TaskRunOutput{ID: "same-tick-output", TaskID: upstream.ID, ConversationID: upstream.ConversationID, FinalAnswerText: "same tick result", SummaryText: "same tick result", ArtifactManifestJSON: []byte("[]"), OutputStatus: "ready", ContentHash: "same-tick-hash", CreatedAt: end, UpdatedAt: end}
	if err := db.Create(&output).Error; err != nil {
		t.Fatal(err)
	}
	ready, contextText, count := collectDependencyInputs(ctx, db, target, "same-tick-downstream", start, end, false)
	if !ready || count != 1 || !strings.Contains(contextText, "same tick result") {
		t.Fatalf("expected downstream to collect completed same-tick source, ready=%v count=%d context=%q", ready, count, contextText)
	}
}

func TestDependencyWindowStartUsesLastSuccessfulNonDeletedRun(t *testing.T) {
	db := automationTestDB(t)
	reference := time.Date(2026, time.July, 30, 4, 35, 0, 0, time.UTC)
	schedule := orm.UserSchedule{ID: "weekly-window", UserID: "u", Name: "周报", CronExpr: "35 12 * * 4", Timezone: "Asia/Shanghai", PromptTemplate: "weekly", KbIDs: "[]", FileIDs: "[]", Enabled: true, NextRunAt: reference, CreatedAt: reference.AddDate(0, -1, 0)}
	if err := db.Create(&schedule).Error; err != nil {
		t.Fatal(err)
	}
	emptyEnd := reference.Add(-time.Hour)
	emptyRun := orm.TaskCenterTask{ID: "empty-aggregate", UserID: "u", ConversationID: "", TaskType: "scheduled", Status: "failed", ScheduleID: &schedule.ID, WindowStart: &schedule.CreatedAt, WindowEnd: &emptyEnd, TriggerType: "manual", DependencyStatus: "missing", CreatedAt: emptyEnd, UpdatedAt: emptyEnd}
	if err := db.Create(&emptyRun).Error; err != nil {
		t.Fatal(err)
	}
	expected, err := previousCronTime(schedule.CronExpr, schedule.Timezone, reference)
	if err != nil {
		t.Fatal(err)
	}
	if got := dependencyWindowStart(db, schedule, reference); !got.Equal(expected) {
		t.Fatalf("failed aggregate must not advance window: got %s want %s", got, expected)
	}

	if err := db.Model(&orm.TaskCenterTask{}).Where("id = ?", emptyRun.ID).Updates(map[string]any{"status": "succeeded", "dependency_status": "ready"}).Error; err != nil {
		t.Fatal(err)
	}
	if got := dependencyWindowStart(db, schedule, reference); !got.Equal(emptyEnd) {
		t.Fatalf("successful aggregate must advance window: got %s want %s", got, emptyEnd)
	}
	archivedAt := emptyEnd.Add(time.Minute)
	if err := db.Model(&orm.TaskCenterTask{}).Where("id = ?", emptyRun.ID).Update("archived_at", archivedAt).Error; err != nil {
		t.Fatal(err)
	}
	if got := dependencyWindowStart(db, schedule, reference); !got.Equal(expected) {
		t.Fatalf("deleted aggregate must not advance window: got %s want %s", got, expected)
	}
}

func TestDependencyCollectionMaterializesHistoricalChatOutput(t *testing.T) {
	db := automationTestDB(t)
	ctx := context.Background()
	end := time.Now().UTC().Truncate(time.Second)
	start := end.AddDate(0, -1, 0)
	source := orm.UserSchedule{ID: "historical-daily", UserID: "u", Name: "历史日报", CronExpr: "0 9 * * *", Timezone: "UTC", PromptTemplate: "daily", KbIDs: "[]", FileIDs: "[]", Enabled: true, NextRunAt: end.Add(24 * time.Hour), CreatedAt: start.AddDate(0, -1, 0)}
	target := orm.UserSchedule{ID: "new-monthly", UserID: "u", Name: "新建月报", CronExpr: "0 9 1 * *", Timezone: "UTC", PromptTemplate: "monthly", KbIDs: "[]", FileIDs: "[]", Enabled: true, NextRunAt: end.AddDate(0, 1, 0), CreatedAt: end}
	for _, schedule := range []orm.UserSchedule{source, target} {
		if err := db.Create(&schedule).Error; err != nil {
			t.Fatal(err)
		}
	}
	dep := orm.ScheduleDependency{ID: "historical-dep", UserID: "u", SourceScheduleID: source.ID, TargetScheduleID: target.ID, Enabled: true, CreatedAt: end, UpdatedAt: end}
	if err := db.Create(&dep).Error; err != nil {
		t.Fatal(err)
	}
	executedAt := end.Add(-10 * 24 * time.Hour)
	upstream := orm.TaskCenterTask{ID: "historical-task", UserID: "u", ConversationID: "historical-conv", TaskType: "scheduled", Status: "succeeded", ScheduleID: &source.ID, ScheduledFireAt: &executedAt, TriggerType: "scheduled", CreatedAt: executedAt, UpdatedAt: executedAt}
	if err := db.Create(&upstream).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ChatHistory{ID: "historical-history", Seq: 1, ConversationID: upstream.ConversationID, Result: "old daily answer"}).Error; err != nil {
		t.Fatal(err)
	}

	ready, contextText, count := collectDependencyInputs(ctx, db, target, "historical-downstream", start, end, false)
	if !ready || count != 1 || !strings.Contains(contextText, "old daily answer") {
		t.Fatalf("expected historical chat to be standardized and collected, ready=%v count=%d context=%q", ready, count, contextText)
	}
	for _, expected := range []string{"已完成历史执行", "@历史日报（历史执行 1/1）", `conversation_id="historical-conv"`, "不是待执行任务"} {
		if !strings.Contains(contextText, expected) {
			t.Fatalf("expected context to explain historical conversation references; missing %q in %q", expected, contextText)
		}
	}
}

func TestReplaceDependenciesRejectsCycle(t *testing.T) {
	db := automationTestDB(t)
	now := time.Now().UTC()
	for _, id := range []string{"a", "b", "c"} {
		if err := db.Create(&orm.UserSchedule{ID: id, UserID: "u", CronExpr: "0 9 * * *", Timezone: "UTC", PromptTemplate: id, KbIDs: "[]", FileIDs: "[]", Enabled: true, NextRunAt: now, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := replaceDependencies(db, "u", "b", []dependencyInput{{SourceScheduleID: "a"}}); err != nil {
		t.Fatal(err)
	}
	if err := replaceDependencies(db, "u", "c", []dependencyInput{{SourceScheduleID: "b"}}); err != nil {
		t.Fatal(err)
	}
	if err := replaceDependencies(db, "u", "a", []dependencyInput{{SourceScheduleID: "c"}}); err == nil {
		t.Fatal("expected cycle to be rejected")
	}
}

func TestReplaceDependenciesRejectsMoreFrequentTarget(t *testing.T) {
	db := automationTestDB(t)
	now := time.Now().UTC()
	for id, cronExpr := range map[string]string{"weekly": "0 9 * * 1", "daily": "0 9 * * *"} {
		if err := db.Create(&orm.UserSchedule{ID: id, UserID: "u", CronExpr: cronExpr, Timezone: "UTC", PromptTemplate: id, KbIDs: "[]", FileIDs: "[]", Enabled: true, NextRunAt: now, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := replaceDependencies(db, "u", "daily", []dependencyInput{{SourceScheduleID: "weekly"}}); err != errDependencyTooSparse {
		t.Fatalf("expected frequency validation error, got %v", err)
	}
	if err := replaceDependencies(db, "u", "weekly", []dependencyInput{{SourceScheduleID: "daily"}}); err != nil {
		t.Fatalf("expected weekly target to accept daily source: %v", err)
	}
}

func TestFinalizeTaskOutputStoresPlainChatAnswer(t *testing.T) {
	db := automationTestDB(t)
	now := time.Now().UTC()
	if err := db.Create(&orm.TaskCenterTask{ID: "task", UserID: "u", ConversationID: "conv", TaskType: "scheduled", Status: "running", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ChatHistory{ID: "hist", Seq: 1, ConversationID: "conv", Result: "daily result"}).Error; err != nil {
		t.Fatal(err)
	}
	finalizeTaskOutput(context.Background(), db, "task", "conv")
	var output orm.TaskRunOutput
	if err := db.Where("task_id = ?", "task").First(&output).Error; err != nil {
		t.Fatal(err)
	}
	if output.OutputStatus != "ready" || output.FinalAnswerText != "daily result" || output.ContentHash == "" {
		t.Fatalf("unexpected output: %+v", output)
	}
}

func TestFinalizeTaskOutputStoresEmptyResultFailureReason(t *testing.T) {
	db := automationTestDB(t)
	now := time.Now().UTC()
	task := orm.TaskCenterTask{ID: "empty-task", UserID: "u", ConversationID: "empty-conv", TaskType: "scheduled", Status: "running", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}

	if status := finalizeTaskOutput(context.Background(), db, task.ID, task.ConversationID); status != "empty" {
		t.Fatalf("expected empty output status, got %q", status)
	}
	var got orm.TaskCenterTask
	if err := db.First(&got, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != "failed" || got.FinishedAt == nil {
		t.Fatalf("expected failed task with finished_at, got %#v", got)
	}
	var progress map[string]any
	if err := json.Unmarshal(got.ProgressJSON, &progress); err != nil {
		t.Fatalf("decode progress: %v", err)
	}
	if progress["failure_reason"] != "聊天服务未生成可用结果" {
		t.Fatalf("unexpected failure reason: %#v", progress["failure_reason"])
	}
}

func TestTaskOutputBodyKeepsOnlyVisibleAnswer(t *testing.T) {
	raw := "<think>hidden reasoning</think>中途看似正文<tp>准备搜索</tp><tool_call>{\"name\":\"search\"}</tool_call>工具间歇说明<trp>搜索完成</trp><tool_result>hidden result</tool_result>最终正文"
	if got := taskOutputBody(raw); got != "最终正文" {
		t.Fatalf("taskOutputBody() = %q", got)
	}
}

func TestTaskOutputBodyWithoutProcessBlocksKeepsWholeAnswer(t *testing.T) {
	if got := taskOutputBody("完整正文"); got != "完整正文" {
		t.Fatalf("taskOutputBody() = %q", got)
	}
}
