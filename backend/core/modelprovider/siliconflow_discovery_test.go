package modelprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type siliconFlowTestTransport struct {
	target    *url.URL
	transport *http.Transport
}

func (transport siliconFlowTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	local := request.Clone(request.Context())
	local.URL.Scheme, local.URL.Host = transport.target.Scheme, transport.target.Host
	return transport.transport.RoundTrip(local)
}

// Keep HTTP tests independent from the package's mutable SSRF-test switch.
// The configured domain remains public-looking, while this isolated transport
// sends every request to this test's server and cannot reach the Internet.
func siliconFlowTestBaseURL(t *testing.T, upstream *httptest.Server) string {
	t.Helper()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	previous := remoteModelsHTTPClient
	remoteModelsHTTPClient = &http.Client{Transport: siliconFlowTestTransport{target: target, transport: transport}}
	t.Cleanup(func() { remoteModelsHTTPClient = previous; transport.CloseIdleConnections() })
	return "http://sf-models.example.test:8181"
}

func setupSiliconFlowDiscovery(t *testing.T, baseURL string) (*gorm.DB, orm.UserModelProvider, orm.UserModelProviderGroup) {
	t.Helper()
	t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", "siliconflow-discovery-test")
	db := orm.MigrateTestDB(t, &orm.DefaultModelProvider{}, &orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}).DB
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now()
	base := orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}
	parent := orm.UserModelProvider{ID: "sf-supplier", Name: "SiliconFlow", Category: defaultProviderCategory, ProtocolName: "OpenAI", Capabilities: "custom_base_url,has_models", BaseURL: baseURL, BaseModel: base}
	group := orm.UserModelProviderGroup{ID: "sf-group", UserModelProviderID: parent.ID, Name: "SiliconFlow", BaseURL: baseURL, APIKey: "saved-test-key", IsVerified: true, BaseModel: base}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	return db, parent, group
}

func requestSiliconFlowDiscovery(t *testing.T, parent orm.UserModelProvider, group orm.UserModelProviderGroup, recognition *upstreamModelRecognitionIndex) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/remote_models", nil)
	req.Header.Set("X-User-Id", parent.CreateUserID)
	req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
	recorder := httptest.NewRecorder()
	listRemoteGroupModels(recorder, req, recognition)
	return recorder
}

func TestSiliconFlowDiscoveryUsesOfficialCategoriesWithoutLosingModelEvidence(t *testing.T) {
	var mu sync.Mutex
	queries := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/gateway/v1/models" || r.Header.Get("Authorization") != "Bearer saved-test-key" {
			t.Errorf("request lost saved endpoint or credential")
		}
		subtype := r.URL.Query().Get("sub_type")
		mu.Lock()
		queries = append(queries, subtype)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch subtype {
		case "":
			w.Write([]byte(`{"data":[{"id":"opaque-chat","input_modalities":["text","image"],"output_modalities":["text"]},{"id":"opaque-vector","input_modalities":["text"]},{"id":"opaque-mm-vector","input_modalities":["image","text"]},{"id":"coarse-vector"},{"id":"catalog-vector"},{"id":"conflicting"},{"id":"retained-speech"},{"id":"manual-model"}]}`))
		case "chat":
			w.Write([]byte(`{"data":[{"id":"opaque-chat","architecture":{"input_modalities":["audio"]}},{"id":"conflicting"},{"id":"manual-model"}]}`))
		case "embedding":
			w.Write([]byte(`{"data":[{"id":"opaque-vector"},{"id":"opaque-mm-vector"},{"id":"coarse-vector"},{"id":"catalog-vector"}]}`))
		case "reranker":
			w.Write([]byte(`{"data":[{"id":"filtered-ranker"},{"id":"conflicting"}]}`))
		case "text-to-image":
			w.Write([]byte(`{"data":[{"id":"opaque-image"},{"id":"two-image-tasks"}]}`))
		case "image-to-image":
			w.Write([]byte(`{"data":[{"id":"opaque-edit"},{"id":"two-image-tasks"}]}`))
		case "speech-to-text":
			w.Write([]byte(`{"data":[{"id":"opaque-stt"}]}`))
		case "text-to-video":
			w.Write([]byte(`{"data":[{"id":"opaque-video"}]}`))
		default:
			t.Errorf("undocumented SiliconFlow sub_type %q", subtype)
		}
	}))
	defer upstream.Close()
	baseURL := siliconFlowTestBaseURL(t, upstream)
	db, parent, group := setupSiliconFlowDiscovery(t, baseURL+"/gateway/v1/")
	manual := orm.UserModelProviderGroupModel{ID: "saved-manual", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, Name: "manual-model", ModelType: "rerank", BaseModel: parent.BaseModel}
	if err := db.Create(&manual).Error; err != nil {
		t.Fatal(err)
	}
	index := newUpstreamModelRecognitionIndex(upstreamCatalogFile{Providers: map[string][]upstreamCatalogModel{"SiliconFlow": {
		{Name: "coarse-vector", ModelType: "llm"}, {Name: "catalog-vector", ModelType: "multimodal_embedding"}, {Name: "retained-speech", ModelType: "tts"},
	}}})
	recorder := requestSiliconFlowDiscovery(t, parent, group, index)
	if recorder.Code != http.StatusOK {
		t.Fatalf("discovery: %d %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data struct {
			Models []struct {
				Name      string   `json:"name"`
				ModelType string   `json:"model_type"`
				Input     []string `json:"input"`
				Output    []string `json:"output"`
				SubTypes  []string `json:"sub_types"`
				Added     bool     `json:"added"`
			} `json:"models"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{"opaque-chat": "llm", "opaque-vector": "embed", "opaque-mm-vector": "multimodal_embedding", "coarse-vector": "", "catalog-vector": "multimodal_embedding", "conflicting": "", "retained-speech": "tts", "manual-model": "llm", "filtered-ranker": "rerank", "opaque-image": "text2image", "opaque-edit": "image_editing", "two-image-tasks": "", "opaque-stt": "stt", "opaque-video": "text2video"}
	if len(payload.Data.Models) != len(expected) {
		t.Fatalf("expected all baseline and filtered models, got %d want %d", len(payload.Data.Models), len(expected))
	}
	for _, item := range payload.Data.Models {
		if want, exists := expected[item.Name]; !exists || item.ModelType != want {
			t.Errorf("%s type=%q want=%q", item.Name, item.ModelType, want)
		}
		if item.Name == "conflicting" && !reflect.DeepEqual(item.SubTypes, []string{"chat", "reranker"}) {
			t.Errorf("lost conflicting category evidence: %#v", item)
		}
		if item.Name == "coarse-vector" && !reflect.DeepEqual(item.SubTypes, []string{"embedding"}) {
			t.Errorf("lost coarse embedding evidence: %#v", item)
		}
		if item.Name == "opaque-chat" && (!reflect.DeepEqual(item.Input, []string{"text", "image", "audio"}) || !reflect.DeepEqual(item.Output, []string{"text"})) {
			t.Errorf("filter overwrote baseline raw modalities: %#v", item)
		}
		if item.Name == "manual-model" && !item.Added {
			t.Error("saved model not marked added")
		}
	}
	var reloaded orm.UserModelProviderGroupModel
	if err := db.Take(&reloaded, "id = ?", manual.ID).Error; err != nil || reloaded.ModelType != "rerank" {
		t.Fatalf("discovery overwrote manually saved type: %#v %v", reloaded, err)
	}
	mu.Lock()
	sort.Strings(queries)
	gotQueries := append([]string(nil), queries...)
	mu.Unlock()
	wantQueries := []string{"", "chat", "embedding", "image-to-image", "reranker", "speech-to-text", "text-to-image", "text-to-video"}
	if !reflect.DeepEqual(gotQueries, wantQueries) {
		t.Fatalf("queries=%v want=%v", gotQueries, wantQueries)
	}
}

func TestSiliconFlowSupplierIdentitySurvivesCompatibleProtocolButHonorsOverrides(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, template string
		custom                   bool
		want                     bool
	}{
		{name: "builtin compatible protocol", protocol: "OpenAI", want: true},
		{name: "builtin anthropic override", protocol: "Claude", want: false},
		{name: "native custom protocol", protocol: "SiliconFlow", custom: true, want: true},
		{name: "custom inherited template", protocol: "OpenAI", template: "SiliconFlow", custom: true, want: true},
		{name: "custom template with override", protocol: "Claude", template: "SiliconFlow", custom: true, want: false},
		{name: "custom display name is not supplier identity", protocol: "OpenAI", custom: true, want: false},
		{name: "unrelated template", protocol: "OpenAI", template: "OpenAI", custom: true, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, parent, _ := setupSiliconFlowDiscovery(t, "https://api.siliconflow.cn/v1/")
			parent.ProtocolName, parent.IsCustom = tc.protocol, tc.custom
			if tc.template != "" {
				parent.DefaultModelProviderID = "template"
				if err := db.Create(&orm.DefaultModelProvider{ID: parent.DefaultModelProviderID, Name: tc.template}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if got := isSiliconFlowSupplier(t.Context(), db, parent); got != tc.want {
				t.Fatalf("identity=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestSiliconFlowDiscoveryIgnoredFiltersPreserveConflictingEvidence(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"gpt-4o"}]}`))
	}))
	defer upstream.Close()
	baseURL := siliconFlowTestBaseURL(t, upstream)
	_, parent, group := setupSiliconFlowDiscovery(t, baseURL+"/v1/")
	index := newUpstreamModelRecognitionIndex(upstreamCatalogFile{RecognitionModels: []upstreamCatalogModel{{Name: "gpt-4o", ModelType: "llm"}}})
	recorder := requestSiliconFlowDiscovery(t, parent, group, index)
	var payload struct {
		Data struct {
			Models []struct {
				ModelType string   `json:"model_type"`
				SubTypes  []string `json:"sub_types"`
			} `json:"models"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || len(payload.Data.Models) != 1 || payload.Data.Models[0].ModelType != "" || !reflect.DeepEqual(payload.Data.Models[0].SubTypes, siliconFlowModelSubTypes[:]) {
		t.Fatalf("ignored filters became arbitrary primary type: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestSiliconFlowDiscoveryCategoryRequestsShareDeadlineAndBoundConcurrency(t *testing.T) {
	var active, peak, calls atomic.Int32
	started := make(chan struct{}, 3)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); current > old && !peak.CompareAndSwap(old, current); old = peak.Load() {
		}
		calls.Add(1)
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer upstream.Close()
	baseURL := siliconFlowTestBaseURL(t, upstream)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan []openaiModelListEntry, 1)
	go func() {
		models, err := listSiliconFlowModelCategories(req, []openaiModelListEntry{{ID: "baseline-retained"}})
		if err != nil {
			t.Errorf("optional cancellation should keep baseline: %v", err)
		}
		done <- models
	}()
	for range 3 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("three bounded workers did not start")
		}
	}
	cancel()
	select {
	case models := <-done:
		if len(models) != 1 || models[0].ID != "baseline-retained" || peak.Load() != 3 || calls.Load() != 3 {
			t.Fatalf("cancellation lost baseline or exceeded concurrency: models=%v peak=%d calls=%d", models, peak.Load(), calls.Load())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("category workers did not cancel with their shared request context")
	}
}

func TestSiliconFlowDiscoveryOptionalCategoryFailureKeepsBaseline(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Query().Get("sub_type") {
				case "":
					w.Write([]byte(`{"data":[{"id":"kept"}]}`))
				case "chat":
					w.Write([]byte(`{"data":[{"id":"chat-only"}]}`))
				default:
					w.WriteHeader(status)
					w.Write([]byte(`{"error":{"message":"secret-provider-output"}}`))
				}
			}))
			defer upstream.Close()
			baseURL := siliconFlowTestBaseURL(t, upstream)
			_, parent, group := setupSiliconFlowDiscovery(t, baseURL+"/v1/")
			recorder := requestSiliconFlowDiscovery(t, parent, group, nil)
			if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"name":"kept"`) || !strings.Contains(recorder.Body.String(), `"name":"chat-only"`) || strings.Contains(recorder.Body.String(), "secret-provider-output") {
				t.Fatalf("optional failure lost catalog or leaked body: %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestSiliconFlowDiscoveryDoesNotHideCategoryAuthenticationFailure(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("sub_type") == "" {
					w.Write([]byte(`{"data":[{"id":"kept"}]}`))
					return
				}
				w.WriteHeader(status)
				w.Write([]byte(`{"error":{"message":"secret-provider-output"}}`))
			}))
			defer upstream.Close()
			baseURL := siliconFlowTestBaseURL(t, upstream)
			_, parent, group := setupSiliconFlowDiscovery(t, baseURL+"/v1/")
			recorder := requestSiliconFlowDiscovery(t, parent, group, nil)
			if recorder.Code == http.StatusOK || strings.Contains(recorder.Body.String(), "secret-provider-output") {
				t.Fatalf("authentication failure hidden or body leaked: %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestRemoteModelDiscoveryRejectsErrorObjectsAndInvalidCollectionShapes(t *testing.T) {
	for _, tc := range []struct {
		name, body, reason string
	}{
		{"error data string", `{"code":50505,"message":"provider-secret","data":"error"}`, "upstream_error"},
		{"nonzero code with array", `{"code":50505,"data":[]}`, "upstream_error"},
		{"invalid code object", `{"code":{"unexpected":true},"data":[]}`, "upstream_error"},
		{"invalid code array", `{"code":[1],"data":[]}`, "upstream_error"},
		{"data object", `{"data":{"id":"looks-like-a-model"}}`, "invalid_response"},
		{"data string", `{"data":"provider-secret"}`, "invalid_response"},
		{"data null", `{"data":null}`, "invalid_response"},
		{"models object", `{"models":{"id":"looks-like-a-model"}}`, "invalid_response"},
		{"error object", `{"error":{"message":"provider-secret"},"data":[]}`, "upstream_error"},
		{"numeric root array", `[123]`, "invalid_response"},
		{"invalid json", `{"data":[`, "invalid_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(tc.body)) }))
			defer upstream.Close()
			baseURL := siliconFlowTestBaseURL(t, upstream)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+"/v1/models", nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = fetchRemoteModelEntries(req)
			failure, ok := err.(interface{ FailureCode() string })
			if err == nil || !ok || failure.FailureCode() != tc.reason || strings.Contains(err.Error(), "provider-secret") {
				t.Fatalf("error body became a successful empty catalog or leaked: %v", err)
			}
		})
	}
}

func TestRemoteModelDiscoveryAcceptsKnownEmptyAndLegacyCollectionShapes(t *testing.T) {
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"data":[]}`, 0}, {`{"models":[]}`, 0}, {`[]`, 0}, {`["model-one","model-two"]`, 2},
		{`{"code":0,"data":[{"id":"model-one"}]}`, 1},
	} {
		t.Run(tc.body, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(tc.body)) }))
			defer upstream.Close()
			baseURL := siliconFlowTestBaseURL(t, upstream)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+"/v1/models", nil)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := fetchRemoteModelEntries(req)
			if err != nil || len(entries) != tc.want {
				t.Fatalf("valid model collection rejected: entries=%v err=%v", entries, err)
			}
		})
	}
}

func TestSiliconFlowDiscoveryChatMetadataConflictRemainsUnclassified(t *testing.T) {
	entry := openaiModelListEntry{ID: "conflicting-chat", OutputModalities: []string{"image"}, DiscoverySubTypes: []string{"chat"}}
	item := remoteModelItem(entry, "SiliconFlow", nil)
	if item.ModelType != "" || !reflect.DeepEqual(item.SubTypes, []string{"chat"}) || !reflect.DeepEqual(item.Output, []string{"image"}) {
		t.Fatalf("conflicting official chat and raw output became an arbitrary task: %#v", item)
	}
}
