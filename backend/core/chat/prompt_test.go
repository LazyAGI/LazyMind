package chat

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

	"github.com/gorilla/mux"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	corestore "lazymind/core/store"
)

func newPromptTestDB(t *testing.T) *orm.DB {
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

func TestPromptUsageUpsertQualifiesPostgresUsageCount(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN: "host=localhost user=postgres dbname=core sslmode=disable",
	}), &gorm.Config{
		DryRun:                 true,
		DisableAutomaticPing:   true,
		SkipDefaultTransaction: true,
	})
	if err != nil {
		t.Fatalf("open postgres dry-run db: %v", err)
	}

	now := time.Date(2026, time.July, 17, 8, 0, 0, 0, time.UTC)
	state := orm.PromptUserState{
		ID:             "pus_test",
		PromptID:       "preset-document-summary",
		UsageCount:     1,
		LastUsedAt:     &now,
		CreateUserID:   "u1",
		CreateUserName: "Prompt Tester",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	statement := db.Clauses(promptUsageConflictClause(now)).Create(&state).Statement
	if statement.Error != nil {
		t.Fatalf("build postgres upsert: %v", statement.Error)
	}
	sql := statement.SQL.String()
	if !strings.Contains(sql, `"prompt_user_states"."usage_count" +`) {
		t.Fatalf("usage_count is not qualified in postgres upsert: %s", sql)
	}
}

func TestPolishPromptCallsRewrite(t *testing.T) {
	db := newPromptTestDB(t)
	corestore.Init(db.DB, nil, nil)
	t.Cleanup(func() { corestore.Init(nil, nil, nil) })

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
		_ = json.NewEncoder(w).Encode(map[string]any{"content": "polished prompt"})
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listener unavailable in current test environment: %v", err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Shutdown(context.Background()) }()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", fmt.Sprintf("http://%s", listener.Addr().String()))

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/core/prompts:polish",
		strings.NewReader(`{"content":"raw prompt","user_instruct":"make it clear"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	PolishPrompt(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if algoBody["task_type"] != "polish" {
		t.Fatalf("expected polish task_type, got %#v", algoBody["task_type"])
	}
	if algoBody["content"] != "raw prompt" {
		t.Fatalf("expected content forwarded, got %#v", algoBody["content"])
	}
	if algoBody["user_instruct"] != "make it clear" {
		t.Fatalf("expected user_instruct forwarded, got %#v", algoBody["user_instruct"])
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["content"] != "polished prompt" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestListPromptsFiltersByKeyword(t *testing.T) {
	db := newPromptTestDB(t)
	corestore.Init(db.DB, nil, nil)
	t.Cleanup(func() { corestore.Init(nil, nil, nil) })

	now := time.Now().UTC()
	prompts := []orm.Prompt{
		{
			ID:      "p_alpha",
			Name:    "Alpha Summary",
			Content: "Summarize weekly operation notes.",
			BaseModel: orm.BaseModel{
				CreateUserID: "u1",
				CreatedAt:    now,
				UpdatedAt:    now,
			},
		},
		{
			ID:      "p_beta",
			Name:    "Beta Draft",
			Content: "Write a launch announcement.",
			BaseModel: orm.BaseModel{
				CreateUserID: "u1",
				CreatedAt:    now.Add(-time.Minute),
				UpdatedAt:    now.Add(-time.Minute),
			},
		},
	}
	if err := db.Create(&prompts).Error; err != nil {
		t.Fatalf("seed prompts: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/prompts?keyword=summary", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	ListPrompts(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Prompts []map[string]any `json:"prompts"`
		Total   int64            `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Total != 1 || len(resp.Prompts) != 1 {
		t.Fatalf("expected one filtered prompt, got total=%d prompts=%#v", resp.Total, resp.Prompts)
	}
	if resp.Prompts[0]["id"] != "p_alpha" {
		t.Fatalf("expected p_alpha, got %#v", resp.Prompts[0])
	}
}

func TestPromptLibraryFavoriteAndUsage(t *testing.T) {
	db := newPromptTestDB(t)
	corestore.Init(db.DB, nil, nil)
	t.Cleanup(func() { corestore.Init(nil, nil, nil) })

	request := func(handler http.HandlerFunc, userID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/core/prompts/preset-general-qa:action", nil)
		req = mux.SetURLVars(req, map[string]string{"name": "preset-general-qa"})
		req.Header.Set("X-User-Id", userID)
		req.Header.Set("X-User-Name", "Prompt Tester")
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}

	for i := 0; i < 2; i++ {
		if rec := request(FavoritePrompt, "u1"); rec.Code != http.StatusOK {
			t.Fatalf("favorite attempt %d failed: status=%d body=%s", i+1, rec.Code, rec.Body.String())
		}
	}
	for i := 0; i < 2; i++ {
		rec := request(UsePrompt, "u1")
		if rec.Code != http.StatusOK {
			t.Fatalf("use attempt %d failed: status=%d body=%s", i+1, rec.Code, rec.Body.String())
		}
		var stateResp promptStateResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &stateResp); err != nil {
			t.Fatalf("decode use attempt %d response: %v", i+1, err)
		}
		if stateResp.ID != "preset-general-qa" || !stateResp.IsFavorite || stateResp.UsageCount != int64(i+1) || stateResp.LastUsedAt == nil {
			t.Fatalf("unexpected use attempt %d response: %#v", i+1, stateResp)
		}
	}

	var states []orm.PromptUserState
	if err := db.Where("create_user_id = ? AND prompt_id = ?", "u1", "preset-general-qa").Find(&states).Error; err != nil {
		t.Fatalf("load prompt states: %v", err)
	}
	if len(states) != 1 || !states[0].IsFavorite || states[0].UsageCount != 2 || states[0].LastUsedAt == nil {
		t.Fatalf("unexpected prompt state: %#v", states)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/core/prompts?scope=recent&locale=en-US", nil)
	listReq.Header.Set("X-User-Id", "u1")
	listRec := httptest.NewRecorder()
	ListPrompts(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list recent prompts failed: status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	var listResp struct {
		Prompts []promptItemResponse `json:"prompts"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode recent prompts: %v", err)
	}
	if len(listResp.Prompts) != 1 || listResp.Prompts[0].ID != "preset-general-qa" || listResp.Prompts[0].UsageCount != 2 {
		t.Fatalf("unexpected recent prompts: %#v", listResp.Prompts)
	}

	otherReq := httptest.NewRequest(http.MethodGet, "/api/core/prompts?scope=favorite", nil)
	otherReq.Header.Set("X-User-Id", "u2")
	otherRec := httptest.NewRecorder()
	ListPrompts(otherRec, otherReq)
	var otherResp struct {
		Prompts []promptItemResponse `json:"prompts"`
	}
	if err := json.Unmarshal(otherRec.Body.Bytes(), &otherResp); err != nil {
		t.Fatalf("decode other user prompts: %v", err)
	}
	if len(otherResp.Prompts) != 0 {
		t.Fatalf("favorite state leaked across users: %#v", otherResp.Prompts)
	}
}

func TestPromptLibraryFacetsRespectCategoryAndScope(t *testing.T) {
	db := newPromptTestDB(t)
	corestore.Init(db.DB, nil, nil)
	t.Cleanup(func() { corestore.Init(nil, nil, nil) })

	now := time.Date(2026, time.July, 17, 9, 0, 0, 0, time.UTC)
	states := []orm.PromptUserState{
		{
			ID:             "pus_general",
			PromptID:       "preset-general-qa",
			IsFavorite:     true,
			UsageCount:     2,
			LastUsedAt:     &now,
			CreateUserID:   "u1",
			CreateUserName: "Prompt Tester",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		{
			ID:             "pus_document",
			PromptID:       "preset-document-summary",
			IsFavorite:     true,
			CreateUserID:   "u1",
			CreateUserName: "Prompt Tester",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	if err := db.Create(&states).Error; err != nil {
		t.Fatalf("create prompt states: %v", err)
	}

	request := func(query string) promptListResponse {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/core/prompts?"+query, nil)
		req.Header.Set("X-User-Id", "u1")
		rec := httptest.NewRecorder()
		ListPrompts(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("list prompts failed: status=%d body=%s", rec.Code, rec.Body.String())
		}
		var response promptListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode prompt list: %v", err)
		}
		return response
	}

	generalAll := request("category=general&scope=all")
	if generalAll.Total != 1 || len(generalAll.Prompts) != 1 || generalAll.Prompts[0].ID != "preset-general-qa" {
		t.Fatalf("unexpected general prompt list: %#v", generalAll)
	}
	if generalAll.Facets.Scopes["all"] != 1 || generalAll.Facets.Scopes["recent"] != 1 || generalAll.Facets.Scopes["favorite"] != 1 || generalAll.Facets.Scopes["custom"] != 0 {
		t.Fatalf("unexpected general scope facets: %#v", generalAll.Facets.Scopes)
	}
	if generalAll.Facets.CategoryTotal != 3 || generalAll.Facets.Categories["general"] != 1 || generalAll.Facets.Categories["document_processing"] != 1 || generalAll.Facets.Categories["information_extraction"] != 1 {
		t.Fatalf("unexpected all-scope category facets: %#v", generalAll.Facets)
	}

	generalFavorite := request("category=general&scope=favorite")
	if generalFavorite.Total != 1 || len(generalFavorite.Prompts) != 1 {
		t.Fatalf("unexpected favorite general list: %#v", generalFavorite)
	}
	if generalFavorite.Facets.CategoryTotal != 2 || generalFavorite.Facets.Categories["general"] != 1 || generalFavorite.Facets.Categories["document_processing"] != 1 || generalFavorite.Facets.Categories["information_extraction"] != 0 {
		t.Fatalf("unexpected favorite category facets: %#v", generalFavorite.Facets)
	}
	if generalFavorite.Facets.Scopes["all"] != 1 || generalFavorite.Facets.Scopes["recent"] != 1 || generalFavorite.Facets.Scopes["favorite"] != 1 {
		t.Fatalf("scope facets must ignore the selected scope: %#v", generalFavorite.Facets.Scopes)
	}

	informationFavorite := request("category=information_extraction&scope=favorite")
	if informationFavorite.Total != 0 || len(informationFavorite.Prompts) != 0 {
		t.Fatalf("unexpected favorite information extraction list: %#v", informationFavorite)
	}
	if informationFavorite.Facets.Scopes["all"] != 1 || informationFavorite.Facets.Scopes["recent"] != 0 || informationFavorite.Facets.Scopes["favorite"] != 0 {
		t.Fatalf("unexpected information extraction scope facets: %#v", informationFavorite.Facets.Scopes)
	}
	if informationFavorite.Facets.CategoryTotal != 2 || informationFavorite.Facets.Categories["general"] != 1 || informationFavorite.Facets.Categories["document_processing"] != 1 {
		t.Fatalf("category facets must ignore the selected category: %#v", informationFavorite.Facets)
	}
}

func TestPromptLibraryRejectsInvalidCategoryAndPresetMutation(t *testing.T) {
	db := newPromptTestDB(t)
	corestore.Init(db.DB, nil, nil)
	t.Cleanup(func() { corestore.Init(nil, nil, nil) })

	createReq := httptest.NewRequest(
		http.MethodPost,
		"/api/core/prompts",
		strings.NewReader(`{"display_name":"Bad category","content":"Content","category":"unknown"}`),
	)
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("X-User-Id", "u1")
	createRec := httptest.NewRecorder()
	CreatePrompt(createRec, createReq)
	if createRec.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid category status 400, got %d body=%s", createRec.Code, createRec.Body.String())
	}

	updateReq := httptest.NewRequest(
		http.MethodPatch,
		"/api/core/prompts/preset-general-qa",
		strings.NewReader(`{"display_name":"Changed"}`),
	)
	updateReq = mux.SetURLVars(updateReq, map[string]string{"name": "preset-general-qa"})
	updateReq.Header.Set("Content-Type", "application/json")
	updateReq.Header.Set("X-User-Id", "u1")
	updateRec := httptest.NewRecorder()
	UpdatePrompt(updateRec, updateReq)
	if updateRec.Code != http.StatusForbidden {
		t.Fatalf("expected preset mutation status 403, got %d body=%s", updateRec.Code, updateRec.Body.String())
	}
}

func TestPromptCategoryLifecycleAndUserIsolation(t *testing.T) {
	db := newPromptTestDB(t)
	corestore.Init(db.DB, nil, nil)
	t.Cleanup(func() { corestore.Init(nil, nil, nil) })

	createCategoryReq := httptest.NewRequest(
		http.MethodPost,
		"/api/core/prompt_categories",
		strings.NewReader(`{"name":"合同审查"}`),
	)
	createCategoryReq.Header.Set("Content-Type", "application/json")
	createCategoryReq.Header.Set("X-User-Id", "u1")
	createCategoryReq.Header.Set("X-User-Name", "User One")
	createCategoryRec := httptest.NewRecorder()
	CreatePromptCategory(createCategoryRec, createCategoryReq)
	if createCategoryRec.Code != http.StatusOK {
		t.Fatalf("create category failed: status=%d body=%s", createCategoryRec.Code, createCategoryRec.Body.String())
	}
	var category promptCategoryResponse
	if err := json.Unmarshal(createCategoryRec.Body.Bytes(), &category); err != nil {
		t.Fatalf("decode category: %v", err)
	}
	if category.ID == "" || category.Name != "合同审查" {
		t.Fatalf("unexpected category: %#v", category)
	}

	createPrompt := func(userID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(
			http.MethodPost,
			"/api/core/prompts",
			strings.NewReader(fmt.Sprintf(`{"display_name":"Review","content":"Review this contract","category":%q}`, category.ID)),
		)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-Id", userID)
		rec := httptest.NewRecorder()
		CreatePrompt(rec, req)
		return rec
	}
	if rec := createPrompt("u1"); rec.Code != http.StatusOK {
		t.Fatalf("create prompt with own category failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := createPrompt("u2"); rec.Code != http.StatusBadRequest {
		t.Fatalf("other user reused category: status=%d body=%s", rec.Code, rec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/core/prompts?category="+category.ID, nil)
	listReq.Header.Set("X-User-Id", "u1")
	listRec := httptest.NewRecorder()
	ListPrompts(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list category prompts failed: status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	var listResp promptListResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode category list: %v", err)
	}
	if len(listResp.Prompts) != 1 || len(listResp.CustomCategories) != 1 {
		t.Fatalf("unexpected prompt category list: %#v", listResp)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/core/prompt_categories/"+category.ID, nil)
	deleteReq = mux.SetURLVars(deleteReq, map[string]string{"name": category.ID})
	deleteReq.Header.Set("X-User-Id", "u1")
	deleteRec := httptest.NewRecorder()
	DeletePromptCategory(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete category failed: status=%d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
	var prompt orm.Prompt
	if err := db.Where("create_user_id = ? AND name = ?", "u1", "Review").First(&prompt).Error; err != nil {
		t.Fatalf("load reassigned prompt: %v", err)
	}
	if prompt.Category != "custom" {
		t.Fatalf("deleted category prompt was not reassigned: category=%q", prompt.Category)
	}
}
