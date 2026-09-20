package taskcenter

import (
	"context"
	"strings"
	"testing"

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

func TestRequestScheduledResultModelSummaryUsesFullResultAndCapsOutput(t *testing.T) {
	answer := "完整任务结果：转化率下降，主要原因是移动端流失增加。"
	called := false
	got, err := requestScheduledResultModelSummary(context.Background(), answer, map[string]any{"llm": "configured"}, func(_ context.Context, req algo.PolishGenerateRequest) (string, error) {
		called = true
		if req.Content != answer {
			t.Fatalf("content = %q, want full task result", req.Content)
		}
		if !strings.Contains(req.UserInstruct, "不超过100") {
			t.Fatalf("instruction does not contain length constraint: %q", req.UserInstruct)
		}
		return "摘要：" + strings.Repeat("核心结论", 20), nil
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
