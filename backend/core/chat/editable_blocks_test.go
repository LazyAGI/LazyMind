package chat

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestReplaceEditableBlockOnlyChangesMatchingFence(t *testing.T) {
	input := "before\n```text\nkeep\n```\n```editable\nold\n```\nafter"
	got, ok := replaceEditableBlock(input, "old", "**new**")
	if !ok || got != "before\n```text\nkeep\n```\n```editable\n**new**\n```\nafter" {
		t.Fatalf("replaceEditableBlock() = %q, %v", got, ok)
	}
}

func TestPatchEditableBlockPersistsAndRejectsStaleContent(t *testing.T) {
	database := newPromptTestDB(t)
	store.Init(database.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now()
	conversation := orm.Conversation{
		ID: "conv-editable",
		BaseModel: orm.BaseModel{
			CreateUserID: "u1", CreateUserName: "User", CreatedAt: now, UpdatedAt: now,
		},
	}
	history := orm.ChatHistory{
		ID: "history-editable", ConversationID: conversation.ID, Seq: 1,
		Result:    "Here:\n```editable\nold text\n```",
		TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}
	if err := database.DB.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.DB.Create(&history).Error; err != nil {
		t.Fatal(err)
	}

	request := func(baseContent, content string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{
			"conversation_id": conversation.ID,
			"history_id":      history.ID,
			"base_content":    baseContent,
			"content":         content,
		})
		req := httptest.NewRequest(http.MethodPatch, "/api/core/conversations:editable-block", bytes.NewReader(body))
		req.Header.Set("X-User-Id", "u1")
		rec := httptest.NewRecorder()
		PatchEditableBlock(rec, req)
		return rec
	}

	if rec := request("old text", "new text"); rec.Code != http.StatusOK {
		t.Fatalf("save status=%d body=%s", rec.Code, rec.Body.String())
	} else if !strings.Contains(rec.Body.String(), `"result":"Here:\n`+"```editable"+`\nnew text\n`+"```"+`"`) {
		t.Fatalf("save response does not return the updated assistant result: %s", rec.Body.String())
	}
	if err := database.DB.First(&history, "id = ?", history.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(history.Result, "```editable\nnew text\n```") {
		t.Fatalf("editable content was not persisted: %q", history.Result)
	}
	if rec := request("old text", "stale write"); rec.Code != http.StatusConflict {
		t.Fatalf("stale save status=%d body=%s", rec.Code, rec.Body.String())
	}
}
