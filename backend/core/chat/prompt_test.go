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
		if rec := request(UsePrompt, "u1"); rec.Code != http.StatusOK {
			t.Fatalf("use attempt %d failed: status=%d body=%s", i+1, rec.Code, rec.Body.String())
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
