package agent

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestLoadRouterTrafficStats(t *testing.T) {
	db := newAgentTestDB(t)
	if err := db.AutoMigrate(&orm.Conversation{}, &orm.ChatHistory{}); err != nil {
		t.Fatalf("auto migrate traffic tables: %v", err)
	}

	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	conversations := []orm.Conversation{
		trafficConversation("c1", "u1", false, start),
		trafficConversation("c2", "u1", false, start),
		trafficConversation("c3", "u2", true, start),
		trafficConversation("c4", "u2", false, start),
	}
	for i := range conversations {
		if err := db.Create(&conversations[i]).Error; err != nil {
			t.Fatalf("create conversation: %v", err)
		}
	}

	histories := []orm.ChatHistory{
		trafficHistory("h1", "c1", "algorithm-a", 1, "", start.Add(time.Hour)),
		trafficHistory("h2", "c2", "algorithm-a", 2, "inaccurate", start.Add(2*time.Hour)),
		trafficHistory("h3", "c1", "algorithm-b", 0, "", start.Add(25*time.Hour)),
		trafficHistory("h4", "c4", "algorithm-b", 0, "", start.Add(26*time.Hour)),
		trafficHistory("task", "c3", "algorithm-a", 1, "", start.Add(3*time.Hour)),
		trafficHistory("legacy", "c4", "", 1, "", start.Add(4*time.Hour)),
		trafficHistory("outside", "c4", "algorithm-a", 1, "", start.Add(-time.Hour)),
	}
	for i := range histories {
		if err := db.Create(&histories[i]).Error; err != nil {
			t.Fatalf("create chat history: %v", err)
		}
	}

	load := func() RouterTrafficStatsResponse {
		result, err := loadRouterTrafficStats(context.Background(), db.DB, start, start.Add(48*time.Hour), "day")
		if err != nil {
			t.Fatalf("load traffic stats: %v", err)
		}
		return result
	}
	result := load()
	if result.Summary.AnswerCount != 4 || result.Summary.UserCount != 2 || result.Summary.ConversationCount != 3 {
		t.Fatalf("unexpected summary: %#v", result.Summary)
	}
	if result.Summary.FeedbackCount != 2 || result.Summary.FeedbackRate != 0.5 {
		t.Fatalf("unexpected feedback summary: %#v", result.Summary)
	}
	if len(result.Algorithms) != 2 {
		t.Fatalf("unexpected algorithms: %#v", result.Algorithms)
	}
	algorithmA, algorithmB := result.Algorithms[0], result.Algorithms[1]
	if algorithmA.AlgorithmID != "algorithm-a" || algorithmA.AnswerCount != 2 || algorithmA.UserCount != 1 || algorithmA.ConversationCount != 2 {
		t.Fatalf("unexpected algorithm-a stats: %#v", algorithmA)
	}
	if algorithmA.LikeCount != 1 || algorithmA.DislikeCount != 1 || algorithmA.PositiveRate == nil || *algorithmA.PositiveRate != 0.5 {
		t.Fatalf("unexpected algorithm-a feedback: %#v", algorithmA)
	}
	if algorithmB.AlgorithmID != "algorithm-b" || algorithmB.UserCount != 2 || algorithmB.PositiveRate != nil {
		t.Fatalf("unexpected algorithm-b stats: %#v", algorithmB)
	}
	if len(result.Trend) != 2 || result.Trend[0].Counts["algorithm-a"] != 2 || result.Trend[1].Counts["algorithm-b"] != 2 {
		t.Fatalf("unexpected trend: %#v", result.Trend)
	}
	if len(result.DislikeReasons) != 1 || result.DislikeReasons[0].Reason != "inaccurate" || result.DislikeReasons[0].Ratio != 1 {
		t.Fatalf("unexpected dislike reasons: %#v", result.DislikeReasons)
	}

	if err := db.Model(&orm.ChatHistory{}).Where("id = ?", "h3").Updates(map[string]any{"feed_back": 1, "reason": ""}).Error; err != nil {
		t.Fatalf("like h3: %v", err)
	}
	result = load()
	if result.Summary.FeedbackCount != 3 || result.Algorithms[1].LikeCount != 1 || result.Algorithms[1].PositiveRate == nil || *result.Algorithms[1].PositiveRate != 1 {
		t.Fatalf("unexpected stats after like: %#v", result)
	}

	if err := db.Model(&orm.ChatHistory{}).Where("id = ?", "h3").Updates(map[string]any{"feed_back": 2, "reason": "slow"}).Error; err != nil {
		t.Fatalf("dislike h3: %v", err)
	}
	result = load()
	if result.Algorithms[1].LikeCount != 0 || result.Algorithms[1].DislikeCount != 1 || result.Algorithms[1].PositiveRate == nil || *result.Algorithms[1].PositiveRate != 0 {
		t.Fatalf("unexpected stats after dislike: %#v", result.Algorithms[1])
	}
	if len(result.DislikeReasons) != 2 || result.DislikeReasons[1].Reason != "slow" {
		t.Fatalf("unexpected reasons after dislike: %#v", result.DislikeReasons)
	}

	if err := db.Model(&orm.ChatHistory{}).Where("id = ?", "h3").Updates(map[string]any{"feed_back": 0, "reason": ""}).Error; err != nil {
		t.Fatalf("cancel feedback for h3: %v", err)
	}
	result = load()
	if result.Summary.FeedbackCount != 2 || result.Algorithms[1].LikeCount != 0 || result.Algorithms[1].DislikeCount != 0 || result.Algorithms[1].PositiveRate != nil {
		t.Fatalf("unexpected stats after feedback cancellation: %#v", result)
	}
}

func TestParseRouterTrafficRange(t *testing.T) {
	request := httptest.NewRequest("GET", "/?start_time=2026-08-01T08:00:00%2B08:00&end_time=2026-08-02T08:00:00%2B08:00&granularity=hour", nil)
	start, end, granularity, err := parseRouterTrafficRange(request)
	if err != nil {
		t.Fatalf("parse range: %v", err)
	}
	if start.Format(time.RFC3339) != "2026-08-01T00:00:00Z" || end.Format(time.RFC3339) != "2026-08-02T00:00:00Z" || granularity != "hour" {
		t.Fatalf("unexpected range: %s %s %s", start, end, granularity)
	}

	invalid := httptest.NewRequest("GET", "/?start_time=2026-08-02T00:00:00Z&end_time=2026-08-01T00:00:00Z&granularity=week", nil)
	if _, _, _, err := parseRouterTrafficRange(invalid); err == nil {
		t.Fatal("expected invalid range error")
	}
}

func TestLoadRouterTrafficStatsCountsRegenerationAttempts(t *testing.T) {
	db := newAgentTestDB(t)
	if err := db.AutoMigrate(&orm.Conversation{}, &orm.ChatHistory{}); err != nil {
		t.Fatalf("auto migrate traffic tables: %v", err)
	}
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	conversation := trafficConversation("c1", "u1", false, start)
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	ext, err := json.Marshal(map[string]any{
		"router_traffic_attempts": []routerTrafficAttempt{
			{AlgorithmID: "algorithm-a", FeedBack: 1, CreateTime: start.Add(time.Hour)},
			{AlgorithmID: "algorithm-b", FeedBack: 2, Reason: "slow", CreateTime: start.Add(2 * time.Hour)},
		},
	})
	if err != nil {
		t.Fatalf("marshal attempts: %v", err)
	}
	history := trafficHistory("h1", "c1", "algorithm-c", 2, "inaccurate", start.Add(3*time.Hour))
	history.Ext = ext
	if err := db.Create(&history).Error; err != nil {
		t.Fatalf("create regenerated history: %v", err)
	}

	result, err := loadRouterTrafficStats(context.Background(), db.DB, start, start.Add(24*time.Hour), "hour")
	if err != nil {
		t.Fatalf("load traffic stats: %v", err)
	}
	if result.Summary.AnswerCount != 3 || result.Summary.FeedbackCount != 3 || result.Summary.UserCount != 1 || result.Summary.ConversationCount != 1 {
		t.Fatalf("unexpected summary: %#v", result.Summary)
	}
	if len(result.Algorithms) != 3 {
		t.Fatalf("expected three algorithms, got %#v", result.Algorithms)
	}
	for _, algorithm := range result.Algorithms {
		if algorithm.AnswerCount != 1 {
			t.Fatalf("regeneration was not counted independently: %#v", algorithm)
		}
	}
	if len(result.Trend) != 3 || len(result.DislikeReasons) != 2 {
		t.Fatalf("unexpected attempt details: trend=%#v reasons=%#v", result.Trend, result.DislikeReasons)
	}
}

func trafficConversation(id, userID string, task bool, now time.Time) orm.Conversation {
	return orm.Conversation{
		ID: id, DisplayName: id, IsTaskConv: task,
		BaseModel: orm.BaseModel{
			CreateUserID: userID, CreateUserName: userID, CreatedAt: now, UpdatedAt: now,
		},
	}
}

func trafficHistory(id, conversationID, algorithmID string, feedback int, reason string, now time.Time) orm.ChatHistory {
	return orm.ChatHistory{
		ID: id, Seq: 1, ConversationID: conversationID, AlgorithmID: algorithmID,
		RawContent: "question", Content: "question", Result: "answer", FeedBack: feedback, Reason: reason,
		TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}
}
