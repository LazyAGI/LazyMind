package preference

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	"lazymind/core/resourcechange"
	"lazymind/core/store"
)

type upsertPreferenceAPITestResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		ResourceID     string  `json:"resource_id"`
		ResourceType   string  `json:"resource_type"`
		Title          string  `json:"title"`
		Content        string  `json:"content"`
		AgentPersona   *string `json:"agent_persona"`
		PreferredName  *string `json:"preferred_name"`
		ResponseStyle  *string `json:"response_style"`
		ContentSummary string  `json:"content_summary"`
	} `json:"data"`
}

type draftPreviewPreferenceAPITestResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		ReviewResultID     string `json:"review_result_id"`
		ReviewStatus       string `json:"review_status"`
		DraftStatus        string `json:"draft_status"`
		DraftSourceVersion int64  `json:"draft_source_version"`
		CurrentContent     string `json:"current_content"`
		DraftContent       string `json:"draft_content"`
		Diff               string `json:"diff"`
	} `json:"data"`
}

type generatePreferenceAPITestResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		DraftStatus        string   `json:"draft_status"`
		DraftSourceVersion int64    `json:"draft_source_version"`
		DraftContent       string   `json:"draft_content"`
		SuggestionIDs      []string `json:"suggestion_ids"`
	} `json:"data"`
}

func newPreferenceTestDB(t *testing.T) *orm.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := orm.Connect(orm.DriverSQLite, dbPath)
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(orm.AllModelsForDDL()...); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func createPreferenceReviewResult(t *testing.T, db *orm.DB, id, userID, target, content string, at time.Time) {
	t.Helper()
	if err := db.Create(&orm.MemoryReviewResult{
		ID:           id,
		UserID:       userID,
		Target:       target,
		SessionID:    "session-" + id,
		Content:      content,
		Operations:   json.RawMessage(`[]`),
		State:        "success",
		ReviewStatus: "pending",
		Time:         at,
	}).Error; err != nil {
		t.Fatalf("create preference review result: %v", err)
	}
}

func preferenceReviewResultStatus(t *testing.T, db *orm.DB, id string) string {
	t.Helper()
	var row orm.MemoryReviewResult
	if err := db.Select("review_status").Where("id = ?", id).Take(&row).Error; err != nil {
		t.Fatalf("query preference review result %s: %v", id, err)
	}
	return row.ReviewStatus
}

func TestUpsertCreatesThenUpdatesPreference(t *testing.T) {
	db := newPreferenceTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	firstReq := httptest.NewRequest(http.MethodPut, "/api/core/user-preference", strings.NewReader(`{"content":"第一版偏好内容"}`))
	firstReq.Header.Set("Content-Type", "application/json")
	firstReq.Header.Set("X-User-Id", "u1")
	firstReq.Header.Set("X-User-Name", "User 1")
	firstRec := httptest.NewRecorder()

	Upsert(firstRec, firstReq)

	if firstRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", firstRec.Code, firstRec.Body.String())
	}

	var firstResp upsertPreferenceAPITestResponse
	if err := json.Unmarshal(firstRec.Body.Bytes(), &firstResp); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if firstResp.Data.ResourceType != "user_preference" {
		t.Fatalf("expected user_preference resource type, got %q", firstResp.Data.ResourceType)
	}
	if firstResp.Data.Content != "第一版偏好内容" {
		t.Fatalf("unexpected first content: %q", firstResp.Data.Content)
	}

	var created orm.SystemUserPreference
	if err := db.Where("user_id = ?", "u1").Take(&created).Error; err != nil {
		t.Fatalf("query created preference: %v", err)
	}
	if created.Version != 1 {
		t.Fatalf("expected created version 1, got %d", created.Version)
	}
	if !created.AutoEvo {
		t.Fatalf("expected created auto_evo to default true")
	}

	secondReq := httptest.NewRequest(http.MethodPut, "/api/core/user-preference", strings.NewReader(`{"content":"第二版偏好内容"}`))
	secondReq.Header.Set("Content-Type", "application/json")
	secondReq.Header.Set("X-User-Id", "u1")
	secondReq.Header.Set("X-User-Name", "User 1")
	secondRec := httptest.NewRecorder()

	Upsert(secondRec, secondReq)

	if secondRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", secondRec.Code, secondRec.Body.String())
	}

	var updated orm.SystemUserPreference
	if err := db.Where("user_id = ?", "u1").Take(&updated).Error; err != nil {
		t.Fatalf("query updated preference: %v", err)
	}
	if updated.ID != created.ID {
		t.Fatalf("expected update in place, got new id %q from old %q", updated.ID, created.ID)
	}
	if updated.Content != "第二版偏好内容" {
		t.Fatalf("unexpected updated content: %q", updated.Content)
	}
	if updated.Version != 2 {
		t.Fatalf("expected updated version 2, got %d", updated.Version)
	}
}

func TestUpsertPreservesPreferenceAutoEvoWhenOmitted(t *testing.T) {
	db := newPreferenceTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	createReq := httptest.NewRequest(http.MethodPut, "/api/core/user-preference", strings.NewReader(`{"content":"第一版偏好内容","auto_evo":false}`))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("X-User-Id", "u1")
	createReq.Header.Set("X-User-Name", "User 1")
	createRec := httptest.NewRecorder()

	Upsert(createRec, createReq)

	if createRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", createRec.Code, createRec.Body.String())
	}
	var created orm.SystemUserPreference
	if err := db.Where("user_id = ?", "u1").Take(&created).Error; err != nil {
		t.Fatalf("query created preference: %v", err)
	}
	if created.AutoEvo {
		t.Fatalf("expected explicit auto_evo=false to be persisted on create")
	}
	if created.AutoEvoGeneration != 0 {
		t.Fatalf("expected create to keep auto_evo_generation 0, got %d", created.AutoEvoGeneration)
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/core/user-preference", strings.NewReader(`{"content":"第二版偏好内容"}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateReq.Header.Set("X-User-Id", "u1")
	updateReq.Header.Set("X-User-Name", "User 1")
	updateRec := httptest.NewRecorder()

	Upsert(updateRec, updateReq)

	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", updateRec.Code, updateRec.Body.String())
	}
	var updated orm.SystemUserPreference
	if err := db.Where("user_id = ?", "u1").Take(&updated).Error; err != nil {
		t.Fatalf("query updated preference: %v", err)
	}
	if updated.AutoEvo {
		t.Fatalf("expected omitted auto_evo to preserve false")
	}
	if updated.AutoEvoGeneration != created.AutoEvoGeneration {
		t.Fatalf("expected omitted auto_evo to preserve generation %d, got %d", created.AutoEvoGeneration, updated.AutoEvoGeneration)
	}
	if updated.Content != "第二版偏好内容" {
		t.Fatalf("unexpected updated content: %q", updated.Content)
	}
}

func TestUpsertPartiallyUpdatesPreferenceMetadata(t *testing.T) {
	db := newPreferenceTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	createReq := httptest.NewRequest(http.MethodPut, "/api/core/user-preference", strings.NewReader(`{"content":"用户偏好","agent_persona":"严谨助手","preferred_name":"老师","response_style":"先结论后解释"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("X-User-Id", "u1")
	createReq.Header.Set("X-User-Name", "User 1")
	createRec := httptest.NewRecorder()

	Upsert(createRec, createReq)

	if createRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", createRec.Code, createRec.Body.String())
	}
	var createResp upsertPreferenceAPITestResponse
	if err := json.Unmarshal(createRec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if stringValue(createResp.Data.AgentPersona) != "严谨助手" || stringValue(createResp.Data.PreferredName) != "老师" || stringValue(createResp.Data.ResponseStyle) != "先结论后解释" {
		t.Fatalf("unexpected metadata in create response: %#v", createResp.Data)
	}

	var created orm.SystemUserPreference
	if err := db.Where("user_id = ?", "u1").Take(&created).Error; err != nil {
		t.Fatalf("query created preference: %v", err)
	}
	if created.ContentHash != evolution.HashSystemUserPreference(created) {
		t.Fatalf("expected user_preference content hash, got %q", created.ContentHash)
	}
	if got := countPreferenceResourceVersions(t, db, created.ID); got != 1 {
		t.Fatalf("expected create to write 1 resource version, got %d", got)
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/core/user-preference", strings.NewReader(`{"preferred_name":"同学"}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateReq.Header.Set("X-User-Id", "u1")
	updateReq.Header.Set("X-User-Name", "User 1")
	updateRec := httptest.NewRecorder()

	Upsert(updateRec, updateReq)

	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", updateRec.Code, updateRec.Body.String())
	}
	var updateResp upsertPreferenceAPITestResponse
	if err := json.Unmarshal(updateRec.Body.Bytes(), &updateResp); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if updateResp.Data.Content != "用户偏好" || stringValue(updateResp.Data.AgentPersona) != "严谨助手" || stringValue(updateResp.Data.PreferredName) != "同学" || stringValue(updateResp.Data.ResponseStyle) != "先结论后解释" {
		t.Fatalf("unexpected metadata in update response: %#v", updateResp.Data)
	}

	var updated orm.SystemUserPreference
	if err := db.Where("user_id = ?", "u1").Take(&updated).Error; err != nil {
		t.Fatalf("query updated preference: %v", err)
	}
	if updated.Content != created.Content || updated.AgentPersona != created.AgentPersona || updated.ResponseStyle != created.ResponseStyle {
		t.Fatalf("expected omitted fields preserved, got %#v", updated)
	}
	if updated.PreferredName != "同学" {
		t.Fatalf("expected preferred_name update, got %q", updated.PreferredName)
	}
	if updated.Version != created.Version+1 {
		t.Fatalf("expected metadata update to bump version, got %d from %d", updated.Version, created.Version)
	}
	if updated.ContentHash != evolution.HashSystemUserPreference(updated) || updated.ContentHash == created.ContentHash {
		t.Fatalf("expected user_preference content hash to change, created=%q updated=%q", created.ContentHash, updated.ContentHash)
	}
	if got := countPreferenceResourceVersions(t, db, created.ID); got != 1 {
		t.Fatalf("expected metadata-only update to keep 1 resource version, got %d", got)
	}
	var version orm.ResourceVersion
	if err := db.Where("resource_id = ?", created.ID).Take(&version).Error; err != nil {
		t.Fatalf("query resource version: %v", err)
	}
	if version.ChangeSource != resourcechange.ChangeSourceDirectSave {
		t.Fatalf("expected direct_save version source, got %q", version.ChangeSource)
	}
}

func TestUpsertAutoEvoDiscardsPendingDraftWithoutOverwritingPreferenceContent(t *testing.T) {
	db := newPreferenceTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	row := orm.SystemUserPreference{
		ID:                 "preference-1",
		UserID:             "u1",
		Content:            "current preference",
		ContentHash:        evolution.HashContent("current preference"),
		Version:            7,
		DraftContent:       "draft preference",
		DraftSourceVersion: 7,
		DraftStatus:        "pending_confirm",
		AutoEvo:            false,
		Ext:                evolution.WithDraftSuggestionIDs(nil, []string{"suggestion-1"}),
		UpdatedBy:          "u1",
		UpdatedByName:      "User 1",
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create preference: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/core/user-preference", strings.NewReader(`{"content":"request body should not win","auto_evo":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	Upsert(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var updated orm.SystemUserPreference
	if err := db.Where("id = ?", row.ID).Take(&updated).Error; err != nil {
		t.Fatalf("query updated preference: %v", err)
	}
	if updated.Content != row.Content {
		t.Fatalf("expected content to remain %q, got %q", row.Content, updated.Content)
	}
	if updated.Version != row.Version {
		t.Fatalf("expected version to remain %d, got %d", row.Version, updated.Version)
	}
	if strings.TrimSpace(updated.DraftStatus) != "" || updated.DraftContent != "" || updated.DraftSourceVersion != 0 || updated.DraftUpdatedAt != nil {
		t.Fatalf("expected draft to be discarded, got status=%q content=%q source=%d updated_at=%v", updated.DraftStatus, updated.DraftContent, updated.DraftSourceVersion, updated.DraftUpdatedAt)
	}
	if gotIDs := evolution.DraftSuggestionIDs(updated.Ext); len(gotIDs) != 0 {
		t.Fatalf("expected draft suggestion ids to be cleared, got %#v", gotIDs)
	}
	if !updated.AutoEvo {
		t.Fatalf("expected auto_evo to be enabled")
	}
	if updated.AutoEvoGeneration != row.AutoEvoGeneration+1 {
		t.Fatalf("expected auto_evo_generation to increment, got %d", updated.AutoEvoGeneration)
	}
}

func TestDraftPreviewReturnsCurrentDraftAndDiff(t *testing.T) {
	db := newPreferenceTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	row := orm.SystemUserPreference{
		ID:                 "preference-1",
		UserID:             "u1",
		Content:            "current preference",
		AgentPersona:       "current persona",
		PreferredName:      "current address",
		ResponseStyle:      "current style",
		ContentHash:        "hash-current",
		Version:            3,
		DraftContent:       "---\nagent_persona: legacy persona\npreferred_name: legacy address\nresponse_style: legacy style\n---\n\nlegacy preference",
		DraftSourceVersion: 3,
		DraftStatus:        "pending_confirm",
		UpdatedBy:          "u1",
		UpdatedByName:      "User 1",
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create preference: %v", err)
	}
	row.ContentHash = evolution.HashSystemUserPreference(row)
	draftContent := "---\nagent_persona: updated persona\npreferred_name: updated address\nresponse_style: updated style\n---\n\nupdated preference"
	createPreferenceReviewResult(t, db, "preference-preview", "u1", orm.ResourceUpdateResourceTypeUserPreference, draftContent, now)

	req := httptest.NewRequest(http.MethodGet, "/api/core/user-preference:draft-preview", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	DraftPreview(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp draftPreviewPreferenceAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	if resp.Data.ReviewResultID != "preference-preview" {
		t.Fatalf("expected review_result_id preference-preview, got %q", resp.Data.ReviewResultID)
	}
	if resp.Data.ReviewStatus != "pending" || resp.Data.DraftStatus != "pending" {
		t.Fatalf("expected pending review status, got review_status=%q draft_status=%q", resp.Data.ReviewStatus, resp.Data.DraftStatus)
	}
	if resp.Data.CurrentContent != evolution.FormatSystemUserPreferenceForChat(row) {
		t.Fatalf("unexpected current content: %q", resp.Data.CurrentContent)
	}
	if resp.Data.DraftContent != draftContent {
		t.Fatalf("unexpected draft content: %q", resp.Data.DraftContent)
	}
	if !strings.Contains(resp.Data.Diff, "-current preference") {
		t.Fatalf("expected diff to contain removed current content, got %q", resp.Data.Diff)
	}
	if !strings.Contains(resp.Data.Diff, "+updated preference") {
		t.Fatalf("expected diff to contain added draft content, got %q", resp.Data.Diff)
	}
}

func TestDraftPreviewIgnoresLegacyPreferenceResourceDraft(t *testing.T) {
	db := newPreferenceTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	row := orm.SystemUserPreference{
		ID:                 "preference-1",
		UserID:             "u1",
		Content:            "current preference",
		AgentPersona:       "current persona",
		PreferredName:      "current address",
		ResponseStyle:      "current style",
		Version:            3,
		DraftContent:       "---\nagent_persona: legacy\npreferred_name: legacy\nresponse_style: legacy\n---\n\nlegacy preference",
		DraftSourceVersion: 3,
		DraftStatus:        "pending_confirm",
		UpdatedBy:          "u1",
		UpdatedByName:      "User 1",
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	row.ContentHash = evolution.HashSystemUserPreference(row)
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create preference: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/user-preference:draft-preview", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	DraftPreview(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected legacy resource draft to be ignored as 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGenerateOverwritesExistingPendingDraft(t *testing.T) {
	db := newPreferenceTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat/rewrite" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"content": "new preference draft"},
		})
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listener unavailable in current test environment: %v", err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Shutdown(context.Background()) }()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", fmt.Sprintf("http://%s", listener.Addr().String()))

	now := time.Now()
	row := orm.SystemUserPreference{
		ID:                 "preference-1",
		UserID:             "u1",
		Content:            "current preference",
		ContentHash:        evolution.HashContent("current preference"),
		Version:            4,
		DraftContent:       "old preference draft",
		DraftSourceVersion: 3,
		DraftStatus:        "pending_confirm",
		Ext:                evolution.WithDraftSuggestionIDs(nil, []string{"old-suggestion"}),
		UpdatedBy:          "u1",
		UpdatedByName:      "User 1",
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create preference: %v", err)
	}
	suggestion := orm.ResourceSuggestion{
		ID:           "suggestion-1",
		UserID:       "u1",
		ResourceType: evolution.ResourceTypeUserPreference,
		ResourceKey:  evolution.SystemResourceKey(evolution.ResourceTypeUserPreference),
		Action:       "modify",
		SessionID:    "session-1",
		Title:        "preference suggestion",
		Content:      "update preference",
		Status:       "accepted",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := db.Create(&suggestion).Error; err != nil {
		t.Fatalf("create suggestion: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/user-preference:generate", strings.NewReader(`{"user_instruct":"生成新版"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	Generate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp generatePreferenceAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	if resp.Data.DraftStatus != "pending_confirm" {
		t.Fatalf("expected pending_confirm, got %q", resp.Data.DraftStatus)
	}
	if resp.Data.DraftContent != "new preference draft" {
		t.Fatalf("unexpected draft content: %q", resp.Data.DraftContent)
	}

	var updated orm.SystemUserPreference
	if err := db.Where("id = ?", row.ID).Take(&updated).Error; err != nil {
		t.Fatalf("query updated preference: %v", err)
	}
	if updated.DraftContent != "new preference draft" {
		t.Fatalf("expected draft to be overwritten, got %q", updated.DraftContent)
	}
	if updated.DraftSourceVersion != row.Version {
		t.Fatalf("expected draft source version %d, got %d", row.Version, updated.DraftSourceVersion)
	}
	gotIDs := evolution.DraftSuggestionIDs(updated.Ext)
	if len(gotIDs) != 0 {
		t.Fatalf("expected draft suggestion ids to be cleared, got %#v", gotIDs)
	}
}

func TestGenerateRejectsMissingUserInstruct(t *testing.T) {
	db := newPreferenceTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	var algoBody map[string]any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat/rewrite" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&algoBody); err != nil {
			t.Fatalf("decode algorithm request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"content": "draft from suggestion"},
		})
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listener unavailable in current test environment: %v", err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Shutdown(context.Background()) }()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", fmt.Sprintf("http://%s", listener.Addr().String()))

	now := time.Now()
	row := orm.SystemUserPreference{
		ID:            "preference-1",
		UserID:        "u1",
		Content:       "current preference",
		ContentHash:   evolution.HashContent("current preference"),
		Version:       4,
		UpdatedBy:     "u1",
		UpdatedByName: "User 1",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create preference: %v", err)
	}
	suggestion := orm.ResourceSuggestion{
		ID:           "suggestion-1",
		UserID:       "u1",
		ResourceType: evolution.ResourceTypeUserPreference,
		ResourceKey:  evolution.SystemResourceKey(evolution.ResourceTypeUserPreference),
		Action:       "modify",
		SessionID:    "session-1",
		Title:        "preference suggestion",
		Content:      "update preference",
		Status:       "accepted",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := db.Create(&suggestion).Error; err != nil {
		t.Fatalf("create suggestion: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/user-preference:generate", strings.NewReader(`{"user_instruct":""}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	Generate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(algoBody) != 0 {
		t.Fatalf("algorithm should not be called for suggestion-only generate, got %#v", algoBody)
	}
}

func TestGenerateUserInstructOnlyUsesDraftContent(t *testing.T) {
	db := newPreferenceTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	var algoBody map[string]any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat/rewrite" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&algoBody); err != nil {
			t.Fatalf("decode algorithm request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"content": "---\nagent_persona: 新角色\npreferred_name: 新称谓\nresponse_style: 新风格\n---\n\ndraft from user instruction"},
		})
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listener unavailable in current test environment: %v", err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Shutdown(context.Background()) }()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", fmt.Sprintf("http://%s", listener.Addr().String()))

	now := time.Now()
	row := orm.SystemUserPreference{
		ID:                 "preference-1",
		UserID:             "u1",
		Content:            "current preference",
		AgentPersona:       "当前角色",
		PreferredName:      "当前称谓",
		ResponseStyle:      "当前风格",
		Version:            4,
		DraftContent:       "---\nagent_persona: 草稿角色\npreferred_name: 草稿称谓\nresponse_style: 草稿风格\n---\n\ndraft preference",
		DraftSourceVersion: 4,
		DraftStatus:        "pending_confirm",
		Ext:                evolution.WithDraftSuggestionIDs(nil, []string{"suggestion-1"}),
		UpdatedBy:          "u1",
		UpdatedByName:      "User 1",
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	row.ContentHash = evolution.HashSystemUserPreference(row)
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create preference: %v", err)
	}
	suggestion := orm.ResourceSuggestion{
		ID:           "suggestion-1",
		UserID:       "u1",
		ResourceType: evolution.ResourceTypeUserPreference,
		ResourceKey:  evolution.SystemResourceKey(evolution.ResourceTypeUserPreference),
		Action:       "modify",
		SessionID:    "session-1",
		Title:        "preference suggestion",
		Content:      "update preference",
		Status:       "accepted",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := db.Create(&suggestion).Error; err != nil {
		t.Fatalf("create suggestion: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/user-preference:generate", strings.NewReader(`{"user_instruct":"只按用户意见生成"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	Generate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if algoBody["user_instruct"] != "只按用户意见生成" {
		t.Fatalf("unexpected user_instruct sent to algorithm: %#v", algoBody["user_instruct"])
	}
	if algoBody["content"] != row.DraftContent {
		t.Fatalf("expected draft content sent to algorithm, got %#v", algoBody["content"])
	}
	if algoBody["task_type"] != "user_preference" {
		t.Fatalf("expected user_preference task_type, got %#v", algoBody["task_type"])
	}
	if _, ok := algoBody["suggestions"]; ok {
		t.Fatalf("suggestions should not be sent to algorithm: %#v", algoBody["suggestions"])
	}
	var updated orm.SystemUserPreference
	if err := db.Where("id = ?", row.ID).Take(&updated).Error; err != nil {
		t.Fatalf("query updated preference: %v", err)
	}
	gotIDs := evolution.DraftSuggestionIDs(updated.Ext)
	if len(gotIDs) != 0 {
		t.Fatalf("expected draft suggestion ids to be cleared, got %#v", gotIDs)
	}
	reviewContent := "---\nagent_persona: 新角色\npreferred_name: 新称谓\nresponse_style: 新风格\n---\n\ndraft from user instruction"
	createPreferenceReviewResult(t, db, "preference-confirm", "u1", orm.ResourceUpdateResourceTypeUserPreference, reviewContent, now.Add(time.Second))

	confirmReq := httptest.NewRequest(http.MethodPost, "/api/core/user-preference:confirm", nil)
	confirmReq.Header.Set("X-User-Id", "u1")
	confirmReq.Header.Set("X-User-Name", "User 1")
	confirmRec := httptest.NewRecorder()

	Confirm(confirmRec, confirmReq)

	if confirmRec.Code != http.StatusOK {
		t.Fatalf("expected confirm status 200, got %d body=%s", confirmRec.Code, confirmRec.Body.String())
	}
	if status := preferenceReviewResultStatus(t, db, "preference-confirm"); status != "accepted" {
		t.Fatalf("expected review result accepted, got %q", status)
	}
	var confirmed orm.SystemUserPreference
	if err := db.Where("id = ?", row.ID).Take(&confirmed).Error; err != nil {
		t.Fatalf("query confirmed preference: %v", err)
	}
	if confirmed.Content != "draft from user instruction" || confirmed.AgentPersona != "新角色" || confirmed.PreferredName != "新称谓" || confirmed.ResponseStyle != "新风格" {
		t.Fatalf("expected generated frontmatter to be split after confirm, got %#v", confirmed)
	}
}

func TestConfirmParsesUserPreferenceFrontmatter(t *testing.T) {
	db := newPreferenceTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	row := orm.SystemUserPreference{
		ID:                 "preference-1",
		UserID:             "u1",
		Content:            "旧正文",
		AgentPersona:       "旧角色",
		PreferredName:      "旧称谓",
		ResponseStyle:      "旧风格",
		ContentHash:        evolution.HashContent("旧正文"),
		Version:            4,
		DraftContent:       "---\nagent_persona: legacy\npreferred_name: legacy\nresponse_style: legacy\n---\n\nlegacy",
		DraftSourceVersion: 4,
		DraftStatus:        "pending_confirm",
		UpdatedBy:          "u1",
		UpdatedByName:      "User 1",
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	row.ContentHash = evolution.HashSystemUserPreference(row)
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create preference: %v", err)
	}
	reviewContent := "---\nagent_persona: 新角色\npreferred_name: 用户称谓\nresponse_style: 回复风格\n---\n\n新正文"
	createPreferenceReviewResult(t, db, "preference-frontmatter", "u1", orm.ResourceUpdateResourceTypeUserPreference, reviewContent, now)

	req := httptest.NewRequest(http.MethodPost, "/api/core/user-preference:confirm", nil)
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	Confirm(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected confirm status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var updated orm.SystemUserPreference
	if err := db.Where("id = ?", row.ID).Take(&updated).Error; err != nil {
		t.Fatalf("query updated preference: %v", err)
	}
	if updated.Content != "新正文" || updated.AgentPersona != "新角色" || updated.PreferredName != "用户称谓" || updated.ResponseStyle != "回复风格" {
		t.Fatalf("expected frontmatter to be split into preference columns, got %#v", updated)
	}
	if strings.Contains(updated.Content, "agent_persona") || strings.Contains(updated.Content, "---") {
		t.Fatalf("content should not keep raw frontmatter, got %q", updated.Content)
	}
	if updated.ContentHash != evolution.HashSystemUserPreference(updated) {
		t.Fatalf("expected hash over split preference, got %q", updated.ContentHash)
	}
	if status := preferenceReviewResultStatus(t, db, "preference-frontmatter"); status != "accepted" {
		t.Fatalf("expected review result accepted, got %q", status)
	}
}

func TestDiscardRejectsPendingPreferenceReviewResult(t *testing.T) {
	db := newPreferenceTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	row := orm.SystemUserPreference{
		ID:                 "preference-1",
		UserID:             "u1",
		Content:            "current preference",
		ContentHash:        evolution.HashContent("current preference"),
		Version:            4,
		DraftContent:       "---\nagent_persona: legacy\npreferred_name: legacy\nresponse_style: legacy\n---\n\nlegacy preference",
		DraftSourceVersion: 4,
		DraftStatus:        "pending_confirm",
		Ext:                evolution.WithDraftSuggestionIDs(nil, []string{"suggestion-1"}),
		UpdatedBy:          "u1",
		UpdatedByName:      "User 1",
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create preference: %v", err)
	}
	createPreferenceReviewResult(t, db, "preference-discard", "u1", orm.ResourceUpdateResourceTypeUserPreference, "---\nagent_persona: rejected\npreferred_name: rejected\nresponse_style: rejected\n---\n\nrejected preference", now)

	req := httptest.NewRequest(http.MethodPost, "/api/core/user-preference:discard", nil)
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	Discard(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if status := preferenceReviewResultStatus(t, db, "preference-discard"); status != "rejected" {
		t.Fatalf("expected review result rejected, got %q", status)
	}
	var updated orm.SystemUserPreference
	if err := db.Where("id = ?", row.ID).Take(&updated).Error; err != nil {
		t.Fatalf("query preference: %v", err)
	}
	if updated.Content != row.Content || updated.Version != row.Version {
		t.Fatalf("discard should not change preference content/version, got content=%q version=%d", updated.Content, updated.Version)
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func countPreferenceResourceVersions(t *testing.T, db *orm.DB, resourceID string) int64 {
	t.Helper()
	var count int64
	if err := db.Model(&orm.ResourceVersion{}).Where("resource_id = ?", resourceID).Count(&count).Error; err != nil {
		t.Fatalf("count resource versions: %v", err)
	}
	return count
}
