package evolution

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type listSuggestionsAPITestResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Items []struct {
			ID           string `json:"id"`
			UserID       string `json:"user_id"`
			ResourceType string `json:"resource_type"`
			ResourceKey  string `json:"resource_key"`
			Outdated     bool   `json:"outdated"`
		} `json:"items"`
		Page     int   `json:"page"`
		PageSize int   `json:"page_size"`
		Total    int64 `json:"total"`
	} `json:"data"`
}

type suggestionAPITestResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		ID           string  `json:"id"`
		Status       string  `json:"status"`
		ReviewerID   string  `json:"reviewer_id"`
		ReviewerName string  `json:"reviewer_name"`
		Outdated     bool    `json:"outdated"`
		ReviewedAt   *string `json:"reviewed_at"`
	} `json:"data"`
}

type batchSuggestionAPITestResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Items []struct {
			ID           string  `json:"id"`
			Status       string  `json:"status"`
			ReviewerID   string  `json:"reviewer_id"`
			ReviewerName string  `json:"reviewer_name"`
			Outdated     bool    `json:"outdated"`
			ReviewedAt   *string `json:"reviewed_at"`
		} `json:"items"`
	} `json:"data"`
}

func TestListSuggestionsSupportsEvolutionAndResourceFilters(t *testing.T) {
	db := newTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	memory := orm.SystemMemory{
		ID:            "memory-1",
		UserID:        "u1",
		Content:       "",
		ContentHash:   HashContent(""),
		Version:       1,
		UpdatedBy:     "system",
		UpdatedByName: "system",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	preference := orm.SystemUserPreference{
		ID:            "preference-1",
		UserID:        "u2",
		Content:       "",
		ContentHash:   HashContent(""),
		Version:       1,
		UpdatedBy:     "system",
		UpdatedByName: "system",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	skill := orm.SkillResource{
		ID:              "skill-1",
		OwnerUserID:     "u1",
		OwnerUserName:   "User 1",
		Category:        "coding",
		ParentSkillName: "git-workflow",
		SkillName:       "git-workflow",
		NodeType:        SkillNodeTypeParent,
		FileExt:         "md",
		RelativePath:    ParentSkillRelativePath("coding", "git-workflow"),
		ContentHash:     HashContent("skill"),
		Version:         1,
		IsEnabled:       true,
		UpdateStatus:    UpdateStatusUpToDate,
		CreateUserID:    "u1",
		CreateUserName:  "User 1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.Create(&memory).Error; err != nil {
		t.Fatalf("create memory: %v", err)
	}
	if err := db.Create(&preference).Error; err != nil {
		t.Fatalf("create preference: %v", err)
	}
	if err := db.Create(&skill).Error; err != nil {
		t.Fatalf("create skill: %v", err)
	}

	rows := []orm.ResourceSuggestion{
		{
			ID:           "s-skill",
			UserID:       "u1",
			ResourceType: ResourceTypeSkill,
			ResourceKey:  skill.ID,
			Category:     skill.Category,
			SkillName:    skill.SkillName,
			RelativePath: skill.RelativePath,
			Action:       SuggestionActionModify,
			SessionID:    "session-skill",
			SnapshotHash: HashContent("skill-old"),
			Title:        "skill suggestion",
			Content:      "update skill",
			Status:       SuggestionStatusPendingReview,
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:           "s-memory",
			UserID:       "u1",
			ResourceType: ResourceTypeMemory,
			ResourceKey:  SystemResourceKey(ResourceTypeMemory),
			Action:       SuggestionActionModify,
			SessionID:    "session-memory",
			Title:        "memory suggestion",
			Content:      "update memory",
			Status:       SuggestionStatusPendingReview,
			CreatedAt:    now.Add(1 * time.Second),
			UpdatedAt:    now.Add(1 * time.Second),
		},
		{
			ID:           "s-pref",
			UserID:       "u2",
			ResourceType: ResourceTypeUserPreference,
			ResourceKey:  SystemResourceKey(ResourceTypeUserPreference),
			Action:       SuggestionActionModify,
			SessionID:    "session-pref",
			Title:        "preference suggestion",
			Content:      "update preference",
			Status:       SuggestionStatusPendingReview,
			CreatedAt:    now.Add(2 * time.Second),
			UpdatedAt:    now.Add(2 * time.Second),
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create suggestions: %v", err)
	}

	testCases := []struct {
		name        string
		query       string
		wantIDs     []string
		wantTotal   int64
		wantUserIDs []string
		wantStale   []bool
	}{
		{
			name:        "filter by evolution id and resource type for skill",
			query:       "/api/core/evolution/suggestions?resource_type=skill&evolution_id=skill:skill-1",
			wantIDs:     []string{"s-skill"},
			wantTotal:   1,
			wantUserIDs: []string{"u1"},
			wantStale:   []bool{true},
		},
		{
			name:        "filter by evolution id and resource key for memory",
			query:       "/api/core/evolution/suggestions?resource_key=memory&evolution_id=memory:memory-1",
			wantIDs:     []string{"s-memory"},
			wantTotal:   1,
			wantUserIDs: []string{"u1"},
			wantStale:   []bool{false},
		},
		{
			name:        "filter by evolution id for user preference",
			query:       "/api/core/evolution/suggestions?evolution_id=user_preference:preference-1",
			wantIDs:     []string{"s-pref"},
			wantTotal:   1,
			wantUserIDs: []string{"u2"},
			wantStale:   []bool{false},
		},
		{
			name:        "filter by resource type only",
			query:       "/api/core/evolution/suggestions?resource_type=skill",
			wantIDs:     []string{"s-skill"},
			wantTotal:   1,
			wantUserIDs: []string{"u1"},
			wantStale:   []bool{true},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.query, nil)
			rec := httptest.NewRecorder()

			ListSuggestions(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
			}

			var resp listSuggestionsAPITestResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.Code != 0 {
				t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
			}
			if resp.Data.Total != tc.wantTotal {
				t.Fatalf("expected total %d, got %d", tc.wantTotal, resp.Data.Total)
			}
			if len(resp.Data.Items) != len(tc.wantIDs) {
				t.Fatalf("expected %d items, got %d", len(tc.wantIDs), len(resp.Data.Items))
			}
			for idx, item := range resp.Data.Items {
				if item.ID != tc.wantIDs[idx] {
					t.Fatalf("expected item %d id %q, got %q", idx, tc.wantIDs[idx], item.ID)
				}
				if item.UserID != tc.wantUserIDs[idx] {
					t.Fatalf("expected item %d user_id %q, got %q", idx, tc.wantUserIDs[idx], item.UserID)
				}
				if item.Outdated != tc.wantStale[idx] {
					t.Fatalf("expected item %d outdated=%v, got %v", idx, tc.wantStale[idx], item.Outdated)
				}
			}
		})
	}
}

func TestListSuggestionsSupportsEvolutionIDFilters(t *testing.T) {
	db := newTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	memory := orm.SystemMemory{
		ID:            "memory-1",
		UserID:        "u1",
		Content:       "",
		ContentHash:   HashContent(""),
		Version:       1,
		UpdatedBy:     "system",
		UpdatedByName: "system",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	preference := orm.SystemUserPreference{
		ID:            "preference-1",
		UserID:        "u2",
		Content:       "",
		ContentHash:   HashContent(""),
		Version:       1,
		UpdatedBy:     "system",
		UpdatedByName: "system",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	parent := orm.SkillResource{
		ID:              "skill-parent",
		OwnerUserID:     "u1",
		OwnerUserName:   "User 1",
		Category:        "coding",
		ParentSkillName: "git-workflow",
		SkillName:       "git-workflow",
		NodeType:        SkillNodeTypeParent,
		FileExt:         "md",
		RelativePath:    ParentSkillRelativePath("coding", "git-workflow"),
		ContentHash:     HashContent("skill"),
		Version:         1,
		IsEnabled:       true,
		UpdateStatus:    UpdateStatusUpToDate,
		CreateUserID:    "u1",
		CreateUserName:  "User 1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	child := orm.SkillResource{
		ID:              "skill-child",
		OwnerUserID:     "u1",
		OwnerUserName:   "User 1",
		Category:        "coding",
		ParentSkillName: "git-workflow",
		SkillName:       "rules",
		NodeType:        SkillNodeTypeChild,
		FileExt:         "md",
		RelativePath:    "coding/git-workflow/rules.md",
		ContentHash:     HashContent("child"),
		Version:         1,
		IsEnabled:       true,
		UpdateStatus:    UpdateStatusUpToDate,
		CreateUserID:    "u1",
		CreateUserName:  "User 1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	for _, row := range []any{&memory, &preference, &parent, &child} {
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("create fixture: %v", err)
		}
	}

	rows := []orm.ResourceSuggestion{
		{
			ID:              "s-skill-legacy",
			UserID:          "u1",
			ResourceType:    ResourceTypeSkill,
			ResourceKey:     parent.ID,
			Category:        parent.Category,
			ParentSkillName: "git-workflow",
			SkillName:       "git-workflow",
			Action:          SuggestionActionModify,
			SessionID:       "session-skill",
			Title:           "skill suggestion",
			Content:         "update skill",
			Status:          SuggestionStatusPendingReview,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			ID:           "s-memory-legacy",
			UserID:       "u1",
			ResourceType: ResourceTypeMemory,
			ResourceKey:  "",
			Action:       SuggestionActionModify,
			SessionID:    "session-memory",
			Title:        "memory suggestion",
			Content:      "update memory",
			Status:       SuggestionStatusPendingReview,
			CreatedAt:    now.Add(1 * time.Second),
			UpdatedAt:    now.Add(1 * time.Second),
		},
		{
			ID:           "s-pref-legacy",
			UserID:       "u2",
			ResourceType: ResourceTypeUserPreference,
			ResourceKey:  "",
			Action:       SuggestionActionModify,
			SessionID:    "session-pref",
			Title:        "preference suggestion",
			Content:      "update preference",
			Status:       SuggestionStatusPendingReview,
			CreatedAt:    now.Add(2 * time.Second),
			UpdatedAt:    now.Add(2 * time.Second),
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create suggestions: %v", err)
	}

	testCases := []struct {
		name      string
		query     string
		wantIDs   []string
		wantTotal int64
	}{
		{
			name:      "filter by typed evolution id for parent skill",
			query:     "/api/core/evolution/suggestions?evolution_id=skill:skill-parent",
			wantIDs:   []string{"s-skill-legacy"},
			wantTotal: 1,
		},
		{
			name:      "filter by typed evolution id for child skill does not include parent suggestion",
			query:     "/api/core/evolution/suggestions?evolution_id=skill:skill-child",
			wantIDs:   nil,
			wantTotal: 0,
		},
		{
			name:      "filter by typed evolution id for memory",
			query:     "/api/core/evolution/suggestions?evolution_id=memory:memory-1",
			wantIDs:   []string{"s-memory-legacy"},
			wantTotal: 1,
		},
		{
			name:      "filter by typed evolution id for user preference",
			query:     "/api/core/evolution/suggestions?evolution_id=user_preference:preference-1",
			wantIDs:   []string{"s-pref-legacy"},
			wantTotal: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.query, nil)
			rec := httptest.NewRecorder()

			ListSuggestions(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
			}

			var resp listSuggestionsAPITestResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.Code != 0 {
				t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
			}
			if resp.Data.Total != tc.wantTotal {
				t.Fatalf("expected total %d, got %d", tc.wantTotal, resp.Data.Total)
			}
			if len(resp.Data.Items) != len(tc.wantIDs) {
				t.Fatalf("expected %d items, got %d", len(tc.wantIDs), len(resp.Data.Items))
			}
			for idx, item := range resp.Data.Items {
				if item.ID != tc.wantIDs[idx] {
					t.Fatalf("expected item %d id %q, got %q", idx, tc.wantIDs[idx], item.ID)
				}
			}
		})
	}
}

func TestListSuggestionsRejectsInvalidEvolutionIDFilter(t *testing.T) {
	db := newTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	req := httptest.NewRequest(http.MethodGet, "/api/core/evolution/suggestions?evolution_id=memory", nil)
	rec := httptest.NewRecorder()

	ListSuggestions(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid suggestion filter") || !strings.Contains(rec.Body.String(), "evolution_id must use") {
		t.Fatalf("expected invalid evolution_id message, got body=%s", rec.Body.String())
	}
}

func TestBatchReviewSuggestionsAndRejectedItemsAreHiddenFromDefaultList(t *testing.T) {
	db := newTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	rows := []orm.ResourceSuggestion{
		{
			ID:           "s-approve-1",
			UserID:       "u1",
			ResourceType: ResourceTypeMemory,
			ResourceKey:  SystemResourceKey(ResourceTypeMemory),
			Action:       SuggestionActionModify,
			SessionID:    "session-approve-1",
			Title:        "approve 1",
			Content:      "memory change 1",
			Status:       SuggestionStatusPendingReview,
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:           "s-approve-2",
			UserID:       "u1",
			ResourceType: ResourceTypeMemory,
			ResourceKey:  SystemResourceKey(ResourceTypeMemory),
			Action:       SuggestionActionModify,
			SessionID:    "session-approve-2",
			Title:        "approve 2",
			Content:      "memory change 2",
			Status:       SuggestionStatusPendingReview,
			CreatedAt:    now.Add(1 * time.Second),
			UpdatedAt:    now.Add(1 * time.Second),
		},
		{
			ID:           "s-reject-1",
			UserID:       "u1",
			ResourceType: ResourceTypeMemory,
			ResourceKey:  SystemResourceKey(ResourceTypeMemory),
			Action:       SuggestionActionModify,
			SessionID:    "session-reject-1",
			Title:        "reject 1",
			Content:      "memory change 3",
			Status:       SuggestionStatusPendingReview,
			CreatedAt:    now.Add(2 * time.Second),
			UpdatedAt:    now.Add(2 * time.Second),
		},
		{
			ID:           "s-reject-2",
			UserID:       "u1",
			ResourceType: ResourceTypeMemory,
			ResourceKey:  SystemResourceKey(ResourceTypeMemory),
			Action:       SuggestionActionModify,
			SessionID:    "session-reject-2",
			Title:        "reject 2",
			Content:      "memory change 4",
			Status:       SuggestionStatusPendingReview,
			CreatedAt:    now.Add(3 * time.Second),
			UpdatedAt:    now.Add(3 * time.Second),
		},
		{
			ID:           "s-applied-1",
			UserID:       "u1",
			ResourceType: ResourceTypeMemory,
			ResourceKey:  SystemResourceKey(ResourceTypeMemory),
			Action:       SuggestionActionModify,
			SessionID:    "session-applied-1",
			Title:        "applied 1",
			Content:      "memory change 5",
			Status:       SuggestionStatusApplied,
			CreatedAt:    now.Add(4 * time.Second),
			UpdatedAt:    now.Add(4 * time.Second),
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create suggestions: %v", err)
	}

	approveReq := httptest.NewRequest(http.MethodPost, "/api/core/evolution/suggestions:batchApprove", strings.NewReader(`{"ids":["s-approve-1","s-approve-2"]}`))
	approveReq.Header.Set("Content-Type", "application/json")
	approveReq.Header.Set("X-User-Id", "reviewer-1")
	approveReq.Header.Set("X-User-Name", "Reviewer One")
	approveRec := httptest.NewRecorder()

	BatchApproveSuggestions(approveRec, approveReq)

	if approveRec.Code != http.StatusOK {
		t.Fatalf("expected approve status 200, got %d body=%s", approveRec.Code, approveRec.Body.String())
	}
	var approveResp batchSuggestionAPITestResponse
	if err := json.Unmarshal(approveRec.Body.Bytes(), &approveResp); err != nil {
		t.Fatalf("decode approve response: %v", err)
	}
	if approveResp.Code != 0 {
		t.Fatalf("expected approve code 0, got %d", approveResp.Code)
	}
	if len(approveResp.Data.Items) != 2 {
		t.Fatalf("expected 2 accepted items, got %d", len(approveResp.Data.Items))
	}
	for _, item := range approveResp.Data.Items {
		if item.Status != SuggestionStatusAccepted {
			t.Fatalf("expected accepted status, got %q", item.Status)
		}
		if item.ReviewerID != "reviewer-1" || item.ReviewerName != "Reviewer One" {
			t.Fatalf("unexpected approve reviewer: %#v", item)
		}
		if item.ReviewedAt == nil || *item.ReviewedAt == "" {
			t.Fatalf("expected approve reviewed_at to be populated")
		}
	}

	rejectReq := httptest.NewRequest(http.MethodPost, "/api/core/evolution/suggestions:batchReject", strings.NewReader(`{"ids":["s-reject-1","s-reject-2"]}`))
	rejectReq.Header.Set("Content-Type", "application/json")
	rejectReq.Header.Set("X-User-Id", "reviewer-2")
	rejectReq.Header.Set("X-User-Name", "Reviewer Two")
	rejectRec := httptest.NewRecorder()

	BatchRejectSuggestions(rejectRec, rejectReq)

	if rejectRec.Code != http.StatusOK {
		t.Fatalf("expected reject status 200, got %d body=%s", rejectRec.Code, rejectRec.Body.String())
	}
	var rejectResp batchSuggestionAPITestResponse
	if err := json.Unmarshal(rejectRec.Body.Bytes(), &rejectResp); err != nil {
		t.Fatalf("decode reject response: %v", err)
	}
	if rejectResp.Code != 0 {
		t.Fatalf("expected reject code 0, got %d", rejectResp.Code)
	}
	if len(rejectResp.Data.Items) != 2 {
		t.Fatalf("expected 2 rejected items, got %d", len(rejectResp.Data.Items))
	}
	for _, item := range rejectResp.Data.Items {
		if item.Status != SuggestionStatusRejected {
			t.Fatalf("expected rejected status, got %q", item.Status)
		}
		if item.ReviewerID != "reviewer-2" || item.ReviewerName != "Reviewer Two" {
			t.Fatalf("unexpected reject reviewer: %#v", item)
		}
		if item.ReviewedAt == nil || *item.ReviewedAt == "" {
			t.Fatalf("expected reject reviewed_at to be populated")
		}
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/core/evolution/suggestions?resource_type=memory&page=1&page_size=20", nil)
	listRec := httptest.NewRecorder()

	ListSuggestions(listRec, listReq)

	if listRec.Code != http.StatusOK {
		t.Fatalf("expected list status 200, got %d body=%s", listRec.Code, listRec.Body.String())
	}
	var listResp listSuggestionsAPITestResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if listResp.Code != 0 {
		t.Fatalf("expected list code 0, got %d", listResp.Code)
	}
	if listResp.Data.Total != 2 {
		t.Fatalf("expected total 2 after rejected/applied items hidden, got %d", listResp.Data.Total)
	}
	if len(listResp.Data.Items) != 2 {
		t.Fatalf("expected 2 listed items after rejected/applied items hidden, got %d", len(listResp.Data.Items))
	}
	for _, item := range listResp.Data.Items {
		if item.ID == "s-reject-1" || item.ID == "s-reject-2" || item.ID == "s-applied-1" {
			t.Fatalf("hidden suggestion %q should not appear in default list", item.ID)
		}
		if item.ID != "s-approve-2" && item.ID != "s-approve-1" {
			t.Fatalf("unexpected listed suggestion id %q", item.ID)
		}
	}
}

func TestBatchReviewSuggestionsIgnoresOriginalStatus(t *testing.T) {
	db := newTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	rows := []orm.ResourceSuggestion{
		{
			ID:           "s-accepted",
			UserID:       "u1",
			ResourceType: ResourceTypeMemory,
			ResourceKey:  SystemResourceKey(ResourceTypeMemory),
			Action:       SuggestionActionModify,
			SessionID:    "session-accepted",
			Title:        "accepted",
			Content:      "memory change 1",
			Status:       SuggestionStatusAccepted,
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:           "s-rejected",
			UserID:       "u1",
			ResourceType: ResourceTypeMemory,
			ResourceKey:  SystemResourceKey(ResourceTypeMemory),
			Action:       SuggestionActionModify,
			SessionID:    "session-rejected",
			Title:        "rejected",
			Content:      "memory change 2",
			Status:       SuggestionStatusRejected,
			CreatedAt:    now.Add(1 * time.Second),
			UpdatedAt:    now.Add(1 * time.Second),
		},
		{
			ID:           "s-applied",
			UserID:       "u1",
			ResourceType: ResourceTypeMemory,
			ResourceKey:  SystemResourceKey(ResourceTypeMemory),
			Action:       SuggestionActionModify,
			SessionID:    "session-applied",
			Title:        "applied",
			Content:      "memory change 3",
			Status:       SuggestionStatusApplied,
			CreatedAt:    now.Add(2 * time.Second),
			UpdatedAt:    now.Add(2 * time.Second),
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create suggestions: %v", err)
	}

	approveReq := httptest.NewRequest(http.MethodPost, "/api/core/evolution/suggestions:batchApprove", strings.NewReader(`{"ids":["s-accepted","s-rejected","s-applied"]}`))
	approveReq.Header.Set("Content-Type", "application/json")
	approveReq.Header.Set("X-User-Id", "reviewer-approve")
	approveReq.Header.Set("X-User-Name", "Reviewer Approve")
	approveRec := httptest.NewRecorder()

	BatchApproveSuggestions(approveRec, approveReq)

	if approveRec.Code != http.StatusOK {
		t.Fatalf("expected approve status 200, got %d body=%s", approveRec.Code, approveRec.Body.String())
	}
	var approveResp batchSuggestionAPITestResponse
	if err := json.Unmarshal(approveRec.Body.Bytes(), &approveResp); err != nil {
		t.Fatalf("decode approve response: %v", err)
	}
	if approveResp.Code != 0 {
		t.Fatalf("expected approve code 0, got %d", approveResp.Code)
	}
	if len(approveResp.Data.Items) != 3 {
		t.Fatalf("expected 3 approved items, got %d", len(approveResp.Data.Items))
	}
	for _, item := range approveResp.Data.Items {
		if item.Status != SuggestionStatusAccepted {
			t.Fatalf("expected accepted status, got %q", item.Status)
		}
		if item.ReviewerID != "reviewer-approve" || item.ReviewerName != "Reviewer Approve" {
			t.Fatalf("unexpected approve reviewer: %#v", item)
		}
		if item.ReviewedAt == nil || *item.ReviewedAt == "" {
			t.Fatalf("expected approve reviewed_at to be populated")
		}
	}

	rejectReq := httptest.NewRequest(http.MethodPost, "/api/core/evolution/suggestions:batchReject", strings.NewReader(`{"ids":["s-accepted","s-rejected","s-applied"]}`))
	rejectReq.Header.Set("Content-Type", "application/json")
	rejectReq.Header.Set("X-User-Id", "reviewer-reject")
	rejectReq.Header.Set("X-User-Name", "Reviewer Reject")
	rejectRec := httptest.NewRecorder()

	BatchRejectSuggestions(rejectRec, rejectReq)

	if rejectRec.Code != http.StatusOK {
		t.Fatalf("expected reject status 200, got %d body=%s", rejectRec.Code, rejectRec.Body.String())
	}
	var rejectResp batchSuggestionAPITestResponse
	if err := json.Unmarshal(rejectRec.Body.Bytes(), &rejectResp); err != nil {
		t.Fatalf("decode reject response: %v", err)
	}
	if rejectResp.Code != 0 {
		t.Fatalf("expected reject code 0, got %d", rejectResp.Code)
	}
	if len(rejectResp.Data.Items) != 3 {
		t.Fatalf("expected 3 rejected items, got %d", len(rejectResp.Data.Items))
	}
	for _, item := range rejectResp.Data.Items {
		if item.Status != SuggestionStatusRejected {
			t.Fatalf("expected rejected status, got %q", item.Status)
		}
		if item.ReviewerID != "reviewer-reject" || item.ReviewerName != "Reviewer Reject" {
			t.Fatalf("unexpected reject reviewer: %#v", item)
		}
		if item.ReviewedAt == nil || *item.ReviewedAt == "" {
			t.Fatalf("expected reject reviewed_at to be populated")
		}
	}
}

func TestReviewSuggestionsIgnoreOriginalStatusForAllResourceTypes(t *testing.T) {
	db := newTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	resourceKey := func(resourceType string) string {
		switch resourceType {
		case ResourceTypeSkill:
			return ParentSkillRelativePath("coding", "git-workflow")
		case ResourceTypeMemory:
			return SystemResourceKey(ResourceTypeMemory)
		case ResourceTypeUserPreference:
			return SystemResourceKey(ResourceTypeUserPreference)
		default:
			return resourceType
		}
	}
	suffix := func(resourceType string) string {
		return strings.ReplaceAll(resourceType, "_", "-")
	}

	now := time.Now()
	resourceTypes := []string{ResourceTypeSkill, ResourceTypeMemory, ResourceTypeUserPreference}
	rows := make([]orm.ResourceSuggestion, 0, len(resourceTypes)*2)
	batchIDs := make([]string, 0, len(resourceTypes))
	for idx, resourceType := range resourceTypes {
		singleID := "single-" + suffix(resourceType)
		batchID := "batch-" + suffix(resourceType)
		rows = append(rows,
			orm.ResourceSuggestion{
				ID:           singleID,
				UserID:       "u1",
				ResourceType: resourceType,
				ResourceKey:  resourceKey(resourceType),
				Action:       SuggestionActionModify,
				SessionID:    "session-" + singleID,
				Title:        "single " + resourceType,
				Content:      "single change",
				Status:       SuggestionStatusRejected,
				CreatedAt:    now.Add(time.Duration(idx) * time.Second),
				UpdatedAt:    now.Add(time.Duration(idx) * time.Second),
			},
			orm.ResourceSuggestion{
				ID:           batchID,
				UserID:       "u1",
				ResourceType: resourceType,
				ResourceKey:  resourceKey(resourceType),
				Action:       SuggestionActionModify,
				SessionID:    "session-" + batchID,
				Title:        "batch " + resourceType,
				Content:      "batch change",
				Status:       SuggestionStatusApplied,
				CreatedAt:    now.Add(time.Duration(idx+10) * time.Second),
				UpdatedAt:    now.Add(time.Duration(idx+10) * time.Second),
			},
		)
		batchIDs = append(batchIDs, batchID)
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create suggestions: %v", err)
	}

	for _, resourceType := range resourceTypes {
		id := "single-" + suffix(resourceType)

		approveReq := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/api/core/evolution/suggestions/"+id+":approve", nil), map[string]string{"id": id})
		approveReq.Header.Set("X-User-Id", "reviewer-approve")
		approveReq.Header.Set("X-User-Name", "Reviewer Approve")
		approveRec := httptest.NewRecorder()

		ApproveSuggestion(approveRec, approveReq)

		if approveRec.Code != http.StatusOK {
			t.Fatalf("expected approve status 200 for %s, got %d body=%s", resourceType, approveRec.Code, approveRec.Body.String())
		}
		var approveResp suggestionAPITestResponse
		if err := json.Unmarshal(approveRec.Body.Bytes(), &approveResp); err != nil {
			t.Fatalf("decode approve response: %v", err)
		}
		if approveResp.Data.Status != SuggestionStatusAccepted {
			t.Fatalf("expected %s approve to overwrite status to accepted, got %q", resourceType, approveResp.Data.Status)
		}
		if approveResp.Data.ReviewerID != "reviewer-approve" || approveResp.Data.ReviewerName != "Reviewer Approve" {
			t.Fatalf("unexpected approve reviewer for %s: %#v", resourceType, approveResp.Data)
		}

		rejectReq := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/api/core/evolution/suggestions/"+id+":reject", nil), map[string]string{"id": id})
		rejectReq.Header.Set("X-User-Id", "reviewer-reject")
		rejectReq.Header.Set("X-User-Name", "Reviewer Reject")
		rejectRec := httptest.NewRecorder()

		RejectSuggestion(rejectRec, rejectReq)

		if rejectRec.Code != http.StatusOK {
			t.Fatalf("expected reject status 200 for %s, got %d body=%s", resourceType, rejectRec.Code, rejectRec.Body.String())
		}
		var rejectResp suggestionAPITestResponse
		if err := json.Unmarshal(rejectRec.Body.Bytes(), &rejectResp); err != nil {
			t.Fatalf("decode reject response: %v", err)
		}
		if rejectResp.Data.Status != SuggestionStatusRejected {
			t.Fatalf("expected %s reject to overwrite status to rejected, got %q", resourceType, rejectResp.Data.Status)
		}
		if rejectResp.Data.ReviewerID != "reviewer-reject" || rejectResp.Data.ReviewerName != "Reviewer Reject" {
			t.Fatalf("unexpected reject reviewer for %s: %#v", resourceType, rejectResp.Data)
		}
	}

	batchBody, err := json.Marshal(map[string]any{"ids": batchIDs})
	if err != nil {
		t.Fatalf("marshal batch body: %v", err)
	}
	batchApproveReq := httptest.NewRequest(http.MethodPost, "/api/core/evolution/suggestions:batchApprove", strings.NewReader(string(batchBody)))
	batchApproveReq.Header.Set("Content-Type", "application/json")
	batchApproveReq.Header.Set("X-User-Id", "batch-approver")
	batchApproveReq.Header.Set("X-User-Name", "Batch Approver")
	batchApproveRec := httptest.NewRecorder()

	BatchApproveSuggestions(batchApproveRec, batchApproveReq)

	if batchApproveRec.Code != http.StatusOK {
		t.Fatalf("expected batch approve status 200, got %d body=%s", batchApproveRec.Code, batchApproveRec.Body.String())
	}
	var batchApproveResp batchSuggestionAPITestResponse
	if err := json.Unmarshal(batchApproveRec.Body.Bytes(), &batchApproveResp); err != nil {
		t.Fatalf("decode batch approve response: %v", err)
	}
	if len(batchApproveResp.Data.Items) != len(resourceTypes) {
		t.Fatalf("expected %d batch approve items, got %d", len(resourceTypes), len(batchApproveResp.Data.Items))
	}
	for _, item := range batchApproveResp.Data.Items {
		if item.Status != SuggestionStatusAccepted {
			t.Fatalf("expected batch approve to overwrite status to accepted, got %q", item.Status)
		}
		if item.ReviewerID != "batch-approver" || item.ReviewerName != "Batch Approver" {
			t.Fatalf("unexpected batch approve reviewer: %#v", item)
		}
	}

	batchRejectReq := httptest.NewRequest(http.MethodPost, "/api/core/evolution/suggestions:batchReject", strings.NewReader(string(batchBody)))
	batchRejectReq.Header.Set("Content-Type", "application/json")
	batchRejectReq.Header.Set("X-User-Id", "batch-rejecter")
	batchRejectReq.Header.Set("X-User-Name", "Batch Rejecter")
	batchRejectRec := httptest.NewRecorder()

	BatchRejectSuggestions(batchRejectRec, batchRejectReq)

	if batchRejectRec.Code != http.StatusOK {
		t.Fatalf("expected batch reject status 200, got %d body=%s", batchRejectRec.Code, batchRejectRec.Body.String())
	}
	var batchRejectResp batchSuggestionAPITestResponse
	if err := json.Unmarshal(batchRejectRec.Body.Bytes(), &batchRejectResp); err != nil {
		t.Fatalf("decode batch reject response: %v", err)
	}
	if len(batchRejectResp.Data.Items) != len(resourceTypes) {
		t.Fatalf("expected %d batch reject items, got %d", len(resourceTypes), len(batchRejectResp.Data.Items))
	}
	for _, item := range batchRejectResp.Data.Items {
		if item.Status != SuggestionStatusRejected {
			t.Fatalf("expected batch reject to overwrite status to rejected, got %q", item.Status)
		}
		if item.ReviewerID != "batch-rejecter" || item.ReviewerName != "Batch Rejecter" {
			t.Fatalf("unexpected batch reject reviewer: %#v", item)
		}
	}
}

func TestListSuggestionsStatusQueryFiltering(t *testing.T) {
	db := newTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	rows := []orm.ResourceSuggestion{
		{
			ID:           "s-pending",
			UserID:       "u1",
			ResourceType: ResourceTypeMemory,
			ResourceKey:  SystemResourceKey(ResourceTypeMemory),
			Action:       SuggestionActionModify,
			SessionID:    "session-pending",
			Title:        "pending",
			Content:      "memory change 1",
			Status:       SuggestionStatusPendingReview,
			CreatedAt:    now.Add(1 * time.Second),
			UpdatedAt:    now.Add(1 * time.Second),
		},
		{
			ID:           "s-accepted",
			UserID:       "u1",
			ResourceType: ResourceTypeMemory,
			ResourceKey:  SystemResourceKey(ResourceTypeMemory),
			Action:       SuggestionActionModify,
			SessionID:    "session-accepted",
			Title:        "accepted",
			Content:      "memory change 2",
			Status:       SuggestionStatusAccepted,
			CreatedAt:    now.Add(2 * time.Second),
			UpdatedAt:    now.Add(2 * time.Second),
		},
		{
			ID:           "s-rejected",
			UserID:       "u1",
			ResourceType: ResourceTypeMemory,
			ResourceKey:  SystemResourceKey(ResourceTypeMemory),
			Action:       SuggestionActionModify,
			SessionID:    "session-rejected",
			Title:        "rejected",
			Content:      "memory change 3",
			Status:       SuggestionStatusRejected,
			CreatedAt:    now.Add(3 * time.Second),
			UpdatedAt:    now.Add(3 * time.Second),
		},
		{
			ID:           "s-applied",
			UserID:       "u1",
			ResourceType: ResourceTypeMemory,
			ResourceKey:  SystemResourceKey(ResourceTypeMemory),
			Action:       SuggestionActionModify,
			SessionID:    "session-applied",
			Title:        "applied",
			Content:      "memory change 4",
			Status:       SuggestionStatusApplied,
			CreatedAt:    now.Add(4 * time.Second),
			UpdatedAt:    now.Add(4 * time.Second),
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create suggestions: %v", err)
	}

	// Unsupported statuses are ignored and still return visible statuses.
	ignoredReq := httptest.NewRequest(http.MethodGet, "/api/core/evolution/suggestions?resource_type=memory&status=applied&status=rejected&page=1&page_size=20", nil)
	ignoredRec := httptest.NewRecorder()

	ListSuggestions(ignoredRec, ignoredReq)

	if ignoredRec.Code != http.StatusOK {
		t.Fatalf("expected list status 200, got %d body=%s", ignoredRec.Code, ignoredRec.Body.String())
	}
	var ignoredResp listSuggestionsAPITestResponse
	if err := json.Unmarshal(ignoredRec.Body.Bytes(), &ignoredResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if ignoredResp.Code != 0 {
		t.Fatalf("expected list code 0, got %d", ignoredResp.Code)
	}
	if ignoredResp.Data.Total != 2 {
		t.Fatalf("expected total 2 after ignoring unsupported status filter, got %d", ignoredResp.Data.Total)
	}
	if len(ignoredResp.Data.Items) != 2 {
		t.Fatalf("expected 2 visible items after ignoring unsupported status filter, got %d", len(ignoredResp.Data.Items))
	}
	if ignoredResp.Data.Items[0].ID != "s-accepted" || ignoredResp.Data.Items[1].ID != "s-pending" {
		t.Fatalf("expected visible suggestions in created_at desc order, got ids %q and %q", ignoredResp.Data.Items[0].ID, ignoredResp.Data.Items[1].ID)
	}

	// Supported status filter should narrow down to that status only.
	pendingReq := httptest.NewRequest(http.MethodGet, "/api/core/evolution/suggestions?resource_type=memory&status=pending_review&page=1&page_size=20", nil)
	pendingRec := httptest.NewRecorder()

	ListSuggestions(pendingRec, pendingReq)

	if pendingRec.Code != http.StatusOK {
		t.Fatalf("expected pending list status 200, got %d body=%s", pendingRec.Code, pendingRec.Body.String())
	}
	var pendingResp listSuggestionsAPITestResponse
	if err := json.Unmarshal(pendingRec.Body.Bytes(), &pendingResp); err != nil {
		t.Fatalf("decode pending list response: %v", err)
	}
	if pendingResp.Code != 0 {
		t.Fatalf("expected pending list code 0, got %d", pendingResp.Code)
	}
	if pendingResp.Data.Total != 1 {
		t.Fatalf("expected total 1 for pending_review filter, got %d", pendingResp.Data.Total)
	}
	if len(pendingResp.Data.Items) != 1 {
		t.Fatalf("expected 1 visible item for pending_review filter, got %d", len(pendingResp.Data.Items))
	}
	if pendingResp.Data.Items[0].ID != "s-pending" {
		t.Fatalf("expected pending suggestion only, got id %q", pendingResp.Data.Items[0].ID)
	}
}

func TestListSuggestionsIncludesAcceptedAfterApproveForEvolutionID(t *testing.T) {
	db := newTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	preference := orm.SystemUserPreference{
		ID:            "preference-1",
		UserID:        "u1",
		Content:       "",
		ContentHash:   HashContent(""),
		Version:       1,
		UpdatedBy:     "system",
		UpdatedByName: "system",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := db.Create(&preference).Error; err != nil {
		t.Fatalf("create preference: %v", err)
	}

	suggestion := orm.ResourceSuggestion{
		ID:           "suggestion-1",
		UserID:       "u1",
		ResourceType: ResourceTypeUserPreference,
		ResourceKey:  SystemResourceKey(ResourceTypeUserPreference),
		Action:       SuggestionActionModify,
		SessionID:    "session-1",
		Title:        "preference suggestion",
		Content:      "update preference",
		Status:       SuggestionStatusPendingReview,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := db.Create(&suggestion).Error; err != nil {
		t.Fatalf("create suggestion: %v", err)
	}

	approveReq := mux.SetURLVars(
		httptest.NewRequest(http.MethodPost, "/api/core/evolution/suggestions/suggestion-1:approve", nil),
		map[string]string{"id": "suggestion-1"},
	)
	approveReq.Header.Set("X-User-Id", "reviewer-1")
	approveReq.Header.Set("X-User-Name", "Reviewer One")
	approveRec := httptest.NewRecorder()

	ApproveSuggestion(approveRec, approveReq)

	if approveRec.Code != http.StatusOK {
		t.Fatalf("expected approve status 200, got %d body=%s", approveRec.Code, approveRec.Body.String())
	}

	listReq := httptest.NewRequest(
		http.MethodGet,
		"/api/core/evolution/suggestions?page=1&page_size=20&evolution_id=user_preference:preference-1",
		nil,
	)
	listRec := httptest.NewRecorder()

	ListSuggestions(listRec, listReq)

	if listRec.Code != http.StatusOK {
		t.Fatalf("expected list status 200, got %d body=%s", listRec.Code, listRec.Body.String())
	}
	var listResp struct {
		Code int `json:"code"`
		Data struct {
			Items []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"items"`
			Total int64 `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if listResp.Code != 0 {
		t.Fatalf("expected list code 0, got %d", listResp.Code)
	}
	if listResp.Data.Total != 1 || len(listResp.Data.Items) != 1 {
		t.Fatalf("expected one accepted suggestion to remain visible, total=%d items=%d", listResp.Data.Total, len(listResp.Data.Items))
	}
	if listResp.Data.Items[0].ID != "suggestion-1" || listResp.Data.Items[0].Status != SuggestionStatusAccepted {
		t.Fatalf("expected accepted suggestion-1, got %#v", listResp.Data.Items[0])
	}
}

func TestSuggestionDetailAndReviewIncludeOutdated(t *testing.T) {
	db := newTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	skill := orm.SkillResource{
		ID:              "skill-1",
		OwnerUserID:     "u1",
		OwnerUserName:   "User 1",
		Category:        "coding",
		ParentSkillName: "git-workflow",
		SkillName:       "git-workflow",
		NodeType:        SkillNodeTypeParent,
		FileExt:         "md",
		RelativePath:    ParentSkillRelativePath("coding", "git-workflow"),
		ContentHash:     HashContent("skill-current"),
		Version:         1,
		IsEnabled:       true,
		UpdateStatus:    UpdateStatusUpToDate,
		CreateUserID:    "u1",
		CreateUserName:  "User 1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.Create(&skill).Error; err != nil {
		t.Fatalf("create skill: %v", err)
	}

	rows := []orm.ResourceSuggestion{
		{
			ID:              "s-get",
			UserID:          "u1",
			ResourceType:    ResourceTypeSkill,
			ResourceKey:     skill.ID,
			Category:        skill.Category,
			ParentSkillName: skill.ParentSkillName,
			SkillName:       skill.SkillName,
			RelativePath:    skill.RelativePath,
			FileExt:         "md",
			Action:          SuggestionActionModify,
			SessionID:       "session-get",
			SnapshotHash:    HashContent("skill-old"),
			Title:           "get suggestion",
			Content:         "update skill",
			Status:          SuggestionStatusPendingReview,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			ID:              "s-approve",
			UserID:          "u1",
			ResourceType:    ResourceTypeSkill,
			ResourceKey:     skill.ID,
			Category:        skill.Category,
			ParentSkillName: skill.ParentSkillName,
			SkillName:       skill.SkillName,
			RelativePath:    skill.RelativePath,
			FileExt:         "md",
			Action:          SuggestionActionModify,
			SessionID:       "session-approve",
			SnapshotHash:    HashContent("skill-old"),
			Title:           "approve suggestion",
			Content:         "update skill",
			Status:          SuggestionStatusPendingReview,
			CreatedAt:       now.Add(1 * time.Second),
			UpdatedAt:       now.Add(1 * time.Second),
		},
		{
			ID:              "s-reject",
			UserID:          "u1",
			ResourceType:    ResourceTypeSkill,
			ResourceKey:     skill.ID,
			Category:        skill.Category,
			ParentSkillName: skill.ParentSkillName,
			SkillName:       skill.SkillName,
			RelativePath:    skill.RelativePath,
			FileExt:         "md",
			Action:          SuggestionActionModify,
			SessionID:       "session-reject",
			SnapshotHash:    HashContent("skill-old"),
			Title:           "reject suggestion",
			Content:         "update skill",
			Status:          SuggestionStatusPendingReview,
			CreatedAt:       now.Add(2 * time.Second),
			UpdatedAt:       now.Add(2 * time.Second),
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create suggestions: %v", err)
	}

	t.Run("get", func(t *testing.T) {
		req := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/core/evolution/suggestions/s-get", nil), map[string]string{"id": "s-get"})
		rec := httptest.NewRecorder()

		GetSuggestion(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
		}

		var resp suggestionAPITestResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Code != 0 {
			t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
		}
		if !resp.Data.Outdated {
			t.Fatalf("expected get response to be outdated")
		}
	})

	t.Run("approve", func(t *testing.T) {
		req := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/api/core/evolution/suggestions/s-approve:approve", nil), map[string]string{"id": "s-approve"})
		req.Header.Set("X-User-Id", "reviewer-1")
		req.Header.Set("X-User-Name", "Reviewer One")
		rec := httptest.NewRecorder()

		ApproveSuggestion(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
		}

		var resp suggestionAPITestResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Code != 0 {
			t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
		}
		if resp.Data.Status != SuggestionStatusAccepted {
			t.Fatalf("expected accepted status, got %q", resp.Data.Status)
		}
		if resp.Data.ReviewerID != "reviewer-1" || resp.Data.ReviewerName != "Reviewer One" {
			t.Fatalf("unexpected reviewer: %#v", resp.Data)
		}
		if !resp.Data.Outdated {
			t.Fatalf("expected approve response to be outdated")
		}
		if resp.Data.ReviewedAt == nil || *resp.Data.ReviewedAt == "" {
			t.Fatalf("expected reviewed_at to be populated")
		}
	})

	t.Run("reject", func(t *testing.T) {
		req := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/api/core/evolution/suggestions/s-reject:reject", nil), map[string]string{"id": "s-reject"})
		req.Header.Set("X-User-Id", "reviewer-2")
		req.Header.Set("X-User-Name", "Reviewer Two")
		rec := httptest.NewRecorder()

		RejectSuggestion(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
		}

		var resp suggestionAPITestResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Code != 0 {
			t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
		}
		if resp.Data.Status != SuggestionStatusRejected {
			t.Fatalf("expected rejected status, got %q", resp.Data.Status)
		}
		if resp.Data.ReviewerID != "reviewer-2" || resp.Data.ReviewerName != "Reviewer Two" {
			t.Fatalf("unexpected reviewer: %#v", resp.Data)
		}
		if !resp.Data.Outdated {
			t.Fatalf("expected reject response to be outdated")
		}
		if resp.Data.ReviewedAt == nil || *resp.Data.ReviewedAt == "" {
			t.Fatalf("expected reviewed_at to be populated")
		}
	})
}
