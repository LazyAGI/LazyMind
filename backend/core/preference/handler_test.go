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
	"lazymind/core/store"
)

type upsertPreferenceAPITestResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		ResourceID     string `json:"resource_id"`
		ResourceType   string `json:"resource_type"`
		Title          string `json:"title"`
		Content        string `json:"content"`
		ContentSummary string `json:"content_summary"`
	} `json:"data"`
}

type draftPreviewPreferenceAPITestResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
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

func TestUpsertAutoEvoReturnsConflictWhenWorkerRunning(t *testing.T) {
	db := newPreferenceTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	row := orm.SystemUserPreference{
		ID:                 "preference-1",
		UserID:             "u1",
		Content:            "current preference",
		ContentHash:        evolution.HashContent("current preference"),
		Version:            2,
		AutoEvo:            false,
		AutoEvoApplyStatus: evolution.AutoEvoApplyStatusRunning,
		AutoEvoGeneration:  7,
		UpdatedBy:          "u1",
		UpdatedByName:      "User 1",
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create preference: %v", err)
	}
	workerKey := evolution.AutoEvoWorkerKey(evolution.ResourceTypeUserPreference, row.ID)
	if !evolution.TryAcquireAutoEvoWorker(workerKey) {
		t.Fatalf("expected to acquire worker lock")
	}
	t.Cleanup(func() { evolution.ReleaseAutoEvoWorker(workerKey) })

	req := httptest.NewRequest(http.MethodPut, "/api/core/user-preference", strings.NewReader(`{"content":"new preference","auto_evo":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	Upsert(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	var updated orm.SystemUserPreference
	if err := db.Where("id = ?", row.ID).Take(&updated).Error; err != nil {
		t.Fatalf("query updated preference: %v", err)
	}
	if updated.Content != row.Content || updated.Version != row.Version || updated.AutoEvo != row.AutoEvo {
		t.Fatalf("expected preference fields unchanged, got content=%q version=%d auto_evo=%v", updated.Content, updated.Version, updated.AutoEvo)
	}
	if updated.AutoEvoGeneration != row.AutoEvoGeneration || updated.AutoEvoApplyStatus != row.AutoEvoApplyStatus {
		t.Fatalf("expected auto_evo state unchanged, got generation=%d status=%q", updated.AutoEvoGeneration, updated.AutoEvoApplyStatus)
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
		ContentHash:        "hash-current",
		Version:            3,
		DraftContent:       "updated preference",
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
	if resp.Data.DraftStatus != "pending_confirm" {
		t.Fatalf("expected pending_confirm, got %q", resp.Data.DraftStatus)
	}
	if resp.Data.CurrentContent != "current preference" {
		t.Fatalf("unexpected current content: %q", resp.Data.CurrentContent)
	}
	if resp.Data.DraftContent != "updated preference" {
		t.Fatalf("unexpected draft content: %q", resp.Data.DraftContent)
	}
	if !strings.Contains(resp.Data.Diff, "-current preference") {
		t.Fatalf("expected diff to contain removed current content, got %q", resp.Data.Diff)
	}
	if !strings.Contains(resp.Data.Diff, "+updated preference") {
		t.Fatalf("expected diff to contain added draft content, got %q", resp.Data.Diff)
	}
}

func TestGenerateOverwritesExistingPendingDraft(t *testing.T) {
	db := newPreferenceTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat/user_preference/generate" {
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
		Action:       evolution.SuggestionActionModify,
		SessionID:    "session-1",
		Title:        "preference suggestion",
		Content:      "update preference",
		Status:       evolution.SuggestionStatusAccepted,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := db.Create(&suggestion).Error; err != nil {
		t.Fatalf("create suggestion: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/user-preference:generate", strings.NewReader(`{"suggestion_ids":["suggestion-1"],"user_instruct":"生成新版"}`))
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
	if len(gotIDs) != 1 || gotIDs[0] != "suggestion-1" {
		t.Fatalf("expected draft suggestion ids to be replaced, got %#v", gotIDs)
	}
}

func TestGenerateAllowsSuggestionsWithoutUserInstruct(t *testing.T) {
	db := newPreferenceTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	var algoBody map[string]any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat/user_preference/generate" {
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
		Action:       evolution.SuggestionActionModify,
		SessionID:    "session-1",
		Title:        "preference suggestion",
		Content:      "update preference",
		Status:       evolution.SuggestionStatusAccepted,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := db.Create(&suggestion).Error; err != nil {
		t.Fatalf("create suggestion: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/user-preference:generate", strings.NewReader(`{"suggestion_ids":["suggestion-1"],"user_instruct":""}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	Generate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if algoBody["user_instruct"] != "" {
		t.Fatalf("expected empty user_instruct sent to algorithm, got %#v", algoBody["user_instruct"])
	}
	suggestions, ok := algoBody["suggestions"].([]any)
	if !ok || len(suggestions) != 1 {
		t.Fatalf("expected one suggestion sent to algorithm, got %#v", algoBody["suggestions"])
	}
}

func TestGenerateUserInstructOnlyUsesDraftContent(t *testing.T) {
	db := newPreferenceTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	var algoBody map[string]any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat/user_preference/generate" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&algoBody); err != nil {
			t.Fatalf("decode algorithm request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"content": "draft from user instruction"},
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
		DraftContent:       "draft preference",
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
	suggestion := orm.ResourceSuggestion{
		ID:           "suggestion-1",
		UserID:       "u1",
		ResourceType: evolution.ResourceTypeUserPreference,
		ResourceKey:  evolution.SystemResourceKey(evolution.ResourceTypeUserPreference),
		Action:       evolution.SuggestionActionModify,
		SessionID:    "session-1",
		Title:        "preference suggestion",
		Content:      "update preference",
		Status:       evolution.SuggestionStatusAccepted,
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
	if algoBody["content"] != "draft preference" {
		t.Fatalf("expected draft content sent to algorithm, got %#v", algoBody["content"])
	}
	suggestions, ok := algoBody["suggestions"].([]any)
	if !ok || len(suggestions) != 0 {
		t.Fatalf("expected empty suggestions array, got %#v", algoBody["suggestions"])
	}
	var updated orm.SystemUserPreference
	if err := db.Where("id = ?", row.ID).Take(&updated).Error; err != nil {
		t.Fatalf("query updated preference: %v", err)
	}
	gotIDs := evolution.DraftSuggestionIDs(updated.Ext)
	if len(gotIDs) != 1 || gotIDs[0] != "suggestion-1" {
		t.Fatalf("expected draft suggestion ids to be preserved, got %#v", gotIDs)
	}

	confirmReq := httptest.NewRequest(http.MethodPost, "/api/core/user-preference:confirm", nil)
	confirmReq.Header.Set("X-User-Id", "u1")
	confirmReq.Header.Set("X-User-Name", "User 1")
	confirmRec := httptest.NewRecorder()

	Confirm(confirmRec, confirmReq)

	if confirmRec.Code != http.StatusOK {
		t.Fatalf("expected confirm status 200, got %d body=%s", confirmRec.Code, confirmRec.Body.String())
	}
	var applied orm.ResourceSuggestion
	if err := db.Where("id = ?", "suggestion-1").Take(&applied).Error; err != nil {
		t.Fatalf("query applied suggestion: %v", err)
	}
	if applied.Status != evolution.SuggestionStatusApplied {
		t.Fatalf("expected suggestion to be applied after confirm, got %q", applied.Status)
	}
}

func TestDiscardKeepsAcceptedSuggestionVisibleForRegeneration(t *testing.T) {
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
		DraftContent:       "draft preference",
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
	suggestion := orm.ResourceSuggestion{
		ID:           "suggestion-1",
		UserID:       "u1",
		ResourceType: evolution.ResourceTypeUserPreference,
		ResourceKey:  evolution.SystemResourceKey(evolution.ResourceTypeUserPreference),
		Action:       evolution.SuggestionActionModify,
		SessionID:    "session-1",
		Title:        "preference suggestion",
		Content:      "update preference",
		Status:       evolution.SuggestionStatusAccepted,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := db.Create(&suggestion).Error; err != nil {
		t.Fatalf("create suggestion: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/user-preference:discard", nil)
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	Discard(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var updated orm.ResourceSuggestion
	if err := db.Where("id = ?", "suggestion-1").Take(&updated).Error; err != nil {
		t.Fatalf("query suggestion: %v", err)
	}
	if updated.Status != evolution.SuggestionStatusAccepted {
		t.Fatalf("expected suggestion to remain accepted after discard, got %q", updated.Status)
	}
}
