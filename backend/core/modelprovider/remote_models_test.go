package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestInferRemoteModelTypeUnknownIsEmpty(t *testing.T) {
	if err := LoadContextWindows("../config/model_context_windows.yaml"); err != nil {
		t.Fatal(err)
	}
	if got := inferRemoteModelType("definitely-not-a-catalog-model"); got != "" {
		t.Fatalf("unknown remote type = %q, want empty", got)
	}
	if got := inferRemoteModelType("qwen-plus"); got != "llm" {
		t.Fatalf("qwen-plus type = %q, want llm", got)
	}
}

func TestModelsListURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want string
	}{
		{"https://api.deepseek.com/", "https://api.deepseek.com/v1/models"},
		{"https://api.deepseek.com", "https://api.deepseek.com/v1/models"},
		{"https://api.deepseek.com/v1/", "https://api.deepseek.com/v1/models"},
		{"https://dashscope.aliyuncs.com/compatible-mode/v1/", "https://dashscope.aliyuncs.com/compatible-mode/v1/models"},
		{"https://api.openai.com/v1/chat/completions", "https://api.openai.com/v1/models"},
	}
	for _, tt := range tests {
		got, err := modelsListURL(tt.in)
		if err != nil {
			t.Fatalf("modelsListURL(%q) error: %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("modelsListURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}

	blocked := []string{
		"file:///etc/passwd",
		"http://127.0.0.1/v1",
		"http://10.0.0.8/v1",
		"http://169.254.169.254/latest/meta-data",
		"https://user:pass@api.openai.com/v1",
	}
	for _, in := range blocked {
		if _, err := modelsListURL(in); err == nil {
			t.Fatalf("modelsListURL(%q) succeeded, want error", in)
		}
	}
}

func TestListRemoteGroupModels(t *testing.T) {
	remoteModelsAllowPrivateHosts = true
	t.Cleanup(func() { remoteModelsAllowPrivateHosts = false })

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q, want Bearer secret", got)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-chat"},{"id":"deepseek-reasoner"},{"id":"qwen-plus"}]}`))
	}))
	t.Cleanup(upstream.Close)

	db := setupListProviderTestDB(t)
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	provider := orm.UserModelProvider{
		ID:           "provider-ds",
		Name:         "DeepSeek",
		Category:     "model",
		Capabilities: "has_models",
		BaseModel:    orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now},
	}
	group := orm.UserModelProviderGroup{
		ID:                  "group-ds",
		UserModelProviderID: provider.ID,
		Name:                "DeepSeek",
		BaseURL:             upstream.URL + "/",
		APIKey:              "secret",
		IsVerified:          true,
		BaseModel:           orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now},
	}
	existing := orm.UserModelProviderGroupModel{
		ID:                       "model-plus",
		UserModelProviderID:      provider.ID,
		UserModelProviderGroupID: group.ID,
		Name:                     "qwen-plus",
		ModelType:                "llm",
		BaseModel:                orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/model_providers/provider-ds/groups/group-ds/remote_models", nil)
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{
		"model_provider_id": "provider-ds",
		"group_id":          "group-ds",
	})
	rec := httptest.NewRecorder()
	ListRemoteGroupModels(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Data remoteGroupModelsResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data.Models) != 3 {
		t.Fatalf("models = %#v, want 3", payload.Data.Models)
	}
	byName := map[string]remoteGroupModelItem{}
	for _, item := range payload.Data.Models {
		byName[item.Name] = item
	}
	if !byName["qwen-plus"].Added {
		t.Fatal("expected qwen-plus to be marked added")
	}
	if byName["deepseek-chat"].Added {
		t.Fatal("expected deepseek-chat not to be added")
	}
	if byName["deepseek-chat"].ModelType != "" {
		t.Fatalf("unknown remote model type = %q, want empty", byName["deepseek-chat"].ModelType)
	}
	if byName["qwen-plus"].ModelType != "llm" {
		t.Fatalf("qwen-plus type = %q, want llm", byName["qwen-plus"].ModelType)
	}
}
