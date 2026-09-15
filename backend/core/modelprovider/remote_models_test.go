package modelprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		{"https://gateway.example.com/openai", "https://gateway.example.com/openai/v1/models"},
		{"https://gateway.example.com/openai/", "https://gateway.example.com/openai/v1/models"},
		{"https://gateway.example.com/compatible", "https://gateway.example.com/compatible/v1/models"},
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
		"http://169.254.169.254/latest/meta-data",
		"https://user:pass@api.openai.com/v1",
	}
	for _, in := range blocked {
		if _, err := modelsListURL(in); err == nil {
			t.Fatalf("modelsListURL(%q) succeeded, want error", in)
		}
	}

	allowedPrivate := []struct {
		in   string
		want string
	}{
		{"http://10.0.0.8/v1", "http://10.0.0.8/v1/models"},
		{"http://172.16.1.4/openai", "http://172.16.1.4/openai/v1/models"},
		{"http://192.168.1.10/", "http://192.168.1.10/v1/models"},
	}
	for _, tt := range allowedPrivate {
		got, err := modelsListURL(tt.in)
		if err != nil {
			t.Fatalf("modelsListURL(%q) error: %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("modelsListURL(%q) = %q, want %q", tt.in, got, tt.want)
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

func TestListRemoteGroupModelsKeepsPathPrefix(t *testing.T) {
	remoteModelsAllowPrivateHosts = true
	t.Cleanup(func() { remoteModelsAllowPrivateHosts = false })

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openai/v1/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-test"}]}`))
	}))
	t.Cleanup(upstream.Close)

	rec := listRemoteGroupModelsForURL(t, upstream.URL+"/openai")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Data remoteGroupModelsResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.URL != upstream.URL+"/openai/v1/models" {
		t.Fatalf("url = %q", payload.Data.URL)
	}
	if len(payload.Data.Models) != 1 || payload.Data.Models[0].Name != "gpt-test" {
		t.Fatalf("models = %#v", payload.Data.Models)
	}
}

func TestDialRemoteModelsPinsResolvedIP(t *testing.T) {
	var lookups int
	var dialed string
	remoteModelsLookupIP = func(ctx context.Context, host string) ([]net.IP, error) {
		lookups++
		if host != "rebinder.example" {
			t.Fatalf("lookup host = %q", host)
		}
		return []net.IP{net.ParseIP("203.0.113.10")}, nil
	}
	remoteModelsDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dialed = address
		return nil, errors.New("stop after pin")
	}
	t.Cleanup(func() {
		remoteModelsLookupIP = lookupRemoteModelsIPs
		remoteModelsDialContext = defaultRemoteModelsDial
	})

	req, err := http.NewRequest(http.MethodGet, "https://rebinder.example/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = remoteModelsHTTPClient.Do(req)
	if err == nil {
		t.Fatal("expected dial error")
	}
	if lookups != 1 {
		t.Fatalf("lookups = %d, want 1", lookups)
	}
	if dialed != net.JoinHostPort("203.0.113.10", "443") {
		t.Fatalf("dialed = %q, want pinned IP", dialed)
	}
}

func TestDialRemoteModelsAllowsRFC1918(t *testing.T) {
	var dialed string
	remoteModelsLookupIP = func(ctx context.Context, host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("10.1.2.3")}, nil
	}
	remoteModelsDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dialed = address
		return nil, errors.New("stop after pin")
	}
	t.Cleanup(func() {
		remoteModelsLookupIP = lookupRemoteModelsIPs
		remoteModelsDialContext = defaultRemoteModelsDial
	})

	req, err := http.NewRequest(http.MethodGet, "https://intranet.example/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = remoteModelsHTTPClient.Do(req)
	if err == nil {
		t.Fatal("expected dial error")
	}
	if dialed != net.JoinHostPort("10.1.2.3", "443") {
		t.Fatalf("dialed = %q, want RFC1918 IP", dialed)
	}
}

func TestListRemoteGroupModelsKeepsOriginalHostWhenPinningIP(t *testing.T) {
	remoteModelsAllowPrivateHosts = true
	t.Cleanup(func() { remoteModelsAllowPrivateHosts = false })

	var sawHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHost = r.Host
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"pinned-model"}]}`))
	}))
	t.Cleanup(upstream.Close)

	parsed, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	port := parsed.Port()
	remoteModelsLookupIP = func(ctx context.Context, host string) ([]net.IP, error) {
		if host != "models.test" {
			t.Fatalf("lookup host = %q", host)
		}
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	t.Cleanup(func() { remoteModelsLookupIP = lookupRemoteModelsIPs })

	rec := listRemoteGroupModelsForURL(t, "http://models.test:"+port+"/")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	wantHost := net.JoinHostPort("models.test", port)
	if sawHost != wantHost {
		t.Fatalf("Host = %q, want original hostname %q", sawHost, wantHost)
	}
}

func TestListRemoteGroupModelsRejectsDNSRebindingToPrivateIP(t *testing.T) {
	var lookups int
	var dialed string
	remoteModelsLookupIP = func(ctx context.Context, host string) ([]net.IP, error) {
		lookups++
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	remoteModelsDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dialed = address
		return nil, errors.New("should not dial")
	}
	t.Cleanup(func() {
		remoteModelsLookupIP = lookupRemoteModelsIPs
		remoteModelsDialContext = defaultRemoteModelsDial
	})

	rec := listRemoteGroupModelsForURL(t, "https://rebinder.example/")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if lookups != 1 {
		t.Fatalf("lookups = %d, want 1 at dial time", lookups)
	}
	if dialed != "" {
		t.Fatalf("dialed private address %q", dialed)
	}
}

func listRemoteGroupModelsForURL(t *testing.T, baseURL string) *httptest.ResponseRecorder {
	t.Helper()
	db := setupListProviderTestDB(t)
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	provider := orm.UserModelProvider{
		ID:           "provider-rm",
		Name:         "Remote",
		Category:     "model",
		Capabilities: "has_models",
		BaseModel:    orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now},
	}
	group := orm.UserModelProviderGroup{
		ID:                  "group-rm",
		UserModelProviderID: provider.ID,
		Name:                "Remote",
		BaseURL:             baseURL,
		APIKey:              "secret",
		IsVerified:          true,
		BaseModel:           orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/model_providers/provider-rm/groups/group-rm/remote_models", nil)
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{
		"model_provider_id": "provider-rm",
		"group_id":          "group-rm",
	})
	rec := httptest.NewRecorder()
	ListRemoteGroupModels(rec, req)
	return rec
}
