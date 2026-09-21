package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestConversationPreviewSummaryIsScopedAndDoesNotChangeActivity(t *testing.T) {
	db := newPromptTestDB(t).DB
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	before := time.Date(2026, 9, 17, 8, 30, 0, 0, time.UTC)
	for _, id := range []string{"saved", "empty", "foreign", "wrong-owner"} {
		owner := "u1"
		if id == "foreign" {
			owner = "u2"
		}
		if err := db.Create(&orm.Conversation{ID: id, DisplayName: id, BaseModel: orm.BaseModel{CreateUserID: owner, CreatedAt: before, UpdatedAt: before}}).Error; err != nil {
			t.Fatal(err)
		}
		if id == "empty" {
			continue
		}
		if id == "wrong-owner" {
			owner = "u2"
		}
		metadata := orm.ConversationOpening{ConversationID: id, UserID: owner, Summary: "summary-" + id, Status: "running", InputJSON: json.RawMessage(`{}`), SourceHistoryIDs: json.RawMessage(`[]`)}
		if err := db.Create(&metadata).Error; err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/core/conversations", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()
	ListConversations(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Conversations []struct {
			ID      string `json:"conversation_id"`
			Summary string `json:"summary"`
			Pending bool   `json:"metadata_pending"`
			Updated string `json:"update_time"`
		} `json:"conversations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Conversations) != 3 {
		t.Fatalf("items=%+v", response.Conversations)
	}
	for _, item := range response.Conversations {
		want := ""
		if item.ID == "saved" {
			want = "summary-saved"
		}
		if item.Summary != want || item.Pending != (item.ID == "saved") || item.Updated != before.Format(time.RFC3339) {
			t.Fatalf("unexpected preview=%+v", item)
		}
	}
}
