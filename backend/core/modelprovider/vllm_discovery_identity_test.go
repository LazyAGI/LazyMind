package modelprovider

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestVLLMDiscoveryUsesLoadedIdentityWithoutChangingServedAliasOrLength(t *testing.T) {
	index := newUpstreamModelRecognitionIndex(upstreamCatalogFile{RecognitionModels: []upstreamCatalogModel{
		{Name: "qwen/qwen3.8-27b", ModelType: "llm", Input: []string{"text", "image", "video"}, Output: []string{"text"}, Context: 1000000},
	}})
	entries := parseRemoteModelEntries([]byte(`{"data":[{"id":"qwen38-27b","root":"/srv/models/Qwen3.8-27B","parent":null,"max_model_len":262144,"context_length":1048576}]}`))
	got := remoteModelItem(entries[0], "VLLM", index)
	if got.ID != "qwen38-27b" || got.Name != "qwen38-27b" || got.ModelType != "llm" {
		t.Fatalf("loaded model identity did not classify the served alias: %+v", got)
	}
	if !reflect.DeepEqual(got.Input, []string{"image", "text", "video"}) || !reflect.DeepEqual(got.Output, []string{"text"}) {
		t.Fatalf("missing catalog modalities: %+v", got)
	}
	if got.MaxInputTokens == nil || *got.MaxInputTokens != "262144" {
		t.Fatalf("served length was replaced by catalog/configured context: %+v", got)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "/srv/models/") || strings.Contains(string(raw), `"root"`) {
		t.Fatalf("loaded filesystem path escaped in public response: %s", raw)
	}
}

func TestVLLMDiscoveryRootDoesNotOverrideExplicitOrUnknownEvidence(t *testing.T) {
	index := newUpstreamModelRecognitionIndex(upstreamCatalogFile{RecognitionModels: []upstreamCatalogModel{
		{Name: "qwen/qwen3.8-27b", ModelType: "llm", Input: []string{"text", "image"}, Output: []string{"text"}},
		{Name: "known-unknown"},
		{Name: "conflicting-alias", ModelType: "llm"},
		{Name: "conflicting-alias", ModelType: "embed"},
		{Name: "vendor/root-conflict", ModelType: "embed"},
		{Name: "other/root-conflict", ModelType: "llm"},
	}})
	for _, tc := range []struct{ name, provider, raw, want string }{
		{"explicit task", "VLLM", `{"id":"alias","root":"Qwen/Qwen3.8-27B","task":"tts"}`, "tts"},
		{"explicit unknown task", "VLLM", `{"id":"alias","root":"Qwen/Qwen3.8-27B","task":"future-task"}`, "future-task"},
		{"explicit unknown output", "VLLM", `{"id":"alias","root":"Qwen/Qwen3.8-27B","output_modalities":["decisions"]}`, ""},
		{"explicit embedding output", "VLLM", `{"id":"alias","root":"Qwen/Qwen3.8-27B","output_modalities":["embeddings"]}`, "embed"},
		{"unknown served ID blocks root", "VLLM", `{"id":"known-unknown","root":"Qwen/Qwen3.8-27B"}`, ""},
		{"conflicting served ID blocks root", "VLLM", `{"id":"conflicting-alias","root":"Qwen/Qwen3.8-27B"}`, ""},
		{"conflicting root", "VLLM", `{"id":"alias","root":"/models/root-conflict"}`, ""},
		{"other supplier root ignored", "OpenAI", `{"id":"alias","root":"Qwen/Qwen3.8-27B"}`, ""},
		{"root query rejected", "VLLM", `{"id":"alias","root":"/models/Qwen3.8-27B?download=true"}`, ""},
		{"root fragment rejected", "VLLM", `{"id":"alias","root":"/models/Qwen3.8-27B#revision"}`, ""},
		{"root URL rejected", "VLLM", `{"id":"alias","root":"https://example.test/Qwen3.8-27B"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var entry openaiModelListEntry
			if err := json.Unmarshal([]byte(tc.raw), &entry); err != nil {
				t.Fatal(err)
			}
			got := remoteModelItem(entry, tc.provider, index)
			if got.ModelType != tc.want {
				t.Fatalf("type=%q, want %q: %+v", got.ModelType, tc.want, got)
			}
			if len(got.Input) != 0 {
				t.Fatalf("root fabricated modalities over stronger evidence: %+v", got)
			}
		})
	}
	entries := parseRemoteModelEntries([]byte(`{"data":[{"id":"alias","root":"Qwen/Qwen3.8-27B","input_modalities":["text"]}]}`))
	got := remoteModelItem(entries[0], "VLLM", index)
	if got.ModelType != "llm" || !reflect.DeepEqual(got.Input, []string{"text"}) || !reflect.DeepEqual(got.Output, []string{"text"}) {
		t.Fatalf("explicit input was widened or missing output not completed: %+v", got)
	}
}

func TestVLLMDiscoveryCatalogModalitiesAreDeterministicAndTypeBound(t *testing.T) {
	models := []upstreamCatalogModel{
		{Name: "one/loaded-model", ModelType: "llm", Input: []string{"text", "image"}, Output: []string{"text"}},
		{Name: "two/loaded-model", ModelType: "llm", Input: []string{"video", "text"}, Output: []string{"audio", "text"}},
		{Name: "three/loaded-model", Input: []string{"unknown"}, Output: []string{"decisions"}},
	}
	for _, rows := range [][]upstreamCatalogModel{models, {models[2], models[1], models[0]}} {
		index := newUpstreamModelRecognitionIndex(upstreamCatalogFile{RecognitionModels: rows})
		entries := parseRemoteModelEntries([]byte(`{"data":[{"id":"alias","root":"/weights/loaded-model"}]}`))
		got := remoteModelItem(entries[0], "VLLM", index)
		if got.ModelType != "llm" || !reflect.DeepEqual(got.Input, []string{"image", "text", "video"}) || !reflect.DeepEqual(got.Output, []string{"text"}) {
			t.Fatalf("same identity catalog evidence merged arbitrarily: %+v", got)
		}
		got.Input[0] = "mutated"
		if again := remoteModelItem(entries[0], "VLLM", index); !reflect.DeepEqual(again.Input, []string{"image", "text", "video"}) {
			t.Fatal("public response mutated the immutable recognition index")
		}
	}
}

func TestVLLMDiscoveryKeepsCatalogTypeWhenCompletingModalities(t *testing.T) {
	index := newUpstreamModelRecognitionIndex(upstreamCatalogFile{RecognitionModels: []upstreamCatalogModel{
		{Name: "vendor/visual", ModelType: "vlm", Input: []string{"text", "image"}, Output: []string{"text"}},
		{Name: "vendor/chat", ModelType: "llm", Input: []string{"text", "audio"}, Output: []string{"text"}},
	}})
	for _, tc := range []struct{ raw, want string }{
		{`{"data":[{"id":"alias","root":"/srv/models/visual"}]}`, "vlm"},
		{`{"data":[{"id":"alias","root":"/srv/models/chat","input_modalities":["audio"]}]}`, "llm"},
	} {
		entries := parseRemoteModelEntries([]byte(tc.raw))
		if got := remoteModelItem(entries[0], "VLLM", index); got.ModelType != tc.want {
			t.Fatalf("completed metadata changed confirmed type: %+v, want %s", got, tc.want)
		}
	}
}

type vllmIdentityTransport struct {
	t   *testing.T
	raw string
}

func (transport vllmIdentityTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Path != "/v1/models" || request.URL.RawQuery != "" {
		transport.t.Fatalf("unexpected discovery request: %s", request.URL)
	}
	raw := transport.raw
	if raw == "" {
		raw = `{"data":[{"id":"served-alias","root":"/private/weights/Qwen3.8-27B","max_model_len":262144}]}`
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(raw)), Request: request}, nil
}

func TestVLLMDiscoveryIgnoresNonStringRootWithoutBreakingModelLists(t *testing.T) {
	previous := remoteModelsHTTPClient
	t.Cleanup(func() { remoteModelsHTTPClient = previous })
	for _, root := range []string{`{"provider":"opaque"}`, `["not-an-identity"]`, `123`, `null`} {
		remoteModelsHTTPClient = &http.Client{Transport: vllmIdentityTransport{t: t, raw: `{"data":[{"id":"opaque-alias","root":` + root + `,"model_type":"llm","max_model_len":262144}]}`}}
		req, requestErr := http.NewRequest(http.MethodGet, "https://models.example.test/v1/models", nil)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		entries, err := fetchRemoteModelEntries(req)
		if err != nil || len(entries) != 1 {
			t.Fatalf("opaque extra root invalidated a legal models response: %v %+v", err, entries)
		}
		for _, provider := range []string{"OpenAI", "VLLM"} {
			got := remoteModelItem(entries[0], provider, nil)
			if got.ModelType != "llm" {
				t.Fatalf("opaque root lost existing metadata for %s: %+v", provider, got)
			}
			if provider == "VLLM" && (got.MaxInputTokens == nil || *got.MaxInputTokens != "262144") {
				t.Fatalf("opaque root lost serving length: %+v", got)
			}
		}
		remoteModelsHTTPClient = &http.Client{Transport: vllmIdentityTransport{t: t, raw: `{"data":[{"id":"opaque-alias","root":` + root + `,"max_model_len":262144}]}`}}
		entries, err = fetchRemoteModelEntries(req)
		if err != nil || len(entries) != 1 {
			t.Fatalf("opaque root prevented unknown model discovery: %v %+v", err, entries)
		}
		got := remoteModelItem(entries[0], "VLLM", nil)
		if got.ModelType != "" || got.MaxInputTokens == nil || *got.MaxInputTokens != "262144" {
			t.Fatalf("opaque root guessed a type or lost explicit serving length: %+v", got)
		}
	}
}

func TestVLLMDiscoveryRootRequiresSupplierIdentityAndPreservesManualModel(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, template, want string
		custom                         bool
	}{
		{"builtin", "OpenAI", "", "llm", false},
		{"template", "OpenAI", "vllm-template", "llm", true},
		{"custom name only", "OpenAI", "", "", true},
		{"different protocol retains serving identity", "Claude", "vllm-template", "llm", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupListProviderTestDB(t)
			store.Init(db, db, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			oldClient := remoteModelsHTTPClient
			remoteModelsHTTPClient = &http.Client{Transport: vllmIdentityTransport{t: t}}
			t.Cleanup(func() { remoteModelsHTTPClient = oldClient })
			oldIndex := upstreamModelRecognition.Load()
			upstreamModelRecognition.Store(newUpstreamModelRecognitionIndex(upstreamCatalogFile{RecognitionModels: []upstreamCatalogModel{{Name: "qwen/qwen3.8-27b", ModelType: "llm", Input: []string{"text", "image"}, Output: []string{"text"}}}}))
			t.Cleanup(func() { upstreamModelRecognition.Store(oldIndex) })
			base := orm.BaseModel{CreateUserID: "owner"}
			parent := orm.UserModelProvider{ID: "p", Name: "vLLM", IsCustom: tc.custom, ProtocolName: tc.protocol, DefaultModelProviderID: tc.template, Category: "model", Capabilities: "has_models,custom_base_url", BaseURL: "https://vllm-discovery.example.test/v1/", BaseModel: base}
			for _, row := range []any{
				&orm.DefaultModelProvider{ID: "vllm-template", Name: "vLLM"}, &parent,
				&orm.UserModelProviderGroup{ID: "g", UserModelProviderID: "p", BaseURL: parent.BaseURL, APIKey: "saved-key", BaseModel: base},
				&orm.UserModelProviderGroupModel{ID: "manual", UserModelProviderID: "p", UserModelProviderGroupID: "g", Name: "served-alias", ModelType: "embed", MaxInputTokens: func() *string { s := "8192"; return &s }(), BaseModel: base},
			} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			req := httptest.NewRequest(http.MethodGet, "/remote_models", nil)
			req.Header.Set("X-User-Id", "owner")
			req = mux.SetURLVars(req, map[string]string{"model_provider_id": "p", "group_id": "g"})
			rec := httptest.NewRecorder()
			ListRemoteGroupModels(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("discovery failed: %d %s", rec.Code, rec.Body.String())
			}
			var payload struct {
				Data remoteGroupModelsResponse `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Data.Models) != 1 || payload.Data.Models[0].ModelType != tc.want {
				t.Fatalf("supplier identity not enforced: %s", rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "/private/weights") {
				t.Fatal("private loaded path exposed")
			}
			var saved orm.UserModelProviderGroupModel
			if err := db.Take(&saved, "id = ?", "manual").Error; err != nil || saved.ModelType != "embed" || saved.MaxInputTokens == nil || *saved.MaxInputTokens != "8192" {
				t.Fatalf("discovery changed manual classification/context: %+v %v", saved, err)
			}
		})
	}
}
