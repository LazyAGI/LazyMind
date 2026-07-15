package evolution

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/resourcefs"
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
			PreferredName          *string `json:"preferred_name"`
			ResponseStyle          *string `json:"response_style"`
			ContentSummary         string  `json:"content_summary"`
			HasPendingReviewResult bool    `json:"has_pending_review_result"`
			ReviewStatus           string  `json:"review_status"`
			AutoEvo                bool    `json:"auto_evo"`
			AutoEvoApplyStatus     string  `json:"auto_evo_apply_status"`
			AutoEvoStartedAt       *string `json:"auto_evo_started_at"`
			AutoEvoFinishedAt      *string `json:"auto_evo_finished_at"`
			AutoEvoError           string  `json:"auto_evo_error"`
		} `json:"items"`
	} `json:"data"`
}

func TestListManagedStatesReturnsDefaultsAndUserScopedRows(t *testing.T) {
	db := newTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	preference := orm.SystemUserPreference{
		Content:       "用户偏好简洁回答。",
		AgentPersona:  "严谨助手",
		PreferredName: "老师",
		ResponseStyle: "先结论后论证",
	}
	commitTestPersonalResource(t, db, "u1", resourcefs.ResourceTypeMemory, "倾向于先结论后论证，遇到风险点时优先列出明确建议。")
	commitTestPersonalResource(t, db, "u1", resourcefs.ResourceTypeUserPreference, FormatSystemUserPreferenceForChat(preference))

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
	if memoryItem.AgentPersona != nil || memoryItem.PreferredName != nil || memoryItem.ResponseStyle != nil {
		t.Fatalf("expected memory metadata fields to be omitted, got %#v", memoryItem)
	}
	if memoryItem.HasPendingReviewResult {
		t.Fatalf("expected memory item to have no pending review result")
	}
	if memoryItem.ReviewStatus != ReviewStatusNone {
		t.Fatalf("expected memory review_status none, got %q", memoryItem.ReviewStatus)
	}
	if !memoryItem.AutoEvo || memoryItem.AutoEvoApplyStatus != AutoEvoApplyStatusIdle || memoryItem.AutoEvoStartedAt != nil || memoryItem.AutoEvoFinishedAt != nil || memoryItem.AutoEvoError != "" {
		t.Fatalf("expected enabled resource to expose waiting lifecycle inputs, got %#v", memoryItem)
	}

	preferenceItem := resp.Data.Items[1]
	if preferenceItem.ResourceType != ResourceTypeUserPreference {
		t.Fatalf("expected second item to be preference, got %q", preferenceItem.ResourceType)
	}
	if preferenceItem.Title != ManagedPreferenceTitle {
		t.Fatalf("expected preference title %q, got %q", ManagedPreferenceTitle, preferenceItem.Title)
	}
	if preferenceItem.ResourceID == "" || preferenceItem.Content != "用户偏好简洁回答。" || preferenceItem.ContentSummary == "" {
		t.Fatalf("unexpected preference item, got %#v", preferenceItem)
	}
	if stringValue(preferenceItem.AgentPersona) != "严谨助手" || stringValue(preferenceItem.PreferredName) != "老师" || stringValue(preferenceItem.ResponseStyle) != "先结论后论证" {
		t.Fatalf("unexpected preference metadata: %#v", preferenceItem)
	}
	if preferenceItem.HasPendingReviewResult {
		t.Fatalf("expected preference item to have no pending review result")
	}
	if preferenceItem.ReviewStatus != ReviewStatusNone {
		t.Fatalf("expected preference review_status none, got %q", preferenceItem.ReviewStatus)
	}

	var resourceCount int64
	if err := db.Model(&orm.PersonalResource{}).Count(&resourceCount).Error; err != nil {
		t.Fatalf("count personal resources: %v", err)
	}
	if resourceCount != 2 {
		t.Fatalf("expected list endpoint to preserve personal resource rows, got %d", resourceCount)
	}
}

func TestNewManagedStateItemExposesAutoEvoLifecycle(t *testing.T) {
	startedAt := time.Date(2026, 7, 15, 8, 30, 0, 123, time.FixedZone("CST", 8*60*60))
	finishedAt := startedAt.Add(2 * time.Minute)
	item := NewManagedStateItem(ResourceTypeMemory, &PersonalResourceContent{
		ResourceID:         "memory-1",
		AutoEvo:            true,
		AutoEvoApplyStatus: AutoEvoApplyStatusFailed,
		AutoEvoGeneration:  7,
		AutoEvoStartedAt:   &startedAt,
		AutoEvoFinishedAt:  &finishedAt,
		AutoEvoError:       "apply conflict",
	}, nil)
	if item.AutoEvoStartedAt == nil || *item.AutoEvoStartedAt != startedAt.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("unexpected started_at: %#v", item.AutoEvoStartedAt)
	}
	if item.AutoEvoFinishedAt == nil || *item.AutoEvoFinishedAt != finishedAt.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("unexpected finished_at: %#v", item.AutoEvoFinishedAt)
	}
	if item.AutoEvoApplyStatus != AutoEvoApplyStatusFailed || item.AutoEvoError != "apply conflict" || item.AutoEvoGeneration != 7 {
		t.Fatalf("unexpected lifecycle item: %#v", item)
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
