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
			ResourceID             string  `json:"resource_id"`
			ResourceType           string  `json:"resource_type"`
			Title                  string  `json:"title"`
			Content                string  `json:"content"`
			AgentPersona           *string `json:"agent_persona"`
			UserAddress            *string `json:"user_address"`
			ResponseStyle          *string `json:"response_style"`
			ContentSummary         string  `json:"content_summary"`
			HasPendingReviewResult bool    `json:"has_pending_review_result"`
			ReviewStatus           string  `json:"review_status"`
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
	preference := orm.SystemUserPreference{
		ID:            "preference-1",
		UserID:        "u1",
		Content:       "用户偏好简洁回答。",
		AgentPersona:  "严谨助手",
		UserAddress:   "老师",
		ResponseStyle: "先结论后论证",
		Version:       1,
		UpdatedBy:     "u1",
		UpdatedByName: "User 1",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	preference.ContentHash = HashSystemUserPreference(preference)
	if err := db.Create(&preference).Error; err != nil {
		t.Fatalf("create preference: %v", err)
	}
	suggestions := []orm.ResourceSuggestion{
		{
			ID:           "memory-suggestion-1",
			UserID:       "u1",
			ResourceType: ResourceTypeMemory,
			ResourceKey:  SystemResourceKey(ResourceTypeMemory),
			Action:       "modify",
			SessionID:    "session-memory-1",
			Title:        "memory suggestion",
			Content:      "补充新的记忆意见",
			Status:       "pending_review",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:           "preference-suggestion-1",
			UserID:       "u1",
			ResourceType: ResourceTypeUserPreference,
			ResourceKey:  SystemResourceKey(ResourceTypeUserPreference),
			Action:       "modify",
			SessionID:    "session-preference-1",
			Title:        "preference suggestion",
			Content:      "补充新的偏好意见",
			Status:       "accepted",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}
	if err := db.Create(&suggestions).Error; err != nil {
		t.Fatalf("create suggestions: %v", err)
	}
	if err := db.Table("memory_review").Create([]map[string]any{
		{
			"id":             "memory-review-result-1",
			"user_id":        "u1",
			"target":         ResourceTypeMemory,
			"session_id":     "",
			"source_content": "",
			"operations":     "[]",
			"content":        "new memory",
			"state":          "success",
			"review_status":  "pending",
			"time":           now,
		},
		{
			"id":             "preference-review-result-1",
			"user_id":        "u1",
			"target":         ResourceTypeUserPreference,
			"session_id":     "",
			"source_content": "",
			"operations":     "[]",
			"content":        "---\nagent_persona: 严谨助手\nuser_address: 老师\nresponse_style: 先结论后论证\n---\n用户偏好简洁回答。",
			"state":          "success",
			"review_status":  "pending",
			"time":           now,
		},
		{
			"id":             "memory-review-result-other-user",
			"user_id":        "u2",
			"target":         ResourceTypeMemory,
			"session_id":     "",
			"source_content": "",
			"operations":     "[]",
			"content":        "other user memory",
			"state":          "success",
			"review_status":  "pending",
			"time":           now,
		},
	}).Error; err != nil {
		t.Fatalf("create review results: %v", err)
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
	if memoryItem.AgentPersona != nil || memoryItem.UserAddress != nil || memoryItem.ResponseStyle != nil {
		t.Fatalf("expected memory metadata fields to be omitted, got %#v", memoryItem)
	}
	if !memoryItem.HasPendingReviewResult {
		t.Fatalf("expected memory item to show pending review result")
	}
	if memoryItem.ReviewStatus != ReviewStatusPending {
		t.Fatalf("expected memory review_status pending, got %q", memoryItem.ReviewStatus)
	}

	preferenceItem := resp.Data.Items[1]
	if preferenceItem.ResourceType != ResourceTypeUserPreference {
		t.Fatalf("expected second item to be preference, got %q", preferenceItem.ResourceType)
	}
	if preferenceItem.Title != ManagedPreferenceTitle {
		t.Fatalf("expected preference title %q, got %q", ManagedPreferenceTitle, preferenceItem.Title)
	}
	if preferenceItem.ResourceID != "preference-1" || preferenceItem.Content != "用户偏好简洁回答。" || preferenceItem.ContentSummary == "" {
		t.Fatalf("unexpected preference item, got %#v", preferenceItem)
	}
	if stringValue(preferenceItem.AgentPersona) != "严谨助手" || stringValue(preferenceItem.UserAddress) != "老师" || stringValue(preferenceItem.ResponseStyle) != "先结论后论证" {
		t.Fatalf("unexpected preference metadata: %#v", preferenceItem)
	}
	if !preferenceItem.HasPendingReviewResult {
		t.Fatalf("expected preference item to show pending review result")
	}
	if preferenceItem.ReviewStatus != ReviewStatusPending {
		t.Fatalf("expected preference review_status pending, got %q", preferenceItem.ReviewStatus)
	}

	var preferenceCount int64
	if err := db.Model(&orm.SystemUserPreference{}).Count(&preferenceCount).Error; err != nil {
		t.Fatalf("count preferences: %v", err)
	}
	if preferenceCount != 1 {
		t.Fatalf("expected list endpoint to preserve existing preference row, got %d", preferenceCount)
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
