package resourcefs

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"lazymind/core/store"
)

func TestGetUserPreferenceFileSplitsFrontmatter(t *testing.T) {
	db := newResourceFSTestDB(t)
	store.Init(db.DB, nil, nil)
	service := NewService(ServiceDeps{DB: db.DB})
	ref := ResourceRef{UserID: "u1", ResourceType: ResourceTypeUserPreference}
	fullContent := "---\nagent_persona: \"测试助手\"\npreferred_name: \"小明\"\nresponse_style: \"简洁\"\ncustom_key: \"keep\"\n---\n\n正文内容\n"
	if _, err := service.EnsureResource(context.Background(), ref, fullContent); err != nil {
		t.Fatalf("EnsureResource returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/personal-resource/user_preference:file", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"resource_type": "user_preference"})
	rec := httptest.NewRecorder()

	GetFile(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	data := decodeOKData(t, rec.Body.Bytes())
	if got := data["content"]; got != "正文内容\n" {
		t.Fatalf("content = %#v", got)
	}
	if got := data["agent_persona"]; got != "测试助手" {
		t.Fatalf("agent_persona = %#v", got)
	}
	if got := data["preferred_name"]; got != "小明" {
		t.Fatalf("preferred_name = %#v", got)
	}
	if got := data["response_style"]; got != "简洁" {
		t.Fatalf("response_style = %#v", got)
	}
}

func TestWriteUserPreferenceDraftStoresFullFrontmatter(t *testing.T) {
	db := newResourceFSTestDB(t)
	store.Init(db.DB, nil, nil)
	service := NewService(ServiceDeps{DB: db.DB})
	ref := ResourceRef{UserID: "u1", ResourceType: ResourceTypeUserPreference}
	fullContent := "---\nagent_persona: \"旧助手\"\npreferred_name: \"旧称呼\"\nresponse_style: \"旧风格\"\ncustom_key: \"keep\"\n---\n\n旧正文\n"
	state, err := service.EnsureResource(context.Background(), ref, fullContent)
	if err != nil {
		t.Fatalf("EnsureResource returned error: %v", err)
	}
	body := []byte(`{
		"content": "新正文\n",
		"agent_persona": "新助手",
		"preferred_name": "新称呼",
		"response_style": "新风格",
		"expected_draft_version": 1,
		"task_id": "frontmatter-test"
	}`)
	req := httptest.NewRequest(http.MethodPut, "/personal-resource/user_preference:file", bytes.NewReader(body))
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"resource_type": "user_preference"})
	rec := httptest.NewRecorder()

	WriteDraft(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	draft, err := service.ReadFile(context.Background(), ReadFileRequest{Ref: ref, RefType: FileRefDraft})
	if err != nil {
		t.Fatalf("ReadFile draft returned error: %v", err)
	}
	if !strings.Contains(draft.Content, `agent_persona: "新助手"`) ||
		!strings.Contains(draft.Content, `preferred_name: "新称呼"`) ||
		!strings.Contains(draft.Content, `response_style: "新风格"`) ||
		!strings.Contains(draft.Content, `custom_key: "keep"`) {
		t.Fatalf("draft content did not preserve full frontmatter: %q", draft.Content)
	}
	if !strings.HasSuffix(draft.Content, "\n\n新正文\n") {
		t.Fatalf("draft content body = %q", draft.Content)
	}
	if draft.DraftVersion != state.DraftVersion+1 {
		t.Fatalf("draft version = %d, want %d", draft.DraftVersion, state.DraftVersion+1)
	}
}

func decodeOKData(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var resp struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, string(body))
	}
	if resp.Code != 0 {
		t.Fatalf("response code = %d, body=%s", resp.Code, string(body))
	}
	return resp.Data
}
