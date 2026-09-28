package taskcenter

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lazymind/core/algo"
	"lazymind/core/common/orm"
)

func TestScheduledResultSummaryKeepsOnlyStructuredFacts(t *testing.T) {
	answer := "任务执行成功\n执行时间：2026-09-20T10:00:00.123456Z\n执行耗时：12 秒\n成功步骤：3\n失败步骤：0\n\n这是一段很长的完整任务内容，不应出现在通知摘要中。"
	got := scheduledResultSummary(answer)
	want := "任务成功；时间：2026-09-20 10:00；耗时：12 秒；步骤：成功3/失败0"
	if got != want {
		t.Fatalf("scheduledResultSummary() = %q, want %q", got, want)
	}
	if len([]rune(got)) > 100 {
		t.Fatalf("summary has %d runes, want <= 100", len([]rune(got)))
	}
}

func TestScheduledResultSummaryUsesBoundedActualResultAsFallback(t *testing.T) {
	answer := "任务已经完成。这里是实际结果正文，包含需要推送到通知里的核心信息。"
	got := scheduledResultSummary(answer)
	if got != answer {
		t.Fatalf("scheduledResultSummary() = %q, want %q", got, answer)
	}
}

func TestScheduledResultSummaryPrefersConclusionFromCommonMarkdown(t *testing.T) {
	answer := `# 数据分析报告

以下是本次数据分析结果。

## 分析过程

- 汇总访问数据
- 对比不同终端

## 核心结论

核心结论：移动端转化率下降 8%，主要原因是注册流程中的用户流失增加。

建议：优先简化移动端注册步骤。`
	got := scheduledResultSummary(answer)
	want := "核心结论：移动端转化率下降 8%，主要原因是注册流程中的用户流失增加。"
	if got != want {
		t.Fatalf("scheduledResultSummary() = %q, want %q", got, want)
	}
}

func TestScheduledResultSummarySkipsHeadingsAndPreambleWithoutKeywords(t *testing.T) {
	answer := `# 今日工作

下面是本次任务的执行情况。

已完成三个客户数据文件的清洗，并生成了可供销售团队使用的合并结果。

相关文件已经保存到任务附件中。`
	got := scheduledResultSummary(answer)
	want := "已完成三个客户数据文件的清洗，并生成了可供销售团队使用的合并结果。"
	if got != want {
		t.Fatalf("scheduledResultSummary() = %q, want %q", got, want)
	}
}

func TestScheduledResultSummaryDoesNotExposeCodeAsFallback(t *testing.T) {
	answer := "# 输出文件\n\n```json\n{\"token\":\"private-value\",\"rows\":42}\n```"
	got := scheduledResultSummary(answer)
	if got != "任务已完成，请打开任务查看结果。" {
		t.Fatalf("scheduledResultSummary() = %q", got)
	}
}

func TestShouldGenerateScheduledResultModelSummaryOnlyForEnabledSummaryNotification(t *testing.T) {
	summaryConfig := `{"events":{"succeeded":{"enabled":true,"content":"summary"}},"channels":{"feishu":{"enabled":true}}}`
	fullConfig := `{"events":{"succeeded":{"enabled":true,"content":"full"}},"channels":{"feishu":{"enabled":true}}}`
	noChannelConfig := `{"events":{"succeeded":{"enabled":true,"content":"summary"}},"channels":{"feishu":{"enabled":false}}}`

	if !shouldGenerateScheduledResultModelSummary(orm.TaskCenterTask{NotificationConfig: &summaryConfig}) {
		t.Fatal("enabled summary notification should request a model summary")
	}
	if shouldGenerateScheduledResultModelSummary(orm.TaskCenterTask{NotificationConfig: &fullConfig}) {
		t.Fatal("full-content notification must not request a model summary")
	}
	if shouldGenerateScheduledResultModelSummary(orm.TaskCenterTask{NotificationConfig: &noChannelConfig}) {
		t.Fatal("notification without an enabled channel must not request a model summary")
	}
	if shouldGenerateScheduledResultModelSummary(orm.TaskCenterTask{}) {
		t.Fatal("task without notification config must not request a model summary")
	}
}

func TestRequestScheduledResultModelSummaryUsesNeutralGenerationAndCapsOutput(t *testing.T) {
	answer := "完整任务结果：转化率下降，主要原因是移动端流失增加。"
	called := false
	got, err := requestScheduledResultModelSummary(context.Background(), answer, map[string]any{"llm": "configured"}, func(_ context.Context, req algo.LearningGenerateRequest) (string, error) {
		called = true
		if req.Content != answer {
			t.Fatalf("content = %q, want full task result", req.Content)
		}
		if !strings.Contains(req.UserInstruct, `{"summary"`) || !strings.Contains(req.UserInstruct, "不超过100") {
			t.Fatalf("instruction does not contain length constraint: %q", req.UserInstruct)
		}
		return `{"summary":"` + strings.Repeat("核心结论", 20) + `"}`, nil
	})
	if err != nil {
		t.Fatalf("requestScheduledResultModelSummary() error = %v", err)
	}
	if !called {
		t.Fatal("summary model was not called")
	}
	if len([]rune(got)) <= 50 || len([]rune(got)) > 100 {
		t.Fatalf("model summary has %d runes, want 51-100", len([]rune(got)))
	}
	if strings.HasPrefix(got, "摘要") {
		t.Fatalf("model summary retained response label: %q", got)
	}
}

func TestRequestScheduledResultModelSummaryRejectsPromptEchoAfterOneCall(t *testing.T) {
	calls := 0
	got, err := requestScheduledResultModelSummary(context.Background(), "真实任务结果", nil, func(_ context.Context, _ algo.LearningGenerateRequest) (string, error) {
		calls++
		return "请根据以下任务完整结果生成一条中文通知摘要。只输出摘要正文，不要标题或解释；必须不超过100个汉字。", nil
	})
	if err == nil || got != "" {
		t.Fatalf("prompt echo result = %q, err=%v; want rejected", got, err)
	}
	if calls != 1 {
		t.Fatalf("model calls = %d, want exactly 1", calls)
	}
}

func TestScheduledResultSummaryContextHonorsCanceledCaller(t *testing.T) {
	type contextKey string
	parent, cancelParent := context.WithCancel(context.WithValue(context.Background(), contextKey("trace"), "kept"))
	cancelParent()

	ctx, cancel := scheduledResultSummaryContext(parent)
	defer cancel()
	if err := ctx.Err(); err != context.Canceled {
		t.Fatalf("summary context ignored caller cancellation: %v", err)
	}
	if got := ctx.Value(contextKey("trace")); got != "kept" {
		t.Fatalf("summary context lost caller values: %v", got)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("summary context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining < scheduledResultSummaryTimeout-time.Second || remaining > scheduledResultSummaryTimeout {
		t.Fatalf("summary timeout = %v, want about %v", remaining, scheduledResultSummaryTimeout)
	}
}

func TestRunScheduledResultFinalizationCoalescesSameTask(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	result := make(chan string, 2)
	run := func() {
		status, err := runScheduledResultFinalization("task-one", func() (string, error) {
			if calls.Add(1) == 1 {
				close(started)
			}
			<-release
			return "ready", nil
		})
		if err != nil {
			result <- err.Error()
			return
		}
		result <- status
	}
	go run()
	<-started
	go run()
	time.Sleep(20 * time.Millisecond)
	close(release)
	if got := <-result; got != "ready" {
		t.Fatalf("first result = %q", got)
	}
	if got := <-result; got != "ready" {
		t.Fatalf("second result = %q", got)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("finalization calls = %d, want 1", got)
	}
}

func TestScheduledResultAlreadyFinalizedRequiresSucceededTaskAndStoredOutput(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	task := orm.TaskCenterTask{ID: "task-one", UserID: "owner", ConversationID: "conversation", TaskType: "scheduled", Status: "succeeded"}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	if done, err := scheduledResultAlreadyFinalized(t.Context(), db, task); err != nil || done {
		t.Fatalf("missing output reported finalized: done=%v err=%v", done, err)
	}
	if err := db.Create(&orm.TaskRunOutput{ID: "output", TaskID: task.ID, ConversationID: task.ConversationID, OutputStatus: "ready", CreatedAt: time.Now(), UpdatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if done, err := scheduledResultAlreadyFinalized(t.Context(), db, task); err != nil || !done {
		t.Fatalf("stored output was not treated as finalized: done=%v err=%v", done, err)
	}
	task.Status = "running"
	if done, err := scheduledResultAlreadyFinalized(t.Context(), db, task); err != nil || done {
		t.Fatalf("running task reported finalized: done=%v err=%v", done, err)
	}
	for _, status := range []string{"failed", "canceled", "skipped"} {
		task.Status = status
		if done, err := scheduledResultAlreadyFinalized(t.Context(), db, task); err != nil || !done {
			t.Fatalf("terminal task %q was allowed to summarize again: done=%v err=%v", status, done, err)
		}
	}
}

func TestClaimScheduledResultModelSummaryPersistsSingleAttempt(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	progress := orm.RawJSON(`{"existing":"kept"}`)
	task := orm.TaskCenterTask{ID: "summary-task", UserID: "owner", ConversationID: "conversation", TaskType: "scheduled", Status: "running", ProgressJSON: progress}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	claimed, err := claimScheduledResultModelSummary(t.Context(), db, task.ID)
	if err != nil || !claimed {
		t.Fatalf("first claim = %v, err=%v", claimed, err)
	}
	claimed, err = claimScheduledResultModelSummary(t.Context(), db, task.ID)
	if err != nil || claimed {
		t.Fatalf("second claim = %v, err=%v", claimed, err)
	}
	if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(task.ProgressJSON, &saved); err != nil {
		t.Fatal(err)
	}
	if saved["existing"] != "kept" || saved[scheduledResultModelSummaryAttemptKey] == nil {
		t.Fatalf("progress marker did not preserve existing state: %#v", saved)
	}
}

func TestScheduledSummaryResolutionPersistsAndReloadsModelResult(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	task := orm.TaskCenterTask{ID: "resolved-summary-task", UserID: "owner", ConversationID: "conversation", TaskType: "scheduled", Status: "running"}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	if err := persistScheduledSummaryResolution(t.Context(), db, task.ID, "模型生成的真实摘要", "model"); err != nil {
		t.Fatal(err)
	}
	summary, source, resolved, err := loadScheduledSummaryResolution(t.Context(), db, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved || source != "model" || summary != "模型生成的真实摘要" {
		t.Fatalf("resolution = (%q, %q, %v), want persisted model summary", summary, source, resolved)
	}
}

func TestScheduledSummaryResolutionNeverOverwritesModelWithFallback(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	task := orm.TaskCenterTask{ID: "protected-summary-task", UserID: "owner", ConversationID: "conversation", TaskType: "scheduled", Status: "running"}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	if err := persistScheduledSummaryResolution(t.Context(), db, task.ID, "模型摘要", "model"); err != nil {
		t.Fatal(err)
	}
	if err := persistScheduledSummaryResolution(t.Context(), db, task.ID, "本地兜底摘要", "fallback"); err != nil {
		t.Fatal(err)
	}
	summary, source, resolved, err := loadScheduledSummaryResolution(t.Context(), db, task.ID)
	if err != nil || !resolved || summary != "模型摘要" || source != "model" {
		t.Fatalf("resolution = (%q, %q, %v), err=%v", summary, source, resolved, err)
	}
}

func TestWaitForScheduledSummaryResolutionReusesModelResult(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	task := orm.TaskCenterTask{ID: "waiting-summary-task", UserID: "owner", ConversationID: "conversation", TaskType: "scheduled", Status: "running"}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(25 * time.Millisecond)
		_ = persistScheduledSummaryResolution(context.Background(), db, task.ID, "并发收尾复用的模型摘要", "model")
	}()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	summary, err := waitForScheduledSummaryResolution(ctx, db, task.ID)
	if err != nil || summary != "并发收尾复用的模型摘要" {
		t.Fatalf("wait result = %q, err=%v", summary, err)
	}
}

func TestScheduledResultSummaryFallbackAllowsUpToOneHundredRunes(t *testing.T) {
	got := scheduledResultSummary(strings.Repeat("结", 80))
	if len([]rune(got)) != 80 {
		t.Fatalf("fallback summary has %d runes, want 80", len([]rune(got)))
	}
}

func TestNormalizeScheduledModelSummaryExtractsJSONSummary(t *testing.T) {
	got := normalizeScheduledModelSummary("```json\n{\"summary\":\"任务配置完成并试运行通过\"}\n```")
	if got != "任务配置完成并试运行通过" {
		t.Fatalf("normalizeScheduledModelSummary() = %q", got)
	}
}
