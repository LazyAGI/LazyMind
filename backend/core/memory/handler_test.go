package memory

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
	"lazymind/core/resourcefs"
	"lazymind/core/store"
)

type upsertMemoryAPITestResponse struct {
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

type draftPreviewMemoryAPITestResponse struct {
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

type generateMemoryAPITestResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		DraftStatus        string   `json:"draft_status"`
		DraftSourceVersion int64    `json:"draft_source_version"`
		DraftContent       string   `json:"draft_content"`
		SuggestionIDs      []string `json:"suggestion_ids"`
	} `json:"data"`
}

func newMemoryTestDB(t *testing.T) *orm.DB {
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

func createMemoryReviewResult(t *testing.T, db *orm.DB, id, userID, target, content string, at time.Time) {
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
		t.Fatalf("create memory review result: %v", err)
	}
}

func memoryReviewResultStatus(t *testing.T, db *orm.DB, id string) string {
	t.Helper()
	var row orm.MemoryReviewResult
	if err := db.Select("review_status").Where("id = ?", id).Take(&row).Error; err != nil {
		t.Fatalf("query memory review result %s: %v", id, err)
	}
	return row.ReviewStatus
}

func createMemoryResourceDraft(t *testing.T, db *orm.DB, userID, draftContent string) {
	t.Helper()
	row, err := evolution.LoadSystemMemory(context.Background(), db.DB, userID)
	if err != nil {
		t.Fatalf("load memory: %v", err)
	}
	service := resourcefs.NewService(resourcefs.ServiceDeps{DB: db.DB})
	ref := resourcefs.ResourceRef{UserID: userID, ResourceType: resourcefs.ResourceTypeMemory}
	if _, err := service.EnsureResource(context.Background(), ref, row.Content); err != nil {
		t.Fatalf("ensure memory resource: %v", err)
	}
	draft, err := service.ReadFile(context.Background(), resourcefs.ReadFileRequest{Ref: ref, RefType: resourcefs.FileRefDraft})
	if err != nil {
		t.Fatalf("read memory draft: %v", err)
	}
	if _, err := service.WriteDraft(context.Background(), resourcefs.WriteDraftRequest{
		Ref:                  ref,
		Content:              draftContent,
		ExpectedDraftVersion: draft.DraftVersion,
		UpdatedBy:            userID,
	}); err != nil {
		t.Fatalf("write memory draft: %v", err)
	}
}

func TestUpsertCreatesThenUpdatesMemory(t *testing.T) {
	db := newMemoryTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	firstReq := httptest.NewRequest(http.MethodPut, "/api/core/memory", strings.NewReader(`{"content":"第一版记忆内容"}`))
	firstReq.Header.Set("Content-Type", "application/json")
	firstReq.Header.Set("X-User-Id", "u1")
	firstReq.Header.Set("X-User-Name", "User 1")
	firstRec := httptest.NewRecorder()

	Upsert(firstRec, firstReq)

	if firstRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", firstRec.Code, firstRec.Body.String())
	}

	var firstResp upsertMemoryAPITestResponse
	if err := json.Unmarshal(firstRec.Body.Bytes(), &firstResp); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if firstResp.Data.ResourceType != "memory" {
		t.Fatalf("expected memory resource type, got %q", firstResp.Data.ResourceType)
	}
	if firstResp.Data.Content != "第一版记忆内容" {
		t.Fatalf("unexpected first content: %q", firstResp.Data.Content)
	}

	var created orm.SystemMemory
	if err := db.Where("user_id = ?", "u1").Take(&created).Error; err != nil {
		t.Fatalf("query created memory: %v", err)
	}
	if created.Version != 1 {
		t.Fatalf("expected created version 1, got %d", created.Version)
	}
	if !created.AutoEvo {
		t.Fatalf("expected created auto_evo to default true")
	}

	secondReq := httptest.NewRequest(http.MethodPut, "/api/core/memory", strings.NewReader(`{"content":"第二版记忆内容"}`))
	secondReq.Header.Set("Content-Type", "application/json")
	secondReq.Header.Set("X-User-Id", "u1")
	secondReq.Header.Set("X-User-Name", "User 1")
	secondRec := httptest.NewRecorder()

	Upsert(secondRec, secondReq)

	if secondRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", secondRec.Code, secondRec.Body.String())
	}

	var updated orm.SystemMemory
	if err := db.Where("user_id = ?", "u1").Take(&updated).Error; err != nil {
		t.Fatalf("query updated memory: %v", err)
	}
	if updated.ID != created.ID {
		t.Fatalf("expected update in place, got new id %q from old %q", updated.ID, created.ID)
	}
	if updated.Content != "第二版记忆内容" {
		t.Fatalf("unexpected updated content: %q", updated.Content)
	}
	if updated.Version != 2 {
		t.Fatalf("expected updated version 2, got %d", updated.Version)
	}
	if updated.UpdatedAt.Before(created.UpdatedAt) {
		t.Fatalf("expected updated_at to move forward")
	}
}

func TestUpsertPreservesMemoryAutoEvoWhenOmitted(t *testing.T) {
	db := newMemoryTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	createReq := httptest.NewRequest(http.MethodPut, "/api/core/memory", strings.NewReader(`{"content":"第一版记忆内容","auto_evo":false}`))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("X-User-Id", "u1")
	createReq.Header.Set("X-User-Name", "User 1")
	createRec := httptest.NewRecorder()

	Upsert(createRec, createReq)

	if createRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", createRec.Code, createRec.Body.String())
	}
	var created orm.SystemMemory
	if err := db.Where("user_id = ?", "u1").Take(&created).Error; err != nil {
		t.Fatalf("query created memory: %v", err)
	}
	if created.AutoEvo {
		t.Fatalf("expected explicit auto_evo=false to be persisted on create")
	}
	if created.AutoEvoGeneration != 0 {
		t.Fatalf("expected create to keep auto_evo_generation 0, got %d", created.AutoEvoGeneration)
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/core/memory", strings.NewReader(`{"content":"第二版记忆内容"}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateReq.Header.Set("X-User-Id", "u1")
	updateReq.Header.Set("X-User-Name", "User 1")
	updateRec := httptest.NewRecorder()

	Upsert(updateRec, updateReq)

	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", updateRec.Code, updateRec.Body.String())
	}
	var updated orm.SystemMemory
	if err := db.Where("user_id = ?", "u1").Take(&updated).Error; err != nil {
		t.Fatalf("query updated memory: %v", err)
	}
	if updated.AutoEvo {
		t.Fatalf("expected omitted auto_evo to preserve false")
	}
	if updated.AutoEvoGeneration != created.AutoEvoGeneration {
		t.Fatalf("expected omitted auto_evo to preserve generation %d, got %d", created.AutoEvoGeneration, updated.AutoEvoGeneration)
	}
	if updated.Content != "第二版记忆内容" {
		t.Fatalf("unexpected updated content: %q", updated.Content)
	}
}

func TestUpsertMemoryAutoEvoOnlyDoesNotCreateResourceVersion(t *testing.T) {
	db := newMemoryTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	createReq := httptest.NewRequest(http.MethodPut, "/api/core/memory", strings.NewReader(`{"content":"第一版记忆内容"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("X-User-Id", "u1")
	createReq.Header.Set("X-User-Name", "User 1")
	createRec := httptest.NewRecorder()

	Upsert(createRec, createReq)

	if createRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", createRec.Code, createRec.Body.String())
	}
	var row orm.SystemMemory
	if err := db.Where("user_id = ?", "u1").Take(&row).Error; err != nil {
		t.Fatalf("query created memory: %v", err)
	}
	if got := countMemoryResourceVersions(t, db, row.ID); got != 1 {
		t.Fatalf("expected create to write 1 resource version, got %d", got)
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/core/memory", strings.NewReader(`{"auto_evo":false}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateReq.Header.Set("X-User-Id", "u1")
	updateReq.Header.Set("X-User-Name", "User 1")
	updateRec := httptest.NewRecorder()

	Upsert(updateRec, updateReq)

	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", updateRec.Code, updateRec.Body.String())
	}
	if got := countMemoryResourceVersions(t, db, row.ID); got != 1 {
		t.Fatalf("expected auto_evo-only update to keep 1 resource version, got %d", got)
	}

	var version orm.ResourceVersion
	if err := db.Where("resource_id = ?", row.ID).Take(&version).Error; err != nil {
		t.Fatalf("query resource version: %v", err)
	}
	if version.ChangeSource != resourcechange.ChangeSourceDirectSave {
		t.Fatalf("expected direct_save version source, got %q", version.ChangeSource)
	}
}

func TestUpsertIgnoresMemoryMetadataFields(t *testing.T) {
	db := newMemoryTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	createReq := httptest.NewRequest(http.MethodPut, "/api/core/memory", strings.NewReader(`{"content":"长期记忆","agent_persona":"严谨助手","preferred_name":"老师","response_style":"先结论后解释"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("X-User-Id", "u1")
	createReq.Header.Set("X-User-Name", "User 1")
	createRec := httptest.NewRecorder()

	Upsert(createRec, createReq)

	if createRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", createRec.Code, createRec.Body.String())
	}
	var createResp upsertMemoryAPITestResponse
	if err := json.Unmarshal(createRec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if createResp.Data.Content != "长期记忆" {
		t.Fatalf("unexpected content in create response: %#v", createResp.Data)
	}
	if createResp.Data.AgentPersona != nil || createResp.Data.PreferredName != nil || createResp.Data.ResponseStyle != nil {
		t.Fatalf("expected memory metadata fields to be omitted, got %#v", createResp.Data)
	}

	var created orm.SystemMemory
	if err := db.Where("user_id = ?", "u1").Take(&created).Error; err != nil {
		t.Fatalf("query created memory: %v", err)
	}
	if created.ContentHash != evolution.HashSystemMemory(created) {
		t.Fatalf("expected memory content hash, got %q", created.ContentHash)
	}
}

func TestUpsertRejectsMemoryMetadataOnlyCreate(t *testing.T) {
	db := newMemoryTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	req := httptest.NewRequest(http.MethodPut, "/api/core/memory", strings.NewReader(`{"agent_persona":"严谨助手"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	Upsert(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	var count int64
	if err := db.Model(&orm.SystemMemory{}).Where("user_id = ?", "u1").Count(&count).Error; err != nil {
		t.Fatalf("count memory rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected metadata-only request not to create memory, got %d rows", count)
	}
}

func TestUpsertAutoEvoDiscardsPendingDraftWithoutOverwritingMemoryContent(t *testing.T) {
	db := newMemoryTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	row := orm.SystemMemory{
		ID:                 "memory-1",
		UserID:             "u1",
		Content:            "current memory",
		ContentHash:        evolution.HashContent("current memory"),
		Version:            7,
		DraftContent:       "draft memory",
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
		t.Fatalf("create memory: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/core/memory", strings.NewReader(`{"content":"request body should not win","auto_evo":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	Upsert(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var updated orm.SystemMemory
	if err := db.Where("id = ?", row.ID).Take(&updated).Error; err != nil {
		t.Fatalf("query updated memory: %v", err)
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
	db := newMemoryTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	row := orm.SystemMemory{
		ID:            "memory-1",
		UserID:        "u1",
		Content:       "current memory",
		ContentHash:   "hash-current",
		Version:       2,
		UpdatedBy:     "u1",
		UpdatedByName: "User 1",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create memory: %v", err)
	}
	createMemoryResourceDraft(t, db, "u1", "updated memory")

	req := httptest.NewRequest(http.MethodGet, "/api/core/memory:draft-preview", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	DraftPreview(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp draftPreviewMemoryAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	if resp.Data.ReviewResultID != "" {
		t.Fatalf("expected empty legacy review_result_id, got %q", resp.Data.ReviewResultID)
	}
	if resp.Data.ReviewStatus != "pending_confirm" || resp.Data.DraftStatus != "pending_confirm" {
		t.Fatalf("expected resourcefs pending status, got review_status=%q draft_status=%q", resp.Data.ReviewStatus, resp.Data.DraftStatus)
	}
	if resp.Data.CurrentContent != "current memory" {
		t.Fatalf("unexpected current content: %q", resp.Data.CurrentContent)
	}
	if resp.Data.DraftContent != "updated memory" {
		t.Fatalf("unexpected draft content: %q", resp.Data.DraftContent)
	}
	if !strings.Contains(resp.Data.Diff, "-current memory") {
		t.Fatalf("expected diff to contain removed current content, got %q", resp.Data.Diff)
	}
	if !strings.Contains(resp.Data.Diff, "+updated memory") {
		t.Fatalf("expected diff to contain added draft content, got %q", resp.Data.Diff)
	}
}

func TestDraftPreviewMigratesLegacyMemoryResourceDraft(t *testing.T) {
	db := newMemoryTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	row := orm.SystemMemory{
		ID:                 "memory-1",
		UserID:             "u1",
		Content:            "current memory",
		ContentHash:        evolution.HashContent("current memory"),
		Version:            2,
		DraftContent:       "legacy draft memory",
		DraftSourceVersion: 2,
		DraftStatus:        "pending_confirm",
		UpdatedBy:          "u1",
		UpdatedByName:      "User 1",
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create memory: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/memory:draft-preview", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	DraftPreview(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected legacy draft_content to migrate into resourcefs draft, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp draftPreviewMemoryAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.DraftContent != "legacy draft memory" {
		t.Fatalf("expected migrated resourcefs draft, got %q", resp.Data.DraftContent)
	}
}

func TestGenerateOverwritesExistingPendingDraft(t *testing.T) {
	db := newMemoryTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat/rewrite" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"content": "new draft content"},
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
	row := orm.SystemMemory{
		ID:            "memory-1",
		UserID:        "u1",
		Content:       "current memory",
		ContentHash:   evolution.HashContent("current memory"),
		Version:       3,
		Ext:           evolution.WithDraftSuggestionIDs(nil, []string{"old-suggestion"}),
		UpdatedBy:     "u1",
		UpdatedByName: "User 1",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create memory: %v", err)
	}
	createMemoryResourceDraft(t, db, "u1", "old draft content")
	suggestion := orm.ResourceSuggestion{
		ID:           "suggestion-1",
		UserID:       "u1",
		ResourceType: evolution.ResourceTypeMemory,
		ResourceKey:  evolution.SystemResourceKey(evolution.ResourceTypeMemory),
		Action:       "modify",
		SessionID:    "session-1",
		Title:        "memory suggestion",
		Content:      "update memory",
		Status:       "accepted",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := db.Create(&suggestion).Error; err != nil {
		t.Fatalf("create suggestion: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/memory:generate", strings.NewReader(`{"user_instruct":"生成新版"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	Generate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp generateMemoryAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	if resp.Data.DraftStatus != "pending_confirm" {
		t.Fatalf("expected pending_confirm, got %q", resp.Data.DraftStatus)
	}
	if resp.Data.DraftContent != "new draft content" {
		t.Fatalf("unexpected draft content: %q", resp.Data.DraftContent)
	}

	service := resourcefs.NewService(resourcefs.ServiceDeps{DB: db.DB})
	draft, err := service.ReadFile(context.Background(), resourcefs.ReadFileRequest{Ref: resourcefs.ResourceRef{UserID: "u1", ResourceType: resourcefs.ResourceTypeMemory}, RefType: resourcefs.FileRefDraft})
	if err != nil {
		t.Fatalf("read resourcefs draft: %v", err)
	}
	if draft.Content != "new draft content" {
		t.Fatalf("expected resourcefs draft to be overwritten, got %q", draft.Content)
	}
}

func TestGenerateUserInstructOnlyUsesDraftContent(t *testing.T) {
	db := newMemoryTestDB(t)
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
	row := orm.SystemMemory{
		ID:            "memory-1",
		UserID:        "u1",
		Content:       "current memory",
		ContentHash:   evolution.HashContent("current memory"),
		Version:       3,
		Ext:           evolution.WithDraftSuggestionIDs(nil, []string{"suggestion-1"}),
		UpdatedBy:     "u1",
		UpdatedByName: "User 1",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create memory: %v", err)
	}
	createMemoryResourceDraft(t, db, "u1", "draft memory")
	suggestion := orm.ResourceSuggestion{
		ID:           "suggestion-1",
		UserID:       "u1",
		ResourceType: evolution.ResourceTypeMemory,
		ResourceKey:  evolution.SystemResourceKey(evolution.ResourceTypeMemory),
		Action:       "modify",
		SessionID:    "session-1",
		Title:        "memory suggestion",
		Content:      "update memory",
		Status:       "accepted",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := db.Create(&suggestion).Error; err != nil {
		t.Fatalf("create suggestion: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/memory:generate", strings.NewReader(`{"user_instruct":"只按用户意见生成"}`))
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
	if algoBody["content"] != "draft memory" {
		t.Fatalf("expected draft content sent to algorithm, got %#v", algoBody["content"])
	}
	if algoBody["task_type"] != "memory" {
		t.Fatalf("expected memory task_type, got %#v", algoBody["task_type"])
	}
	if _, ok := algoBody["suggestions"]; ok {
		t.Fatalf("suggestions should not be sent to algorithm: %#v", algoBody["suggestions"])
	}
	confirmReq := httptest.NewRequest(http.MethodPost, "/api/core/memory:confirm", nil)
	confirmReq.Header.Set("X-User-Id", "u1")
	confirmReq.Header.Set("X-User-Name", "User 1")
	confirmRec := httptest.NewRecorder()

	Confirm(confirmRec, confirmReq)

	if confirmRec.Code != http.StatusOK {
		t.Fatalf("expected confirm status 200, got %d body=%s", confirmRec.Code, confirmRec.Body.String())
	}
	var confirmed orm.SystemMemory
	if err := db.Where("id = ?", row.ID).Take(&confirmed).Error; err != nil {
		t.Fatalf("query confirmed memory: %v", err)
	}
	if confirmed.Content != "draft from user instruction" || confirmed.Version != row.Version+1 {
		t.Fatalf("expected resourcefs draft content to be applied, got content=%q version=%d", confirmed.Content, confirmed.Version)
	}
}

func TestDiscardDoesNotTouchPendingMemoryReviewResult(t *testing.T) {
	db := newMemoryTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	row := orm.SystemMemory{
		ID:            "memory-1",
		UserID:        "u1",
		Content:       "current memory",
		ContentHash:   evolution.HashContent("current memory"),
		Version:       3,
		Ext:           evolution.WithDraftSuggestionIDs(nil, []string{"suggestion-1"}),
		UpdatedBy:     "u1",
		UpdatedByName: "User 1",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create memory: %v", err)
	}
	createMemoryResourceDraft(t, db, "u1", "resourcefs draft memory")
	createMemoryReviewResult(t, db, "memory-discard", "u1", orm.ResourceUpdateResourceTypeMemory, "review draft memory", now)

	req := httptest.NewRequest(http.MethodPost, "/api/core/memory:discard", nil)
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	Discard(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if status := memoryReviewResultStatus(t, db, "memory-discard"); status != "pending" {
		t.Fatalf("expected legacy review result to remain pending, got %q", status)
	}
	var updated orm.SystemMemory
	if err := db.Where("id = ?", row.ID).Take(&updated).Error; err != nil {
		t.Fatalf("query memory: %v", err)
	}
	if updated.Content != row.Content || updated.Version != row.Version {
		t.Fatalf("discard should not change memory content/version, got content=%q version=%d", updated.Content, updated.Version)
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func countMemoryResourceVersions(t *testing.T, db *orm.DB, resourceID string) int64 {
	t.Helper()
	var count int64
	if err := db.Model(&orm.ResourceVersion{}).Where("resource_id = ?", resourceID).Count(&count).Error; err != nil {
		t.Fatalf("count resource versions: %v", err)
	}
	return count
}
