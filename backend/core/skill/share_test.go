package skill

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	"lazymind/core/store"
)

func newTCP4HTTPTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listener unavailable in current test environment: %v", err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.Start()
	return server
}

type listSkillShareTargetsAPITestResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		SkillID       string `json:"skill_id"`
		StatusSummary struct {
			PendingAccept int `json:"pending_accept"`
			Completed     int `json:"completed"`
			Rejected      int `json:"rejected"`
			Failed        int `json:"failed"`
		} `json:"status_summary"`
		Items []struct {
			TargetUserID      string `json:"target_user_id"`
			TargetUserName    string `json:"target_user_name"`
			Status            string `json:"status"`
			ShareItemID       string `json:"share_item_id"`
			ShareTaskID       string `json:"share_task_id"`
			Message           string `json:"message"`
			TargetRootSkillID string `json:"target_root_skill_id"`
			ErrorMessage      string `json:"error_message"`
		} `json:"items"`
		Page     int `json:"page"`
		PageSize int `json:"page_size"`
		Total    int `json:"total"`
	} `json:"data"`
}

type createSkillShareAPITestResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		ShareTaskID string `json:"share_task_id"`
		Items       []struct {
			TargetUserID   string `json:"TargetUserID"`
			TargetUserName string `json:"TargetUserName"`
		} `json:"items"`
	} `json:"data"`
}

type listSkillSharesAPITestResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Items []struct {
			ShareItemID     string `json:"share_item_id"`
			TargetUserID    string `json:"target_user_id"`
			TargetUserName  string `json:"target_user_name"`
			SourceUserID    string `json:"source_user_id"`
			SourceUserName  string `json:"source_user_name"`
			SourceSkillID   string `json:"source_skill_id"`
			Message         string `json:"message"`
			TargetRootSkill string `json:"target_root_skill_id"`
		} `json:"items"`
		Total int `json:"total"`
	} `json:"data"`
}

func TestShareResolvesTargetUserNamesWhenCreatingItems(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	skills := []orm.SkillResource{
		newShareTestSkillResource("skill-1", "u1", "User 1", "release-check", now),
		newShareTestSkillResource("skill-u2", "u2", "User Two", "target-one", now),
		newShareTestSkillResource("skill-u3", "u3", "user-three", "target-two", now),
	}
	if err := db.Create(&skills).Error; err != nil {
		t.Fatalf("create seed skills: %v", err)
	}

	req := mux.SetURLVars(
		httptest.NewRequest(
			http.MethodPost,
			"/api/core/skills/skill-1:share",
			strings.NewReader(`{"target_user_ids":["u1","u2","u3"],"message":"please review"}`),
		),
		map[string]string{"skill_id": "skill-1"},
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	Share(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp createSkillShareAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	if len(resp.Data.Items) != 2 {
		t.Fatalf("expected 2 share items after filtering self, got %d", len(resp.Data.Items))
	}
	if resp.Data.Items[0].TargetUserID != "u2" || resp.Data.Items[0].TargetUserName != "User Two" {
		t.Fatalf("expected first target to resolve display name, got %#v", resp.Data.Items[0])
	}
	if resp.Data.Items[1].TargetUserID != "u3" || resp.Data.Items[1].TargetUserName != "user-three" {
		t.Fatalf("expected second target to fall back to username, got %#v", resp.Data.Items[1])
	}

	var items []orm.SkillShareItem
	if err := db.Order("target_user_id ASC").Find(&items).Error; err != nil {
		t.Fatalf("query share items: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 persisted share items, got %d", len(items))
	}
	if items[0].TargetUserID != "u2" || items[0].TargetUserName != "User Two" {
		t.Fatalf("expected persisted u2 user name to be resolved, got %#v", items[0])
	}
	if items[1].TargetUserID != "u3" || items[1].TargetUserName != "user-three" {
		t.Fatalf("expected persisted u3 user name to be resolved, got %#v", items[1])
	}
}

func TestShareExpandsTargetGroupsFromAuthService(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	const internalToken = "test-internal-token"
	authServer := newTCP4HTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/authservice/group/g1/user":
			if got := r.URL.Query().Get("active_only"); got != "true" {
				t.Fatalf("expected group user lookup to request active_only=true, got %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"users": []map[string]string{
					{"user_id": "u1", "username": "User 1"},
					{"user_id": "u2", "username": "User Two"},
					{"user_id": "u3", "username": "User Three"},
				},
			})
		case "/api/authservice/group/g1/user/internal":
			t.Fatalf("expected share to use existing auth-service group users endpoint before internal fallback")
		case "/api/authservice/user/u2":
			_ = json.NewEncoder(w).Encode(map[string]string{"user_id": "u2", "username": "User Two"})
		case "/api/authservice/user/u3":
			_ = json.NewEncoder(w).Encode(map[string]string{"user_id": "u3", "username": "User Three"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer authServer.Close()
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", authServer.URL)
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", internalToken)

	now := time.Now().UTC()
	parent := newShareTestSkillResource("skill-1", "u1", "User 1", "release-check", now)
	if err := db.Create(&parent).Error; err != nil {
		t.Fatalf("create parent skill: %v", err)
	}

	req := mux.SetURLVars(
		httptest.NewRequest(
			http.MethodPost,
			"/api/core/skills/skill-1:share",
			strings.NewReader(`{"target_user_ids":[],"target_group_ids":["g1"],"message":"please review"}`),
		),
		map[string]string{"skill_id": "skill-1"},
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	Share(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp createSkillShareAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	if len(resp.Data.Items) != 2 {
		t.Fatalf("expected 2 share items after filtering self, got %d", len(resp.Data.Items))
	}
	if resp.Data.Items[0].TargetUserID != "u2" || resp.Data.Items[1].TargetUserID != "u3" {
		t.Fatalf("expected auth-service group members u2 and u3, got %#v", resp.Data.Items)
	}
}

func TestShareGroupTargetsExcludeLocallyCachedDisabledUsers(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	authServer := newTCP4HTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/authservice/group/g1/user":
			if got := r.URL.Query().Get("active_only"); got != "true" {
				t.Fatalf("expected group user lookup to request active_only=true, got %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"users": []map[string]string{
					{"user_id": "u2", "username": "User Two"},
				},
			})
		case "/api/authservice/user/u2":
			_ = json.NewEncoder(w).Encode(map[string]string{"user_id": "u2", "username": "User Two"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer authServer.Close()
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", authServer.URL)

	now := time.Now().UTC()
	parent := newShareTestSkillResource("skill-1", "u1", "User 1", "release-check", now)
	if err := db.Create(&parent).Error; err != nil {
		t.Fatalf("create parent skill: %v", err)
	}
	if err := db.Create(&orm.UserGroupModel{
		UserID:  "u2",
		GroupID: "g1",
	}).Error; err != nil {
		t.Fatalf("create active cached group member: %v", err)
	}
	if err := db.Create(&orm.UserGroupModel{
		UserID:  "u-disabled",
		GroupID: "g1",
	}).Error; err != nil {
		t.Fatalf("create disabled cached group member: %v", err)
	}

	req := mux.SetURLVars(
		httptest.NewRequest(
			http.MethodPost,
			"/api/core/skills/skill-1:share",
			strings.NewReader(`{"target_user_ids":[],"target_group_ids":["g1"],"message":"please review"}`),
		),
		map[string]string{"skill_id": "skill-1"},
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	Share(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp createSkillShareAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	if len(resp.Data.Items) != 1 {
		t.Fatalf("expected only active auth-service member to be shared, got %d items: %#v", len(resp.Data.Items), resp.Data.Items)
	}
	if resp.Data.Items[0].TargetUserID != "u2" {
		t.Fatalf("expected only u2 to remain share target, got %#v", resp.Data.Items[0])
	}

	var items []orm.SkillShareItem
	if err := db.Order("target_user_id ASC").Find(&items).Error; err != nil {
		t.Fatalf("query share items: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 persisted share item, got %d", len(items))
	}
	if items[0].TargetUserID != "u2" {
		t.Fatalf("expected persisted share item for u2 only, got %#v", items[0])
	}
}

func TestOutgoingSharesResolvesLegacyTargetUserNames(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	if err := db.Create(&orm.SkillShareTask{
		ID:                    "task-legacy",
		SourceUserID:          "u1",
		SourceUserName:        "User 1",
		SourceSkillID:         "skill-1",
		SourceCategory:        "coding",
		SourceParentSkillName: "release-check",
		SourceRelativeRoot:    "coding/release-check",
		Message:               "legacy share",
		CreatedAt:             now,
		UpdatedAt:             now,
	}).Error; err != nil {
		t.Fatalf("create share task: %v", err)
	}
	if err := db.Create(&orm.SkillShareItem{
		ID:             "item-legacy",
		ShareTaskID:    "task-legacy",
		TargetUserID:   "u2",
		TargetUserName: "u2",
		Status:         shareStatusPendingAccept,
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create legacy share item: %v", err)
	}
	targetUserSkill := newShareTestSkillResource("skill-u2", "u2", "User Two", "target-one", now)
	if err := db.Create(&targetUserSkill).Error; err != nil {
		t.Fatalf("create target user skill: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/skill-shares/outgoing?page=1&page_size=20", nil)
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	OutgoingShares(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp listSkillSharesAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	if resp.Data.Total != 1 || len(resp.Data.Items) != 1 {
		t.Fatalf("expected one outgoing share item, got total=%d items=%d", resp.Data.Total, len(resp.Data.Items))
	}
	if resp.Data.Items[0].TargetUserName != "User Two" {
		t.Fatalf("expected outgoing share list to replace legacy user id with user name, got %#v", resp.Data.Items[0])
	}
}

func TestListSkillShareTargetsAggregatesLatestStatusPerUser(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	seedShareTestData(t, db, now)

	req := mux.SetURLVars(
		httptest.NewRequest(http.MethodGet, "/api/core/skills/skill-1:shares?page=1&page_size=20", nil),
		map[string]string{"skill_id": "skill-1"},
	)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	ListSkillShareTargets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp listSkillShareTargetsAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	if resp.Data.SkillID != "skill-1" {
		t.Fatalf("expected skill_id skill-1, got %q", resp.Data.SkillID)
	}
	if resp.Data.Total != 4 {
		t.Fatalf("expected total 4 unique targets, got %d", resp.Data.Total)
	}
	if resp.Data.StatusSummary.PendingAccept != 1 || resp.Data.StatusSummary.Completed != 1 || resp.Data.StatusSummary.Rejected != 1 || resp.Data.StatusSummary.Failed != 1 {
		t.Fatalf("unexpected status summary: %#v", resp.Data.StatusSummary)
	}

	itemsByUser := make(map[string]struct {
		Status            string
		Message           string
		TargetRootSkillID string
		ErrorMessage      string
	}, len(resp.Data.Items))
	for _, item := range resp.Data.Items {
		itemsByUser[item.TargetUserID] = struct {
			Status            string
			Message           string
			TargetRootSkillID string
			ErrorMessage      string
		}{
			Status:            item.Status,
			Message:           item.Message,
			TargetRootSkillID: item.TargetRootSkillID,
			ErrorMessage:      item.ErrorMessage,
		}
	}

	if len(itemsByUser) != 4 {
		t.Fatalf("expected 4 items in current page, got %d", len(itemsByUser))
	}
	if got := itemsByUser["u2"]; got.Status != shareStatusPendingAccept || got.Message != "resend to user 2" {
		t.Fatalf("expected u2 latest status pending_accept from resend task, got %#v", got)
	}
	if got := itemsByUser["u3"]; got.Status != shareStatusRejected {
		t.Fatalf("expected u3 rejected, got %#v", got)
	}
	if got := itemsByUser["u4"]; got.Status != shareStatusCompleted || got.TargetRootSkillID != "target-skill-u4" {
		t.Fatalf("expected u4 completed with target skill id, got %#v", got)
	}
	if got := itemsByUser["u5"]; got.Status != shareStatusFailed || got.ErrorMessage != "copy failed" {
		t.Fatalf("expected u5 failed with error message, got %#v", got)
	}
	if _, ok := itemsByUser["u9"]; ok {
		t.Fatalf("did not expect share targets from another skill to appear")
	}
}

func TestListSkillShareTargetsResolvesLegacyTargetUserNames(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	skills := []orm.SkillResource{
		newShareTestSkillResource("skill-1", "u1", "User 1", "release-check", now),
		newShareTestSkillResource("skill-u2", "u2", "User Two", "target-one", now),
	}
	if err := db.Create(&skills).Error; err != nil {
		t.Fatalf("create seed skills: %v", err)
	}
	if err := db.Create(&orm.SkillShareTask{
		ID:                    "task-legacy",
		SourceUserID:          "u1",
		SourceUserName:        "User 1",
		SourceSkillID:         "skill-1",
		SourceCategory:        "coding",
		SourceParentSkillName: "release-check",
		SourceRelativeRoot:    "coding/release-check",
		Message:               "legacy share",
		CreatedAt:             now,
		UpdatedAt:             now,
	}).Error; err != nil {
		t.Fatalf("create share task: %v", err)
	}
	if err := db.Create(&orm.SkillShareItem{
		ID:             "item-legacy",
		ShareTaskID:    "task-legacy",
		TargetUserID:   "u2",
		TargetUserName: "u2",
		Status:         shareStatusPendingAccept,
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create legacy share item: %v", err)
	}

	req := mux.SetURLVars(
		httptest.NewRequest(http.MethodGet, "/api/core/skills/skill-1:shares?page=1&page_size=20", nil),
		map[string]string{"skill_id": "skill-1"},
	)
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	ListSkillShareTargets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp listSkillShareTargetsAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	if len(resp.Data.Items) != 1 {
		t.Fatalf("expected one aggregated target, got %d", len(resp.Data.Items))
	}
	if resp.Data.Items[0].TargetUserName != "User Two" {
		t.Fatalf("expected legacy target name to be resolved in share targets list, got %#v", resp.Data.Items[0])
	}
}

func TestListSkillShareTargetsSupportsStatusFilter(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	seedShareTestData(t, db, now)

	req := mux.SetURLVars(
		httptest.NewRequest(http.MethodGet, "/api/core/skills/skill-1:shares?status=completed&page=1&page_size=20", nil),
		map[string]string{"skill_id": "skill-1"},
	)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	ListSkillShareTargets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp listSkillShareTargetsAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	if resp.Data.Total != 1 {
		t.Fatalf("expected filtered total 1, got %d", resp.Data.Total)
	}
	if len(resp.Data.Items) != 1 {
		t.Fatalf("expected 1 filtered item, got %d", len(resp.Data.Items))
	}
	if resp.Data.Items[0].TargetUserID != "u4" || resp.Data.Items[0].Status != shareStatusCompleted {
		t.Fatalf("expected completed item for u4, got %#v", resp.Data.Items[0])
	}
	if resp.Data.StatusSummary.PendingAccept != 1 || resp.Data.StatusSummary.Completed != 1 || resp.Data.StatusSummary.Rejected != 1 || resp.Data.StatusSummary.Failed != 1 {
		t.Fatalf("unexpected status summary under filter: %#v", resp.Data.StatusSummary)
	}
}

func seedShareTestData(t *testing.T, db *orm.DB, now time.Time) {
	t.Helper()

	parentSkill := orm.SkillResource{
		ID:             "skill-1",
		OwnerUserID:    "u1",
		OwnerUserName:  "User 1",
		Category:       "coding",
		SkillName:      "release-check",
		NodeType:       evolution.SkillNodeTypeParent,
		RelativePath:   evolution.ParentSkillRelativePath("coding", "release-check"),
		ContentHash:    "hash-skill-1",
		Version:        1,
		IsEnabled:      true,
		UpdateStatus:   evolution.UpdateStatusUpToDate,
		CreateUserID:   "u1",
		CreateUserName: "User 1",
		CreatedAt:      now.Add(-6 * time.Hour),
		UpdatedAt:      now.Add(-6 * time.Hour),
	}
	otherSkill := orm.SkillResource{
		ID:             "skill-2",
		OwnerUserID:    "u1",
		OwnerUserName:  "User 1",
		Category:       "coding",
		SkillName:      "deploy-check",
		NodeType:       evolution.SkillNodeTypeParent,
		RelativePath:   evolution.ParentSkillRelativePath("coding", "deploy-check"),
		ContentHash:    "hash-skill-2",
		Version:        1,
		IsEnabled:      true,
		UpdateStatus:   evolution.UpdateStatusUpToDate,
		CreateUserID:   "u1",
		CreateUserName: "User 1",
		CreatedAt:      now.Add(-6 * time.Hour),
		UpdatedAt:      now.Add(-6 * time.Hour),
	}
	if err := db.Create([]*orm.SkillResource{&parentSkill, &otherSkill}).Error; err != nil {
		t.Fatalf("create skill resources: %v", err)
	}

	acceptedAt := now.Add(-4 * time.Hour)
	rejectedAt := now.Add(-2 * time.Hour)
	tasks := []orm.SkillShareTask{
		{
			ID:                    "task-old-u2",
			SourceUserID:          "u1",
			SourceUserName:        "User 1",
			SourceSkillID:         "skill-1",
			SourceCategory:        "coding",
			SourceParentSkillName: "release-check",
			SourceRelativeRoot:    "coding/release-check",
			Message:               "first share to user 2",
			CreatedAt:             now.Add(-5 * time.Hour),
			UpdatedAt:             now.Add(-5 * time.Hour),
		},
		{
			ID:                    "task-new-u2",
			SourceUserID:          "u1",
			SourceUserName:        "User 1",
			SourceSkillID:         "skill-1",
			SourceCategory:        "coding",
			SourceParentSkillName: "release-check",
			SourceRelativeRoot:    "coding/release-check",
			Message:               "resend to user 2",
			CreatedAt:             now.Add(-1 * time.Hour),
			UpdatedAt:             now.Add(-1 * time.Hour),
		},
		{
			ID:                    "task-u3",
			SourceUserID:          "u1",
			SourceUserName:        "User 1",
			SourceSkillID:         "skill-1",
			SourceCategory:        "coding",
			SourceParentSkillName: "release-check",
			SourceRelativeRoot:    "coding/release-check",
			Message:               "share to user 3",
			CreatedAt:             now.Add(-3 * time.Hour),
			UpdatedAt:             now.Add(-3 * time.Hour),
		},
		{
			ID:                    "task-u4",
			SourceUserID:          "u1",
			SourceUserName:        "User 1",
			SourceSkillID:         "skill-1",
			SourceCategory:        "coding",
			SourceParentSkillName: "release-check",
			SourceRelativeRoot:    "coding/release-check",
			Message:               "share to user 4",
			CreatedAt:             now.Add(-90 * time.Minute),
			UpdatedAt:             now.Add(-90 * time.Minute),
		},
		{
			ID:                    "task-u5",
			SourceUserID:          "u1",
			SourceUserName:        "User 1",
			SourceSkillID:         "skill-1",
			SourceCategory:        "coding",
			SourceParentSkillName: "release-check",
			SourceRelativeRoot:    "coding/release-check",
			Message:               "share to user 5",
			CreatedAt:             now.Add(-80 * time.Minute),
			UpdatedAt:             now.Add(-80 * time.Minute),
		},
		{
			ID:                    "task-other-skill",
			SourceUserID:          "u1",
			SourceUserName:        "User 1",
			SourceSkillID:         "skill-2",
			SourceCategory:        "coding",
			SourceParentSkillName: "deploy-check",
			SourceRelativeRoot:    "coding/deploy-check",
			Message:               "share another skill",
			CreatedAt:             now.Add(-70 * time.Minute),
			UpdatedAt:             now.Add(-70 * time.Minute),
		},
	}
	if err := db.Create(&tasks).Error; err != nil {
		t.Fatalf("create share tasks: %v", err)
	}

	items := []orm.SkillShareItem{
		{
			ID:                "item-old-u2",
			ShareTaskID:       "task-old-u2",
			TargetUserID:      "u2",
			TargetUserName:    "User 2",
			Status:            shareStatusCompleted,
			AcceptedAt:        &acceptedAt,
			TargetRootSkillID: "target-skill-u2-old",
			CreatedAt:         now.Add(-5 * time.Hour),
			UpdatedAt:         now.Add(-4 * time.Hour),
		},
		{
			ID:             "item-new-u2",
			ShareTaskID:    "task-new-u2",
			TargetUserID:   "u2",
			TargetUserName: "User 2",
			Status:         shareStatusPendingAccept,
			CreatedAt:      now.Add(-1 * time.Hour),
			UpdatedAt:      now.Add(-1 * time.Hour),
		},
		{
			ID:             "item-u3",
			ShareTaskID:    "task-u3",
			TargetUserID:   "u3",
			TargetUserName: "User 3",
			Status:         shareStatusRejected,
			RejectedAt:     &rejectedAt,
			CreatedAt:      now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-2 * time.Hour),
		},
		{
			ID:                "item-u4",
			ShareTaskID:       "task-u4",
			TargetUserID:      "u4",
			TargetUserName:    "User 4",
			Status:            shareStatusCompleted,
			AcceptedAt:        &acceptedAt,
			TargetRootSkillID: "target-skill-u4",
			CreatedAt:         now.Add(-90 * time.Minute),
			UpdatedAt:         now.Add(-85 * time.Minute),
		},
		{
			ID:             "item-u5",
			ShareTaskID:    "task-u5",
			TargetUserID:   "u5",
			TargetUserName: "User 5",
			Status:         shareStatusFailed,
			ErrorMessage:   "copy failed",
			CreatedAt:      now.Add(-80 * time.Minute),
			UpdatedAt:      now.Add(-75 * time.Minute),
		},
		{
			ID:             "item-other-skill",
			ShareTaskID:    "task-other-skill",
			TargetUserID:   "u9",
			TargetUserName: "User 9",
			Status:         shareStatusCompleted,
			CreatedAt:      now.Add(-70 * time.Minute),
			UpdatedAt:      now.Add(-69 * time.Minute),
		},
	}
	if err := db.Create(&items).Error; err != nil {
		t.Fatalf("create share items: %v", err)
	}
}

func newShareTestSkillResource(id, userID, userName, skillName string, now time.Time) orm.SkillResource {
	return orm.SkillResource{
		ID:             id,
		OwnerUserID:    userID,
		OwnerUserName:  userName,
		Category:       "coding",
		SkillName:      skillName,
		NodeType:       evolution.SkillNodeTypeParent,
		RelativePath:   evolution.ParentSkillRelativePath("coding", skillName),
		ContentHash:    "hash-" + id,
		Version:        1,
		IsEnabled:      true,
		UpdateStatus:   evolution.UpdateStatusUpToDate,
		CreateUserID:   userID,
		CreateUserName: userName,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}
