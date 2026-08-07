package chat

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"lazymind/core/common/orm"
	corestore "lazymind/core/store"
)

func TestListConversationsHidesExternalAgentBindings(t *testing.T) {
	db, err := orm.Connect(
		orm.DriverSQLite,
		filepath.Join(t.TempDir(), "conversation-list.db"),
	)
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&orm.Conversation{},
		&orm.ChatHistory{},
		&orm.ExternalAgentBinding{},
	); err != nil {
		t.Fatalf("migrate models: %v", err)
	}
	corestore.Init(db.DB, nil, nil)
	t.Cleanup(func() { corestore.Init(nil, nil, nil) })

	now := time.Now().UTC()
	for _, conversation := range []orm.Conversation{
		{
			ID:          "normal-conversation",
			DisplayName: "Normal",
			ChannelID:   "default",
			BaseModel: orm.BaseModel{
				CreateUserID:   "user-1",
				CreateUserName: "User",
				CreatedAt:      now,
				UpdatedAt:      now,
			},
		},
		{
			ID:          "external-conversation",
			DisplayName: "Codex",
			ChannelID:   "default",
			BaseModel: orm.BaseModel{
				CreateUserID:   "user-1",
				CreateUserName: "User",
				CreatedAt:      now,
				UpdatedAt:      now,
			},
		},
	} {
		if err := db.Create(&conversation).Error; err != nil {
			t.Fatalf("create conversation %s: %v", conversation.ID, err)
		}
	}
	binding := orm.ExternalAgentBinding{
		ID:                "binding-1",
		ConversationID:    "external-conversation",
		Provider:          "codex",
		ProviderThreadID:  "thread-1",
		ManagedByLazyMind: false,
		CreatedByUserID:   "user-1",
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatalf("create binding: %v", err)
	}

	request := httptest.NewRequest("GET", "/conversations", nil)
	request.Header.Set("X-User-Id", "user-1")
	response := httptest.NewRecorder()
	ListConversations(response, request)

	if response.Code != 200 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Conversations []struct {
			ConversationID string `json:"conversation_id"`
		} `json:"conversations"`
		TotalSize int `json:"total_size"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.TotalSize != 1 || len(payload.Conversations) != 1 {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	if payload.Conversations[0].ConversationID != "normal-conversation" {
		t.Fatalf(
			"conversation=%q, want normal-conversation",
			payload.Conversations[0].ConversationID,
		)
	}
}
