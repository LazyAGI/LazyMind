package wordgroup

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"

	"github.com/gorilla/mux"
)

// --- Test helpers ---

// setupWordGroupTest creates a SQLite-backed store with Word and WordGroupConflict tables migrated.
func setupWordGroupTest(t *testing.T) {
	t.Helper()
	ormDB := orm.MigrateTestDB(t, &orm.Word{}, &orm.WordGroupConflict{})
	store.Init(ormDB.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
}

// newWordGroupRequest builds a request with X-User-Id header and optional mux vars.
func newWordGroupRequest(method, path, body string, vars map[string]string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "user-test-1")
	req.Header.Set("X-User-Name", "Test User")
	if vars != nil {
		req = mux.SetURLVars(req, vars)
	}
	return req
}

// decodeWordGroupResponse reads the common APIResponse JSON.
func decodeWordGroupResponse(t *testing.T, w *httptest.ResponseRecorder) common.APIResponse {
	t.Helper()
	var resp common.APIResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

// --- Pure function tests ---

// TestNormalizeSource maps user/AI/empty/unknown source strings to canonical values.
func TestNormalizeSource(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"user", "user"},
		{"USER", "user"},
		{"  User  ", "user"},
		{"用户", "user"},
		{"ai", "ai"},
		{"AI", "ai"},
		{"系统", "ai"},
		{"", "user"},
		{"  ", "user"},
		{"unknown", ""},
		{"random", ""},
	}
	for _, tt := range tests {
		got := normalizeSource(tt.input)
		if got != tt.want {
			t.Fatalf("normalizeSource(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestValidateTermAndAliases rejects duplicate aliases and alias matching the term.
func TestValidateTermAndAliases(t *testing.T) {
	if msg := validateTermAndAliases("hello", []string{"world", "greeting"}); msg != "" {
		t.Fatalf("expected ok, got %q", msg)
	}
	if msg := validateTermAndAliases("hello", []string{"hello", "world"}); msg == "" {
		t.Fatal("expected error when alias matches term")
	}
	if msg := validateTermAndAliases("hello", []string{"dup", "dup"}); msg == "" {
		t.Fatal("expected error for duplicate aliases")
	}
	if msg := validateTermAndAliases("hello", nil); msg != "" {
		t.Fatalf("expected ok, got %q", msg)
	}
}

// TestNormalizeAliases trims whitespace and removes empty entries.
func TestNormalizeAliases(t *testing.T) {
	got := normalizeAliases([]string{" a ", "", "b", "  c  "})
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("normalizeAliases: got %v, want [a b c]", got)
	}
	if got := normalizeAliases(nil); len(got) != 0 {
		t.Fatalf("normalizeAliases(nil): expected empty, got %v", got)
	}
}

// TestUniqueWordCandidates merges term and aliases, deduplicates, preserves order.
func TestUniqueWordCandidates(t *testing.T) {
	got := uniqueWordCandidates("hello", []string{"hello", "world"})
	if len(got) != 2 || got[0] != "hello" || got[1] != "world" {
		t.Fatalf("uniqueWordCandidates: got %v, want [hello world]", got)
	}
	got = uniqueWordCandidates("", nil)
	if len(got) != 0 {
		t.Fatalf("uniqueWordCandidates(empty): expected empty, got %v", got)
	}
}

// --- Handler tests ---

// TestCreateWordGroup_Success creates a term with aliases.
func TestCreateWordGroup_Success(t *testing.T) {
	setupWordGroupTest(t)
	body := `{"term":"apple","aliases":["fruit","malus"],"description":"a fruit"}`
	req := newWordGroupRequest("POST", "/word_group", body, nil)
	w := httptest.NewRecorder()
	CreateWordGroup(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}
	resp := decodeWordGroupResponse(t, w)
	if resp.Code != 0 {
		t.Fatalf("response code: got %d, want 0", resp.Code)
	}
	var cwr CreateWordGroupResponse
	dataJSON, _ := json.Marshal(resp.Data)
	json.Unmarshal(dataJSON, &cwr)
	if cwr.Term != "apple" {
		t.Fatalf("term: got %q, want apple", cwr.Term)
	}
	if len(cwr.Aliases) != 2 {
		t.Fatalf("aliases: got %d, want 2", len(cwr.Aliases))
	}
	if cwr.GroupID == "" {
		t.Fatal("expected non-empty group_id")
	}
}

// TestCreateWordGroup_EmptyTerm returns 400.
func TestCreateWordGroup_EmptyTerm(t *testing.T) {
	setupWordGroupTest(t)
	body := `{"term":"","aliases":["x"]}`
	req := newWordGroupRequest("POST", "/word_group", body, nil)
	w := httptest.NewRecorder()
	CreateWordGroup(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestCreateWordGroup_TermMatchesAlias returns 400.
func TestCreateWordGroup_TermMatchesAlias(t *testing.T) {
	setupWordGroupTest(t)
	body := `{"term":"apple","aliases":["apple"]}`
	req := newWordGroupRequest("POST", "/word_group", body, nil)
	w := httptest.NewRecorder()
	CreateWordGroup(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestCreateWordGroup_MissingUserID returns 400.
func TestCreateWordGroup_MissingUserID(t *testing.T) {
	setupWordGroupTest(t)
	req := httptest.NewRequest("POST", "/word_group", strings.NewReader(`{"term":"apple"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	CreateWordGroup(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestDeleteWordGroup_Success creates then soft-deletes a group.
func TestDeleteWordGroup_Success(t *testing.T) {
	setupWordGroupTest(t)
	body := `{"term":"delete-me"}`
	req := newWordGroupRequest("POST", "/word_group", body, nil)
	w := httptest.NewRecorder()
	CreateWordGroup(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("create status: got %d", w.Code)
	}
	resp := decodeWordGroupResponse(t, w)
	var cwr CreateWordGroupResponse
	dataJSON, _ := json.Marshal(resp.Data)
	json.Unmarshal(dataJSON, &cwr)

	delReq := newWordGroupRequest("DELETE", "/word_group/"+cwr.GroupID, "",
		map[string]string{"group_id": cwr.GroupID})
	w2 := httptest.NewRecorder()
	DeleteWordGroup(w2, delReq)

	if w2.Code != http.StatusOK {
		t.Fatalf("delete status: got %d, want %d", w2.Code, http.StatusOK)
	}
	resp2 := decodeWordGroupResponse(t, w2)
	var delResp DeleteWordGroupResponse
	dataJSON2, _ := json.Marshal(resp2.Data)
	json.Unmarshal(dataJSON2, &delResp)
	if delResp.DeletedRows < 1 {
		t.Fatalf("deleted rows: got %d, want >= 1", delResp.DeletedRows)
	}
}

// TestDeleteWordGroup_NotFound returns 404 for unknown group.
func TestDeleteWordGroup_NotFound(t *testing.T) {
	setupWordGroupTest(t)
	req := newWordGroupRequest("DELETE", "/word_group/ghost", "",
		map[string]string{"group_id": "ghost"})
	w := httptest.NewRecorder()
	DeleteWordGroup(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusNotFound)
	}
}

// TestBatchDeleteWordGroups deletes multiple groups at once.
func TestBatchDeleteWordGroups(t *testing.T) {
	setupWordGroupTest(t)
	var gids []string
	for _, term := range []string{"batch-1", "batch-2"} {
		body := `{"term":"` + term + `"}`
		req := newWordGroupRequest("POST", "/word_group", body, nil)
		w := httptest.NewRecorder()
		CreateWordGroup(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("create %s: status %d", term, w.Code)
		}
		resp := decodeWordGroupResponse(t, w)
		var cwr CreateWordGroupResponse
		dataJSON, _ := json.Marshal(resp.Data)
		json.Unmarshal(dataJSON, &cwr)
		gids = append(gids, cwr.GroupID)
	}

	delBody, _ := json.Marshal(BatchDeleteWordGroupsRequest{GroupIDs: gids})
	req := newWordGroupRequest("POST", "/word_group:batchDelete", string(delBody), nil)
	w := httptest.NewRecorder()
	BatchDeleteWordGroups(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("batch delete status: got %d, want %d", w.Code, http.StatusOK)
	}
	resp := decodeWordGroupResponse(t, w)
	var br BatchDeleteWordGroupsResponse
	dataJSON, _ := json.Marshal(resp.Data)
	json.Unmarshal(dataJSON, &br)
	if br.DeletedRows < 2 {
		t.Fatalf("deleted rows: got %d, want >= 2", br.DeletedRows)
	}
}

// TestGetWordGroup_NotFound returns 404 for non-existent group.
func TestGetWordGroup_NotFound(t *testing.T) {
	setupWordGroupTest(t)
	req := newWordGroupRequest("GET", "/word_group/ghost", "",
		map[string]string{"group_id": "ghost"})
	w := httptest.NewRecorder()
	GetWordGroup(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusNotFound)
	}
}

// TestCheckWordsExist reports which words already exist for a user.
func TestCheckWordsExist(t *testing.T) {
	setupWordGroupTest(t)
	body := `{"term":"existing-word","aliases":["ext-alias"]}`
	req := newWordGroupRequest("POST", "/word_group", body, nil)
	w := httptest.NewRecorder()
	CreateWordGroup(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create status: got %d", w.Code)
	}

	checkBody := `{"term":"existing-word","aliases":["ext-alias","new-word"]}`
	req2 := newWordGroupRequest("POST", "/word_group:checkExists", checkBody, nil)
	w2 := httptest.NewRecorder()
	CheckWordsExist(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w2.Code, http.StatusOK)
	}
	resp := decodeWordGroupResponse(t, w2)
	var cer CheckWordsExistResponse
	dataJSON, _ := json.Marshal(resp.Data)
	json.Unmarshal(dataJSON, &cer)
	if len(cer.Existing) < 2 {
		t.Fatalf("existing words: got %d, want >= 2, got %v", len(cer.Existing), cer.Existing)
	}
}

// TestListWordGroups returns groups for the request user.
func TestListWordGroups(t *testing.T) {
	setupWordGroupTest(t)
	body := `{"term":"listed-word"}`
	req := newWordGroupRequest("POST", "/word_group", body, nil)
	w := httptest.NewRecorder()
	CreateWordGroup(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create status: got %d", w.Code)
	}

	req2 := newWordGroupRequest("GET", "/word_group?page_size=10", "", nil)
	w2 := httptest.NewRecorder()
	ListWordGroups(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("list status: got %d, want %d", w2.Code, http.StatusOK)
	}
	resp := decodeWordGroupResponse(t, w2)
	var lr ListWordGroupsResponse
	dataJSON, _ := json.Marshal(resp.Data)
	json.Unmarshal(dataJSON, &lr)
	if lr.TotalSize < 1 {
		t.Fatalf("total: got %d, want >= 1", lr.TotalSize)
	}
}

// TestSearchWordGroups finds groups by keyword substring.
func TestSearchWordGroups(t *testing.T) {
	setupWordGroupTest(t)
	body := `{"term":"pineapple"}`
	req := newWordGroupRequest("POST", "/word_group", body, nil)
	w := httptest.NewRecorder()
	CreateWordGroup(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create status: got %d", w.Code)
	}

	searchBody := `{"keyword":"pine","page_size":10}`
	req2 := newWordGroupRequest("POST", "/word_group:search", searchBody, nil)
	w2 := httptest.NewRecorder()
	SearchWordGroups(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("search status: got %d, want %d", w2.Code, http.StatusOK)
	}
	resp := decodeWordGroupResponse(t, w2)
	var lr ListWordGroupsResponse
	dataJSON, _ := json.Marshal(resp.Data)
	json.Unmarshal(dataJSON, &lr)
	if lr.TotalSize < 1 {
		t.Fatalf("total: got %d, want >= 1", lr.TotalSize)
	}
}

// TestWordGroupInvalidMethod returns 405 for wrong HTTP method.
func TestWordGroupInvalidMethod(t *testing.T) {
	setupWordGroupTest(t)
	req := newWordGroupRequest("PUT", "/word_group", `{"term":"x"}`, nil)
	w := httptest.NewRecorder()
	CreateWordGroup(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

// TestEscapeLikePatternIntegration confirms terms containing '%' work end-to-end.
func TestEscapeLikePatternIntegration(t *testing.T) {
	setupWordGroupTest(t)
	body := `{"term":"50% off sale"}`
	req := newWordGroupRequest("POST", "/word_group", body, nil)
	w := httptest.NewRecorder()
	CreateWordGroup(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create status: got %d", w.Code)
	}

	var cwr CreateWordGroupResponse
	resp := decodeWordGroupResponse(t, w)
	dataJSON, _ := json.Marshal(resp.Data)
	json.Unmarshal(dataJSON, &cwr)
	if cwr.Term != "50% off sale" {
		t.Fatalf("term: got %q", cwr.Term)
	}
}

// TestCreateWordGroup_Conflict_Success creates a group and resolves a conflict row.
func TestCreateWordGroup_Conflict_Success(t *testing.T) {
	setupWordGroupTest(t)

	db := store.DB()
	now := db.NowFunc()
	conflict := orm.WordGroupConflict{
		ID:           "conflict-1",
		Word:         "ambig-word",
		CreateUserID: "user-test-1",
		GroupIDs:     `["g1"]`,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := db.Create(&conflict).Error; err != nil {
		t.Fatalf("create conflict row: %v", err)
	}

	body := `{"term":"ambig-word","aliases":["sense-a"],"conflict":true,"id":"conflict-1"}`
	req := newWordGroupRequest("POST", "/word_group", body, nil)
	w := httptest.NewRecorder()
	CreateWordGroup(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

// TestCreateWordGroup_Conflict_NotFound returns 404 when conflict id does not exist.
func TestCreateWordGroup_Conflict_NotFound(t *testing.T) {
	setupWordGroupTest(t)
	body := `{"term":"no-conflict","conflict":true,"id":"missing-conflict"}`
	req := newWordGroupRequest("POST", "/word_group", body, nil)
	w := httptest.NewRecorder()
	CreateWordGroup(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusNotFound)
	}
}

// TestDeleteWordGroup_ForOtherUser cannot delete a group owned by a different user.
func TestDeleteWordGroup_ForOtherUser(t *testing.T) {
	setupWordGroupTest(t)
	body := `{"term":"mine"}`
	req := newWordGroupRequest("POST", "/word_group", body, nil)
	w := httptest.NewRecorder()
	CreateWordGroup(w, req)
	resp := decodeWordGroupResponse(t, w)
	var cwr CreateWordGroupResponse
	dataJSON, _ := json.Marshal(resp.Data)
	json.Unmarshal(dataJSON, &cwr)

	delReq := httptest.NewRequest("DELETE", "/word_group/"+cwr.GroupID, nil)
	delReq.Header.Set("X-User-Id", "user-other")
	delReq = mux.SetURLVars(delReq, map[string]string{"group_id": cwr.GroupID})
	w2 := httptest.NewRecorder()
	DeleteWordGroup(w2, delReq)

	if w2.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want %d", w2.Code, http.StatusNotFound)
	}
}

// TestUpdateWordGroup_Success updates term and aliases for an existing group.
func TestUpdateWordGroup_Success(t *testing.T) {
	setupWordGroupTest(t)
	body := `{"term":"original-term","aliases":["orig-alias"]}`
	req := newWordGroupRequest("POST", "/word_group", body, nil)
	w := httptest.NewRecorder()
	CreateWordGroup(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create status: got %d", w.Code)
	}
	resp := decodeWordGroupResponse(t, w)
	var cwr CreateWordGroupResponse
	dataJSON, _ := json.Marshal(resp.Data)
	json.Unmarshal(dataJSON, &cwr)

	updateBody, _ := json.Marshal(UpdateWordGroupRequest{
		GroupID:     cwr.GroupID,
		Term:        "updated-term",
		Aliases:     []string{"new-alias"},
		Description: "updated desc",
	})
	req2 := newWordGroupRequest("POST", "/word_group:update", string(updateBody), nil)
	w2 := httptest.NewRecorder()
	UpdateWordGroup(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("update status: got %d, want %d, body: %s", w2.Code, http.StatusOK, w2.Body.String())
	}
	resp2 := decodeWordGroupResponse(t, w2)
	var ucr CreateWordGroupResponse
	dataJSON2, _ := json.Marshal(resp2.Data)
	json.Unmarshal(dataJSON2, &ucr)
	if ucr.Term != "updated-term" {
		t.Fatalf("term: got %q, want updated-term", ucr.Term)
	}
}

// TestUpdateWordGroup_NotFound returns 404 when group does not exist.
func TestUpdateWordGroup_NotFound(t *testing.T) {
	setupWordGroupTest(t)
	body, _ := json.Marshal(UpdateWordGroupRequest{
		GroupID: "no-such-group",
		Term:    "ghost",
	})
	req := newWordGroupRequest("POST", "/word_group:update", string(body), nil)
	w := httptest.NewRecorder()
	UpdateWordGroup(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusNotFound)
	}
}

// TestBatchDeleteWordGroups_Empty rejects empty group_ids.
func TestBatchDeleteWordGroups_Empty(t *testing.T) {
	setupWordGroupTest(t)
	req := newWordGroupRequest("POST", "/word_group:batchDelete",
		`{"group_ids":[]}`, nil)
	w := httptest.NewRecorder()
	BatchDeleteWordGroups(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestListWordGroups_Empty returns empty list when no groups exist.
func TestListWordGroups_Empty(t *testing.T) {
	setupWordGroupTest(t)
	req := newWordGroupRequest("GET", "/word_group", "", nil)
	w := httptest.NewRecorder()
	ListWordGroups(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d", w.Code)
	}
	resp := decodeWordGroupResponse(t, w)
	var lr ListWordGroupsResponse
	dataJSON, _ := json.Marshal(resp.Data)
	json.Unmarshal(dataJSON, &lr)
	if lr.TotalSize != 0 {
		t.Fatalf("total: got %d, want 0", lr.TotalSize)
	}
}

// TestCheckWordsExist_EmptyTerms returns 400 when no words provided.
func TestCheckWordsExist_EmptyTerms(t *testing.T) {
	setupWordGroupTest(t)
	req := newWordGroupRequest("POST", "/word_group:checkExists", `{}`, nil)
	w := httptest.NewRecorder()
	CheckWordsExist(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}
