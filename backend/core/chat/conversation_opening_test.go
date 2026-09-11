package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"lazymind/core/algo"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func openingTestService(t *testing.T) *openingService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&orm.Conversation{}, &orm.ChatHistory{}, &orm.ExternalAgentBinding{}, &orm.AsyncJob{}, &orm.ConversationOpening{}, &orm.ConversationOpeningBackfill{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { sqlDB.Close() })
	s := newOpeningService(db)
	s.loadConfig = func(context.Context, *gorm.DB, string) (map[string]any, error) {
		return map[string]any{"llm": map[string]any{"model": "default"}, "conversation_metadata": map[string]any{"model": "metadata"}}, nil
	}
	s.call = func(context.Context, json.RawMessage, map[string]any, int) (algo.OpeningTaskResult, error) {
		return openingTestResult("ready"), nil
	}
	return s
}
func openingTestResult(status string) algo.OpeningTaskResult {
	return algo.OpeningTaskResult{Status: "succeeded", Output: algo.OpeningDescription{Title: "对话整理方案", Summary: "为 LazyMind 设计对话整理方案。", IntentStatus: status, MissingContext: []string{}}, Usage: json.RawMessage(`{"model_calls":1,"model_id":{"model":"test"}}`)}
}
func openingTestConversation(t *testing.T, s *openingService, id, title, source string) orm.Conversation {
	t.Helper()
	now := time.Now().UTC().Add(-time.Hour)
	c := orm.Conversation{ID: id, DisplayName: title, TitleSource: source, ChatExecutor: "lazymind", BaseModel: orm.BaseModel{CreateUserID: "u1", CreatedAt: now, UpdatedAt: now}}
	if err := s.db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	return c
}
func openingTestInput(t *testing.T, s *openingService, id, conv, text string, seq int) {
	t.Helper()
	h := orm.ChatHistory{ID: id, ConversationID: conv, RawContent: text, Seq: seq, RunStatus: "completed"}
	if err := s.db.Create(&h).Error; err != nil {
		t.Fatal(err)
	}
}
func openingTestRun(t *testing.T, s *openingService, conv string) (asyncjob.Result, error) {
	t.Helper()
	var meta orm.ConversationOpening
	if err := s.db.First(&meta, "conversation_id = ?", conv).Error; err != nil {
		t.Fatal(err)
	}
	var row orm.AsyncJob
	if err := s.db.First(&row, "id = ?", meta.JobID).Error; err != nil {
		t.Fatal(err)
	}
	until := time.Now().UTC().Add(time.Minute)
	row.Status = "running"
	row.AttemptCount++
	row.LockUntil = &until
	if err := s.db.Save(&row).Error; err != nil {
		t.Fatal(err)
	}
	job := asyncjob.Job{ID: row.ID, JobType: row.JobType, ResourceType: row.ResourceType, ResourceID: row.ResourceID, PayloadJSON: row.PayloadJSON, AttemptCount: row.AttemptCount, CreateUserID: row.CreateUserID}
	result, err := s.generate(context.Background(), job, nil)
	status := "succeeded"
	if err != nil {
		status = "pending"
		if result.Permanent || row.AttemptCount >= row.MaxAttempts {
			status = "failed"
		}
	}
	s.db.Model(&row).Updates(map[string]any{"status": status, "lock_until": nil})
	return result, err
}
func openingTestMeta(t *testing.T, s *openingService, id string) orm.ConversationOpening {
	t.Helper()
	var m orm.ConversationOpening
	if err := s.db.First(&m, "conversation_id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return m
}

func TestOpeningTitleProtectionAndStableActivity(t *testing.T) {
	for _, source := range []string{"unknown", "user", "rename_during_call"} {
		t.Run(source, func(t *testing.T) {
			s := openingTestService(t)
			title := "整理 LazyMind 对话"
			actualSource := source
			if source == "rename_during_call" {
				actualSource = "default"
			}
			before := openingTestConversation(t, s, "c1", title, actualSource)
			openingTestInput(t, s, "h1", "c1", title, 1)
			if _, err := s.enqueue(context.Background(), "c1", ""); err != nil {
				t.Fatal(err)
			}
			if source == "rename_during_call" {
				s.call = func(context.Context, json.RawMessage, map[string]any, int) (algo.OpeningTaskResult, error) {
					s.db.Model(&orm.Conversation{}).Where("id = ?", "c1").UpdateColumns(map[string]any{"display_name": "人工标题", "title_source": "user", "title_revision": 1})
					return openingTestResult("ready"), nil
				}
			}
			if _, err := openingTestRun(t, s, "c1"); err != nil {
				t.Fatal(err)
			}
			var after orm.Conversation
			s.db.First(&after, "id = ?", "c1")
			expected := "对话整理方案"
			if source == "user" {
				expected = title
			}
			if source == "rename_during_call" {
				expected = "人工标题"
			}
			if after.DisplayName != expected || !after.UpdatedAt.Equal(before.UpdatedAt) {
				t.Fatalf("title/activity changed incorrectly: %+v", after)
			}
			m := openingTestMeta(t, s, "c1")
			if m.Summary == "" || !m.WindowClosed || m.CallCount != 1 {
				t.Fatalf("metadata: %+v", m)
			}
			openingTestInput(t, s, "h2", "c1", "顺便实现前端", 2)
			if queued, err := s.enqueue(context.Background(), "c1", ""); err != nil || queued {
				t.Fatalf("ready intent reopened: %v %v", queued, err)
			}
		})
	}
}
func TestOpeningCompletionAndEvidenceReplacement(t *testing.T) {
	s := openingTestService(t)
	openingTestConversation(t, s, "c1", "优化检索", "default")
	openingTestInput(t, s, "h1", "c1", "优化检索", 1)
	calls := 0
	s.call = func(context.Context, json.RawMessage, map[string]any, int) (algo.OpeningTaskResult, error) {
		calls++
		return openingTestResult("provisional"), nil
	}
	for turn := 1; turn <= 3; turn++ {
		if turn > 1 {
			openingTestInput(t, s, fmt.Sprintf("h%d", turn), "c1", fmt.Sprintf("补充对象 %d", turn), turn)
		}
		if queued, err := s.enqueue(context.Background(), "c1", ""); err != nil || !queued {
			t.Fatalf("enqueue: %v %v", queued, err)
		}
		if _, err := openingTestRun(t, s, "c1"); err != nil {
			t.Fatal(err)
		}
		if queued, err := s.enqueue(context.Background(), "c1", ""); err != nil || queued {
			t.Fatal("same input reran")
		}
	}
	if m := openingTestMeta(t, s, "c1"); !m.WindowClosed || m.GenerationCount != 3 || calls != 3 {
		t.Fatalf("completion budget: %+v", m)
	}
	s.db.Model(&orm.ChatHistory{}).Where("id = ?", "h1").Update("raw_content", "改为分析销售")
	if _, err := s.enqueue(context.Background(), "c1", ""); err != nil {
		t.Fatal(err)
	}
	s.call = func(context.Context, json.RawMessage, map[string]any, int) (algo.OpeningTaskResult, error) {
		s.db.Model(&orm.ChatHistory{}).Where("id = ?", "h1").Update("raw_content", "改为设计数据库")
		return openingTestResult("ready"), nil
	}
	if _, err := openingTestRun(t, s, "c1"); err != nil {
		t.Fatal(err)
	}
	m := openingTestMeta(t, s, "c1")
	if m.Status != "pending" || m.Summary != "" || m.GenerationCount != 0 || !strings.Contains(string(m.InputJSON), "设计数据库") {
		t.Fatalf("stale result accepted: %+v", m)
	}
}

func TestOpeningEmptyTurnsDoNotConsumeWindow(t *testing.T) {
	s := openingTestService(t)
	openingTestConversation(t, s, "c1", "新对话", "default")
	for i, text := range []string{"在吗", "嗯", "好的", "排查数据库连接池泄漏"} {
		openingTestInput(t, s, fmt.Sprintf("h%d", i+1), "c1", text, i+1)
	}
	calls := 0
	s.call = func(_ context.Context, input json.RawMessage, _ map[string]any, _ int) (algo.OpeningTaskResult, error) {
		calls++
		if !strings.Contains(string(input), "连接池泄漏") {
			return algo.OpeningTaskResult{Status: "succeeded", Output: algo.OpeningDescription{IntentStatus: "empty"}, Usage: json.RawMessage(`{"model_calls":1}`)}, nil
		}
		return openingTestResult("ready"), nil
	}
	if queued, err := s.enqueue(context.Background(), "c1", ""); err != nil || !queued {
		t.Fatal("initial opening was not queued", err)
	}
	if _, err := openingTestRun(t, s, "c1"); err != nil {
		t.Fatal(err)
	}
	meta := openingTestMeta(t, s, "c1")
	if meta.Status != "pending" || meta.GenerationCount != 0 || meta.OpeningTurns != 1 || !strings.Contains(string(meta.InputJSON), "连接池泄漏") {
		t.Fatalf("empty turns did not advance to the effective request: %+v input=%s", meta, meta.InputJSON)
	}
	if _, err := openingTestRun(t, s, "c1"); err != nil {
		t.Fatal(err)
	}
	meta = openingTestMeta(t, s, "c1")
	if !meta.WindowClosed || meta.GenerationCount != 1 || calls != 2 {
		t.Fatalf("effective request was not finalized: %+v calls=%d", meta, calls)
	}
}

func TestOpeningExplicitRetrySameConfiguration(t *testing.T) {
	foundInvalidOutput := false
	for _, code := range openingExplicitRetryErrorCodes {
		foundInvalidOutput = foundInvalidOutput || code == "invalid_output"
	}
	if !foundInvalidOutput {
		t.Fatal("invalid_output is missing from explicit retry candidates")
	}
	for _, code := range []string{"invalid_output", "model_failed", "transport_error", "request_timeout", "rate_limited", "service_unavailable"} {
		if !openingShouldRetry(code, "same", "same") {
			t.Fatalf("same configuration should allow explicit retry for %s", code)
		}
	}
	for _, code := range []string{"token_limit", "model_configuration", "authentication_failed", "invalid_request", "not_found"} {
		if openingShouldRetry(code, "same", "same") {
			t.Fatalf("same configuration should not retry deterministic failure %s", code)
		}
		if !openingShouldRetry(code, "old", "new") {
			t.Fatalf("configuration change should allow retry for %s", code)
		}
	}
}
func TestOpeningAttachmentArrivalAndLongInput(t *testing.T) {
	s := openingTestService(t)
	c := openingTestConversation(t, s, "c1", "看看这个", "default")
	openingTestInput(t, s, "h1", "c1", "看看这个", 1)
	s.db.Model(&orm.ChatHistory{}).Where("id = ?", "h1").Update("ext", json.RawMessage(`{"input":[{"input_type":"file","uri":"contract.pdf"}]}`))
	s.call = func(context.Context, json.RawMessage, map[string]any, int) (algo.OpeningTaskResult, error) {
		return openingTestResult("provisional"), nil
	}
	if _, err := s.enqueue(context.Background(), "c1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := openingTestRun(t, s, "c1"); err != nil {
		t.Fatal(err)
	}
	s.db.Model(&orm.ChatHistory{}).Where("id = ?", "h1").Update("ext", json.RawMessage(`{"input":[{"input_type":"file","uri":"contract.pdf","description":"软件开发合同，交付与验收条款"}]}`))
	if queued, err := s.enqueue(context.Background(), "c1", ""); err != nil || !queued {
		t.Fatal("description did not trigger completion", err)
	}
	if m := openingTestMeta(t, s, "c1"); m.GenerationCount != 1 {
		t.Fatal("description arrival reset budget")
	}
	long := strings.Repeat("资料内容 ", 15000) + "末尾要求：计算年度销售额"
	s.db.Model(&orm.ChatHistory{}).Where("id = ?", "h1").Update("raw_content", long)
	snap, err := loadOpeningSnapshot(s.db, c)
	if err != nil || !strings.Contains(string(snap.Input), long) {
		t.Fatal("long input truncated", err)
	}
}
func TestOpeningFallbackAndRetryBudget(t *testing.T) {
	for _, code := range []string{"token_limit", "transport_error", "authentication_failed"} {
		t.Run(code, func(t *testing.T) {
			s := openingTestService(t)
			openingTestConversation(t, s, "c1", "整理对话", "default")
			openingTestInput(t, s, "h1", "c1", "整理对话", 1)
			calls := []string{}
			s.call = func(_ context.Context, _ json.RawMessage, cfg map[string]any, _ int) (algo.OpeningTaskResult, error) {
				calls = append(calls, cfg["llm"].(map[string]any)["model"].(string))
				if code == "transport_error" {
					return algo.OpeningTaskResult{}, context.DeadlineExceeded
				}
				return algo.OpeningTaskResult{Status: "failed", ErrorCode: code}, nil
			}
			if _, err := s.enqueue(context.Background(), "c1", ""); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				result, err := openingTestRun(t, s, "c1")
				if err == nil {
					t.Fatal("expected failure")
				}
				if result.Permanent {
					break
				}
			}
			expected := 1
			if code == "token_limit" {
				expected = 2
			}
			if code == "transport_error" {
				expected = 3
			}
			if len(calls) != expected {
				t.Fatalf("calls: %v", calls)
			}
			if code == "token_limit" && calls[1] != "default" {
				t.Fatal("capacity fallback missing")
			}
			for _, model := range calls {
				if code == "transport_error" && model != "metadata" {
					t.Fatal("general error switched model")
				}
			}
			if m := openingTestMeta(t, s, "c1"); m.Status != "failed" || m.CallCount != expected || m.IntentStatus != "" {
				t.Fatalf("failure state: %+v", m)
			}
		})
	}
}

func TestOpeningSavedImageDescriptionAndCapacityFallback(t *testing.T) {
	s := openingTestService(t)
	c := openingTestConversation(t, s, "image", "看看这个", "default")
	openingTestInput(t, s, "image-h", c.ID, "看看这个", 1)
	s.db.Model(&orm.ChatHistory{}).Where("id = ?", "image-h").Updates(map[string]any{
		"ext":    json.RawMessage(`{"input":[{"input_type":"image","uri":"diagram.png"}]}`),
		"result": `<tool_result>{"name":"vision_extractor","result":{"description":"电商订单系统架构图","url":"/uploads/diagram.png"}}</tool_result>`,
	})
	snapshot, err := loadOpeningSnapshot(s.db, c)
	if err != nil || !strings.Contains(string(snapshot.Input), "电商订单系统架构图") || strings.Contains(string(snapshot.Input), "tool_result") {
		t.Fatal("saved description missing or tool log leaked", err, string(snapshot.Input))
	}
	if _, err := s.enqueue(context.Background(), c.ID, ""); err != nil {
		t.Fatal(err)
	}
	requests := 0
	s.call = func(context.Context, json.RawMessage, map[string]any, int) (algo.OpeningTaskResult, error) {
		requests++
		if requests == 1 {
			return algo.OpeningTaskResult{Status: "failed", ErrorCode: "token_limit", Usage: json.RawMessage(`{"model_calls":0}`)}, nil
		}
		return openingTestResult("ready"), nil
	}
	if result, err := openingTestRun(t, s, c.ID); err == nil || result.Permanent {
		t.Fatal("capacity failure should schedule fallback", result, err)
	}
	if _, err := openingTestRun(t, s, c.ID); err != nil {
		t.Fatal(err)
	}
	if meta := openingTestMeta(t, s, c.ID); meta.Status != "done" || meta.CallCount != 1 {
		t.Fatal("preflight must not count as a model call", meta)
	}
}
func TestOpeningBackfillDedupPauseAndUserIsolation(t *testing.T) {
	s := openingTestService(t)
	store.Init(s.db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	openingTestConversation(t, s, "c1", "整理对话", "unknown")
	openingTestInput(t, s, "h1", "c1", "整理对话", 1)
	for _, action := range []string{"start", "start", "pause", "resume"} {
		recorder := httptest.NewRecorder()
		OpeningBackfill(recorder, newSettingsRequest("POST", "/conversations/metadata-backfill", `{"action":"`+action+`"}`, "u1", nil))
		if recorder.Code != 200 {
			t.Fatal(recorder.Body.String())
		}
	}
	var count int64
	s.db.Model(&orm.ConversationOpeningBackfill{}).Count(&count)
	if count != 1 {
		t.Fatal("duplicate batch")
	}
	var batch orm.ConversationOpeningBackfill
	s.db.First(&batch)
	job := asyncjob.Job{ResourceID: batch.ID, ResourceType: "opening_backfill", CreateUserID: "u1"}
	if _, err := s.backfill(context.Background(), job, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backfill(context.Background(), job, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := openingTestRun(t, s, "c1"); err != nil {
		t.Fatal(err)
	}
	if m := openingTestMeta(t, s, "c1"); !m.WindowClosed || m.CallCount != 1 {
		t.Fatal("history reran")
	}
	rec := httptest.NewRecorder()
	openingTestConversation(t, s, "live", "优化检索", "default")
	openingTestInput(t, s, "live-h", "live", "优化检索", 1)
	if _, err := s.enqueue(context.Background(), "live", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := openingTestRun(t, s, "live"); err != nil {
		t.Fatal(err)
	}
	OpeningBackfill(rec, newSettingsRequest("GET", "/conversations/metadata-backfill", "", "u2", nil))
	if rec.Code != 200 || strings.Contains(rec.Body.String(), batch.ID) || strings.Contains(rec.Body.String(), "对话整理") || !strings.Contains(rec.Body.String(), `"completed":0`) {
		t.Fatalf("user isolation: %s", rec.Body.String())
	}
}

func TestOpeningEligibilityAndArchiveDuringCall(t *testing.T) {
	for _, field := range []string{"archived_at", "deleted_at", "is_ephemeral", "chat_executor"} {
		t.Run(field, func(t *testing.T) {
			s := openingTestService(t)
			openingTestConversation(t, s, "excluded", "整理对话", "default")
			openingTestInput(t, s, "h1", "excluded", "整理对话", 1)
			var value any = time.Now()
			if field == "is_ephemeral" {
				value = true
			}
			if field == "chat_executor" {
				value = "external"
			}
			s.db.Model(&orm.Conversation{}).Where("id = ?", "excluded").UpdateColumn(field, value)
			if queued, err := s.enqueue(context.Background(), "excluded", ""); err != nil || queued {
				t.Fatal("ineligible conversation enqueued", queued, err)
			}
		})
	}
	s := openingTestService(t)
	openingTestConversation(t, s, "archive", "整理对话", "default")
	openingTestInput(t, s, "h1", "archive", "整理对话", 1)
	s.enqueue(context.Background(), "archive", "")
	s.call = func(context.Context, json.RawMessage, map[string]any, int) (algo.OpeningTaskResult, error) {
		s.db.Model(&orm.Conversation{}).Where("id = ?", "archive").UpdateColumn("archived_at", time.Now())
		return openingTestResult("ready"), nil
	}
	if _, err := openingTestRun(t, s, "archive"); err != nil {
		t.Fatal(err)
	}
	if meta := openingTestMeta(t, s, "archive"); meta.Status != "skipped" || meta.Summary != "" {
		t.Fatal("archived result saved", meta)
	}
}

func TestOpeningBackfillReadsOriginalHistoryAfterCompression(t *testing.T) {
	s := openingTestService(t)
	conv := openingTestConversation(t, s, "compressed", "设计销售报表", "unknown")
	inputs := []string{"设计销售报表", "按月份汇总", "只给方案，暂不实施", "后来改为排查登录故障"}
	for i, input := range inputs {
		openingTestInput(t, s, fmt.Sprintf("compressed-h%d", i+1), conv.ID, input, i+1)
	}
	handleModelContextUpdated(t.Context(), s.db, conv.ID, &ModelContextUpdatedEvent{
		SummaryText: "当前正在排查登录故障", CoveredThroughSeq: 3, Version: 1,
	})
	modelContext := loadModelContext(t.Context(), s.db, conv.ID)
	if modelContext == nil || modelContext.CoveredThroughSeq != 3 {
		t.Fatal("compressed context was not persisted", modelContext)
	}
	var histories []orm.ChatHistory
	if err := s.db.Where("conversation_id = ?", conv.ID).Order("seq").Find(&histories).Error; err != nil {
		t.Fatal(err)
	}
	if len(histories) != 4 || len(buildModelHistoryMessages(histories, nil, modelContext)) != 3 {
		t.Fatal("expected original history plus a separate compressed model view")
	}
	s.call = func(_ context.Context, input json.RawMessage, _ map[string]any, _ int) (algo.OpeningTaskResult, error) {
		for _, original := range inputs[:3] {
			if !strings.Contains(string(input), original) {
				t.Fatalf("opening message lost after compression: %s", input)
			}
		}
		if strings.Contains(string(input), "登录故障") {
			t.Fatalf("compressed summary or later task entered opening: %s", input)
		}
		return openingTestResult("ready"), nil
	}
	if queued, err := s.enqueue(t.Context(), conv.ID, "history-batch"); err != nil || !queued {
		t.Fatal("historical opening was not queued", err)
	}
	if _, err := openingTestRun(t, s, conv.ID); err != nil {
		t.Fatal(err)
	}
	if meta := openingTestMeta(t, s, conv.ID); meta.Status != "done" || meta.OpeningTurns != 3 || meta.CallCount != 1 {
		t.Fatal("compressed history backfill failed", meta)
	}
	handleModelContextUpdated(t.Context(), s.db, conv.ID, &ModelContextUpdatedEvent{
		SummaryText: "登录故障已经修复", CoveredThroughSeq: 4, Version: 1,
	})
	if queued, err := s.enqueue(t.Context(), conv.ID, "history-batch"); err != nil || queued {
		t.Fatal("later compression reopened the original intent", err)
	}
}

func TestOpeningRealModelDescription(t *testing.T) {
	url := os.Getenv("OPENING_MODEL_URL")
	if url == "" {
		t.Skip("opt-in real model acceptance")
	}
	s := openingTestService(t)
	s.loadConfig = func(context.Context, *gorm.DB, string) (map[string]any, error) {
		model := os.Getenv("OPENING_MODEL_NAME")
		return map[string]any{
			"conversation_metadata": map[string]any{"source": "openai", "model": model, "base_url": url, "skip_auth": true},
		}, nil
	}
	s.call = algo.DescribeConversationOpening
	openingTestConversation(t, s, "real", "解释倒排索引", "default")
	openingTestInput(t, s, "real-h", "real", "解释倒排索引的基本工作原理", 1)
	if _, err := s.enqueue(context.Background(), "real", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := openingTestRun(t, s, "real"); err != nil {
		t.Fatal(err)
	}
	meta := openingTestMeta(t, s, "real")
	if meta.Status != "done" || meta.CallCount != 1 || !strings.Contains(meta.Summary, "倒排索引") {
		t.Fatal("real description failed", meta)
	}
	t.Logf("real description: calls=%d summary=%s usage=%s", meta.CallCount, meta.Summary, meta.UsageJSON)
}
