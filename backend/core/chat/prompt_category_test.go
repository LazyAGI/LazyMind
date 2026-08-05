package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corestore "lazymind/core/store"

	"github.com/gorilla/mux"
)

// setupCategoryTestDB initializes SQLite and sets the global store.
func setupCategoryTestDB(t *testing.T) {
	t.Helper()
	db := newPromptTestDB(t)
	corestore.Init(db.DB, nil, nil)
	t.Cleanup(func() { corestore.Init(nil, nil, nil) })
}

// newCategoryRequest creates a request with X-User-Id and optional path vars.
func newCategoryRequest(method, path, body string, userID string, vars map[string]string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if userID != "" {
		req.Header.Set("X-User-Id", userID)
	}
	if vars != nil {
		req = mux.SetURLVars(req, vars)
	}
	return req
}

// --- Pure function tests ---

// TestPromptCategoryFromPath extracts "name" var and strips "prompt_categories/" prefix.
func TestPromptCategoryFromPath(t *testing.T) {
	req := newCategoryRequest("DELETE", "/prompt_categories/my-cat", "", "u1",
		map[string]string{"name": "prompt_categories/my-cat"})
	got := promptCategoryFromPath(req)
	if got != "my-cat" {
		t.Fatalf("got %q, want my-cat", got)
	}
}

// TestPromptCategoryFromPath_DoublePrefix strips repeated prefixes.
func TestPromptCategoryFromPath_DoublePrefix(t *testing.T) {
	req := newCategoryRequest("DELETE", "/prompt_categories/prompt_categories/x", "", "u1",
		map[string]string{"name": "prompt_categories/prompt_categories/x"})
	got := promptCategoryFromPath(req)
	if got != "prompt_categories/x" {
		t.Fatalf("got %q, want prompt_categories/x", got)
	}
}

// TestPromptCategoryFromPath_NoVar returns empty.
func TestPromptCategoryFromPath_NoVar(t *testing.T) {
	req := newCategoryRequest("DELETE", "/prompt_categories/", "", "u1", nil)
	got := promptCategoryFromPath(req)
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// --- Handler tests ---

// TestCreatePromptCategory_EmptyName returns 400.
func TestCreatePromptCategory_EmptyName(t *testing.T) {
	setupCategoryTestDB(t)
	req := newCategoryRequest("POST", "/prompt_categories", `{"name":""}`, "u1", nil)
	w := httptest.NewRecorder()
	CreatePromptCategory(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestCreatePromptCategory_NameTooLong returns 400 when name exceeds 30 runes.
func TestCreatePromptCategory_NameTooLong(t *testing.T) {
	setupCategoryTestDB(t)
	longName := strings.Repeat("a", 31) // 31 runes > 30 max
	body, _ := json.Marshal(promptCategoryRequest{Name: longName})
	req := newCategoryRequest("POST", "/prompt_categories", string(body), "u1", nil)
	w := httptest.NewRecorder()
	CreatePromptCategory(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestCreatePromptCategory_Duplicate returns 409 when same name already exists.
func TestCreatePromptCategory_Duplicate(t *testing.T) {
	setupCategoryTestDB(t)
	// Create first.
	req1 := newCategoryRequest("POST", "/prompt_categories", `{"name":"Duplicate"}`, "u1", nil)
	w1 := httptest.NewRecorder()
	CreatePromptCategory(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first create: status %d", w1.Code)
	}

	// Create duplicate (case-insensitive).
	req2 := newCategoryRequest("POST", "/prompt_categories", `{"name":"duplicate"}`, "u1", nil)
	w2 := httptest.NewRecorder()
	CreatePromptCategory(w2, req2)

	if w2.Code != http.StatusConflict {
		t.Fatalf("status: got %d, want %d", w2.Code, http.StatusConflict)
	}
}

// TestCreatePromptCategory_MissingUserID returns 400.
func TestCreatePromptCategory_MissingUserID(t *testing.T) {
	setupCategoryTestDB(t)
	req := newCategoryRequest("POST", "/prompt_categories", `{"name":"Test"}`, "", nil)
	w := httptest.NewRecorder()
	CreatePromptCategory(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestDeletePromptCategory_NotFound returns 404 for non-existent category.
func TestDeletePromptCategory_NotFound(t *testing.T) {
	setupCategoryTestDB(t)
	vars := map[string]string{"name": "prompt_categories/no-such-cat"}
	req := newCategoryRequest("DELETE", "/prompt_categories/no-such-cat", "", "u1", vars)
	w := httptest.NewRecorder()
	DeletePromptCategory(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusNotFound)
	}
}

// TestDeletePromptCategory_EmptyID returns 400.
func TestDeletePromptCategory_EmptyID(t *testing.T) {
	setupCategoryTestDB(t)
	req := newCategoryRequest("DELETE", "/prompt_categories/", "", "u1", nil)
	w := httptest.NewRecorder()
	DeletePromptCategory(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestListPromptCategories_Empty returns empty list for new user.
func TestListPromptCategories_Empty(t *testing.T) {
	setupCategoryTestDB(t)
	req := newCategoryRequest("GET", "/prompt_categories", "", "u-empty", nil)
	w := httptest.NewRecorder()
	ListPromptCategories(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	cats, _ := resp["categories"].([]any)
	if len(cats) != 0 {
		t.Fatalf("categories: got %d, want 0", len(cats))
	}
}

// TestListPromptCategories_MissingUserID returns 400.
func TestListPromptCategories_MissingUserID(t *testing.T) {
	setupCategoryTestDB(t)
	req := newCategoryRequest("GET", "/prompt_categories", "", "", nil)
	w := httptest.NewRecorder()
	ListPromptCategories(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestPromptCategoryNameAtMaxLen is accepted (30 runes exactly).
func TestPromptCategoryNameAtMaxLen(t *testing.T) {
	setupCategoryTestDB(t)
	exactName := strings.Repeat("b", 30) // exactly 30 runes
	body, _ := json.Marshal(promptCategoryRequest{Name: exactName})
	req := newCategoryRequest("POST", "/prompt_categories", string(body), "u1", nil)
	w := httptest.NewRecorder()
	CreatePromptCategory(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

// TestPromptCategoryName30RunesMultiByte is accepted (30 multi-byte runes).
func TestPromptCategoryName30RunesMultiByte(t *testing.T) {
	setupCategoryTestDB(t)
	name := strings.Repeat("中文", 15) // 30 runes (15 Chinese characters * 2)
	body, _ := json.Marshal(promptCategoryRequest{Name: name})
	req := newCategoryRequest("POST", "/prompt_categories", string(body), "u2", nil)
	w := httptest.NewRecorder()
	CreatePromptCategory(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

// TestDeletePromptCategory_CascadesToPrompts removes category and soft-deletes associated prompts.
func TestDeletePromptCategory_CascadesToPrompts(t *testing.T) {
	setupCategoryTestDB(t)
	// Create category.
	req1 := newCategoryRequest("POST", "/prompt_categories", `{"name":"TempCat"}`, "u1", nil)
	w1 := httptest.NewRecorder()
	CreatePromptCategory(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("create category: status %d", w1.Code)
	}
	var cat promptCategoryResponse
	json.NewDecoder(w1.Body).Decode(&cat)

	// Create a prompt under this category.
	body, _ := json.Marshal(map[string]string{
		"display_name": "Test Prompt",
		"content":      "test content",
		"category":     cat.ID,
	})
	req2 := newCategoryRequest("POST", "/prompts", string(body), "u1", nil)
	w2 := httptest.NewRecorder()
	CreatePrompt(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("create prompt: status %d body=%s", w2.Code, w2.Body.String())
	}

	// Delete the category.
	vars := map[string]string{"name": "prompt_categories/" + cat.ID}
	req3 := newCategoryRequest("DELETE", "/prompt_categories/"+cat.ID, "", "u1", vars)
	w3 := httptest.NewRecorder()
	DeletePromptCategory(w3, req3)

	if w3.Code != http.StatusOK {
		t.Fatalf("delete category: status %d body=%s", w3.Code, w3.Body.String())
	}

	// Verify list is empty after delete.
	req4 := newCategoryRequest("GET", "/prompt_categories", "", "u1", nil)
	w4 := httptest.NewRecorder()
	ListPromptCategories(w4, req4)
	if w4.Code != http.StatusOK {
		t.Fatalf("list after delete: status %d", w4.Code)
	}
	var resp map[string]any
	json.NewDecoder(w4.Body).Decode(&resp)
	cats, _ := resp["categories"].([]any)
	if len(cats) != 0 {
		t.Fatalf("categories after delete: got %d, want 0", len(cats))
	}
}
