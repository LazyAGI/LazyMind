package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"

	"github.com/gorilla/mux"
)

func TestGetConversationTrailReadsPersistedReferenceMetadata(t *testing.T) {
	db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/conversation-trail.db")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.Conversation{}, &orm.ChatHistory{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{
		ID:          "conv-trail",
		DisplayName: "Trail",
		ChannelID:   "default",
		BaseModel: orm.BaseModel{
			CreateUserID:   "u1",
			CreateUserName: "User 1",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	histories := []orm.ChatHistory{
		{
			ID: "h-1", Seq: 1, ConversationID: "conv-trail", RawContent: "先梳理现有知识库问题",
			TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
		},
		{
			ID: "h-2", Seq: 2, ConversationID: "conv-trail",
			RawContent: "这是一个超过十五个字符的长问题摘要，用于验证截断行为",
			Ext:        json.RawMessage(`{"trail":{"parent_history_id":"h-1","depth":3,"source":"reference"}}`),
			TimeMixin:  orm.TimeMixin{CreateTime: now.Add(time.Minute), UpdateTime: now.Add(time.Minute)},
		},
		{
			ID: "h-3", Seq: 3, ConversationID: "conv-trail", RawContent: "普通的新问题",
			TimeMixin: orm.TimeMixin{CreateTime: now.Add(2 * time.Minute), UpdateTime: now.Add(2 * time.Minute)},
		},
	}
	if err := db.Create(&histories).Error; err != nil {
		t.Fatalf("create histories: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/conversations/conv-trail:trail?page_size=100", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"name": "conv-trail:trail"})
	rec := httptest.NewRecorder()

	GetConversationTrail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp ConversationTrailListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.TotalSize != 3 || len(resp.Items) != 3 {
		t.Fatalf("unexpected item count: total=%d items=%d", resp.TotalSize, len(resp.Items))
	}
	if resp.Items[0].HistoryID != "h-1" || resp.Items[1].HistoryID != "h-2" || resp.Items[2].HistoryID != "h-3" {
		t.Fatalf("items not ordered by seq ascending: %#v", resp.Items)
	}
	if resp.Items[1].Depth != 1 || resp.Items[1].ParentHistoryID != "h-1" || resp.Items[1].Source != "reference" {
		t.Fatalf("persisted trail metadata not applied: %#v", resp.Items[1])
	}
	if len([]rune(resp.Items[1].Summary)) > 15 {
		t.Fatalf("summary exceeds 15 runes: %q", resp.Items[1].Summary)
	}
	if resp.Items[1].Question == resp.Items[1].Summary {
		t.Fatalf("question should remain available for delayed tooltip: %#v", resp.Items[1])
	}
	if resp.Items[2].Depth != 0 || resp.Items[2].ParentHistoryID != "" {
		t.Fatalf("ordinary turn should remain a root node: %#v", resp.Items[2])
	}
}

func TestGetConversationTrailRequiresConversationOwner(t *testing.T) {
	db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/conversation-trail-owner.db")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.Conversation{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{
		ID:        "private-conv",
		ChannelID: "default",
		BaseModel: orm.BaseModel{
			CreateUserID:   "owner",
			CreateUserName: "Owner",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/conversations/private-conv:trail", nil)
	req.Header.Set("X-User-Id", "other")
	req = mux.SetURLVars(req, map[string]string{"name": "private-conv:trail"})
	rec := httptest.NewRecorder()

	GetConversationTrail(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSummarizeConversationTrailQuestionUsesAtMostFifteenRunes(t *testing.T) {
	if got := summarizeConversationTrailQuestion("一二三四五六七八九十"); got != "一二三四五六七八九十" {
		t.Fatalf("short summary = %q", got)
	}
	got := summarizeConversationTrailQuestion("一二三四五六七八九十一二三四五")
	if len([]rune(got)) != 15 {
		t.Fatalf("long summary has %d runes: %q", len([]rune(got)), got)
	}
}

func TestBuildChatHistoryExtWithTrailOnlyWritesExplicitReferences(t *testing.T) {
	histories := []orm.ChatHistory{
		{ID: "h-root", Seq: 1, Ext: json.RawMessage(`{"input":[]}`)},
		{ID: "h-child", Seq: 2, Ext: json.RawMessage(`{"trail":{"parent_history_id":"h-root","depth":1}}`)},
	}

	ordinary := buildChatHistoryExtWithTrail(
		map[string]any{"input": []any{map[string]any{"input_type": "text", "text": "继续优化这个问题"}}},
		"继续优化这个问题",
		histories,
		chatPersistTarget{Seq: 3},
	)
	var ordinaryPayload map[string]any
	if err := json.Unmarshal(ordinary, &ordinaryPayload); err != nil {
		t.Fatalf("decode ordinary ext: %v", err)
	}
	if _, ok := ordinaryPayload["trail"]; ok {
		t.Fatalf("ordinary turn must not infer trail metadata: %#v", ordinaryPayload)
	}

	referenced := buildChatHistoryExtWithTrail(
		map[string]any{
			"input":            []any{map[string]any{"input_type": "text", "text": "<cite_message>引用回答</cite_message>继续优化"}},
			"cite_history_ids": []any{"h-child"},
		},
		"<cite_message>引用回答</cite_message>继续优化",
		histories,
		chatPersistTarget{Seq: 3},
	)
	var referencedPayload struct {
		Trail conversationTrailMetadata `json:"trail"`
	}
	if err := json.Unmarshal(referenced, &referencedPayload); err != nil {
		t.Fatalf("decode referenced ext: %v", err)
	}
	if referencedPayload.Trail.ParentHistoryID != "h-child" || referencedPayload.Trail.Depth != 1 || referencedPayload.Trail.Source != "reference" {
		t.Fatalf("unexpected reference trail metadata: %#v", referencedPayload.Trail)
	}

	staleReference := buildChatHistoryExtWithTrail(
		map[string]any{
			"input":            []any{map[string]any{"input_type": "text", "text": "引用已删除的问题"}},
			"cite_history_ids": []any{"missing-history"},
		},
		"引用已删除的问题",
		histories,
		chatPersistTarget{Seq: 3},
	)
	var stalePayload struct {
		Trail conversationTrailMetadata `json:"trail"`
	}
	if err := json.Unmarshal(staleReference, &stalePayload); err != nil {
		t.Fatalf("decode stale reference ext: %v", err)
	}
	if stalePayload.Trail.ParentHistoryID != "" {
		t.Fatalf("stale source ID must not attach to the latest turn: %#v", stalePayload.Trail)
	}
}

func TestBuildChatHistoryExtWithTrailPreservesRegeneratedRelationship(t *testing.T) {
	existing := orm.ChatHistory{
		ID:  "h-child",
		Ext: json.RawMessage(`{"trail":{"parent_history_id":"h-root","depth":1,"source":"reference"}}`),
	}
	ext := buildChatHistoryExtWithTrail(
		map[string]any{"input": []any{map[string]any{"input_type": "text", "text": "重新生成"}}},
		"重新生成",
		[]orm.ChatHistory{{ID: "h-root"}, existing},
		chatPersistTarget{Seq: 2, Existing: &existing, IsRegeneration: true},
	)
	var payload struct {
		Trail conversationTrailMetadata `json:"trail"`
	}
	if err := json.Unmarshal(ext, &payload); err != nil {
		t.Fatalf("decode regenerated ext: %v", err)
	}
	if payload.Trail.ParentHistoryID != "h-root" || payload.Trail.Depth != 1 {
		t.Fatalf("regeneration lost trail metadata: %#v", payload.Trail)
	}
}
