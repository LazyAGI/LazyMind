package evolution

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type managedStateListAPITestResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Items []struct {
			ResourceID                  string  `json:"resource_id"`
			ResourceType                string  `json:"resource_type"`
			Title                       string  `json:"title"`
			Content                     string  `json:"content"`
			AgentPersona                *string `json:"agent_persona"`
			UserAddress                 *string `json:"user_address"`
			ResponseStyle               *string `json:"response_style"`
			ContentSummary              string  `json:"content_summary"`
			HasPendingReviewSuggestions bool    `json:"has_pending_review_suggestions"`
			SuggestionStatus            string  `json:"suggestion_status"`
		} `json:"items"`
	} `json:"data"`
}

func TestListManagedStatesReturnsDefaultsAndUserScopedRows(t *testing.T) {
	db := newTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	memory := orm.SystemMemory{
		ID:            "memory-1",
		UserID:        "u1",
		Content:       "倾向于先结论后论证，遇到风险点时优先列出明确建议。",
		AgentPersona:  "严谨助手",
		UserAddress:   "老师",
		ResponseStyle: "先结论后论证",
		Version:       1,
		UpdatedBy:     "u1",
		UpdatedByName: "User 1",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	memory.ContentHash = HashSystemMemory(memory)
	if err := db.Create(&memory).Error; err != nil {
		t.Fatalf("create memory: %v", err)
	}
	suggestions := []orm.ResourceSuggestion{
		{
			ID:           "memory-suggestion-1",
			UserID:       "u1",
			ResourceType: ResourceTypeMemory,
			ResourceKey:  SystemResourceKey(ResourceTypeMemory),
			Action:       SuggestionActionModify,
			SessionID:    "session-memory-1",
			Title:        "memory suggestion",
			Content:      "补充新的记忆意见",
			Status:       SuggestionStatusPendingReview,
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:           "preference-suggestion-1",
			UserID:       "u1",
			ResourceType: ResourceTypeUserPreference,
			ResourceKey:  SystemResourceKey(ResourceTypeUserPreference),
			Action:       SuggestionActionModify,
			SessionID:    "session-preference-1",
			Title:        "preference suggestion",
			Content:      "补充新的偏好意见",
			Status:       SuggestionStatusAccepted,
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}
	if err := db.Create(&suggestions).Error; err != nil {
		t.Fatalf("create suggestions: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/personalization-items", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	ListManagedStates(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp managedStateListAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	if len(resp.Data.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(resp.Data.Items))
	}

	memoryItem := resp.Data.Items[0]
	if memoryItem.ResourceType != ResourceTypeMemory {
		t.Fatalf("expected first item to be memory, got %q", memoryItem.ResourceType)
	}
	if memoryItem.Title != ManagedMemoryTitle {
		t.Fatalf("expected memory title %q, got %q", ManagedMemoryTitle, memoryItem.Title)
	}
	if memoryItem.ContentSummary == "" {
		t.Fatalf("expected memory content summary")
	}
	if stringValue(memoryItem.AgentPersona) != "严谨助手" || stringValue(memoryItem.UserAddress) != "老师" || stringValue(memoryItem.ResponseStyle) != "先结论后论证" {
		t.Fatalf("unexpected memory metadata: %#v", memoryItem)
	}
	if !memoryItem.HasPendingReviewSuggestions {
		t.Fatalf("expected memory item to show pending review suggestions")
	}
	if memoryItem.SuggestionStatus != SuggestionStatusPendingReview {
		t.Fatalf("expected memory suggestion_status pending_review, got %q", memoryItem.SuggestionStatus)
	}

	preferenceItem := resp.Data.Items[1]
	if preferenceItem.ResourceType != ResourceTypeUserPreference {
		t.Fatalf("expected second item to be preference, got %q", preferenceItem.ResourceType)
	}
	if preferenceItem.Title != ManagedPreferenceTitle {
		t.Fatalf("expected preference title %q, got %q", ManagedPreferenceTitle, preferenceItem.Title)
	}
	if preferenceItem.ResourceID != "" || preferenceItem.Content != "" || preferenceItem.ContentSummary != "" {
		t.Fatalf("expected empty default preference item, got %#v", preferenceItem)
	}
	if !preferenceItem.HasPendingReviewSuggestions {
		t.Fatalf("expected preference item to show pending review suggestions")
	}
	if preferenceItem.SuggestionStatus != SuggestionStatusAccepted {
		t.Fatalf("expected preference suggestion_status accepted, got %q", preferenceItem.SuggestionStatus)
	}

	var preferenceCount int64
	if err := db.Model(&orm.SystemUserPreference{}).Count(&preferenceCount).Error; err != nil {
		t.Fatalf("count preferences: %v", err)
	}
	if preferenceCount != 0 {
		t.Fatalf("expected list endpoint to not create missing preference row, got %d", preferenceCount)
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
