package modelprovider

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestUpstreamRecognitionFuzzyBasenameRequiresNormalizedEquality(t *testing.T) {
	index := newUpstreamModelRecognitionIndex(upstreamCatalogFile{Providers: map[string][]upstreamCatalogModel{"Qwen": {
		{Name: "qwen/qwen3.8-27b", ModelType: "llm"},
		{Name: "vendor/ranker_v2", ModelType: "rerank"},
		{Name: "vendor/special-v1:free", ModelType: "text2image"},
	}}, RecognitionModels: []upstreamCatalogModel{
		{Name: "one/conflicting.model", ModelType: "llm"},
		{Name: "two/conflicting_model", ModelType: "embed"},
		{Name: "only-unknown"},
		{Name: "exact-unknown"},
		{Name: "exact_unknown", ModelType: "llm"},
	}})
	for _, tc := range []struct{ id, want string }{
		{"qwen38-27b", "llm"},
		{" GATEWAY/QWEN_3.8_27B ", "llm"},
		{"ranker.v2", "rerank"},
		{"special_v1:free", "text2image"},
		{"special-v1", ""},
		{"special_v1:paid", ""},
		{"qwen3.8-4b", ""},
		{"qwen3.9-27b", ""},
		{"qwen3.80-27b", ""},
		{"prefix-qwen38-27b", ""},
		{"qwen38-27b-suffix", ""},
		{"conflicting-model", ""},
		{"only_unknown", ""},
		{"exact-unknown", ""},
		{"unknown-chat-name", ""},
	} {
		if got := index.modelType("Custom", tc.id); got != tc.want {
			t.Errorf("modelType(%q)=%q, want %q", tc.id, got, tc.want)
		}
	}
}

func TestFuzzyDiscoveryRespectsStrongerIdentityAndRawEvidence(t *testing.T) {
	index := newUpstreamModelRecognitionIndex(upstreamCatalogFile{RecognitionModels: []upstreamCatalogModel{
		{Name: "qwen/qwen3.8-27b", ModelType: "llm", Input: []string{"text", "image"}, Output: []string{"text"}, Context: 1000000},
		{Name: "BAAI/bge-m3", ModelType: "embed", Input: []string{"text"}, Output: []string{"embeddings"}, Context: 8192},
		{Name: "root-unknown"},
		{Name: "exact-unknown"},
		{Name: "exact_unknown", ModelType: "llm"},
		{Name: "exact-vector", ModelType: "embed", Input: []string{"text"}, Output: []string{"embeddings"}},
	}})
	for _, tc := range []struct{ name, provider, raw, want string }{
		{"fuzzy ID", "VLLM", `{"id":"qwen38-27b","max_model_len":262144}`, "llm"},
		{"root exact before fuzzy ID", "VLLM", `{"id":"qwen38-27b","root":"/srv/models/bge-m3","max_model_len":262144}`, "embed"},
		{"root fuzzy before fuzzy ID", "VLLM", `{"id":"qwen38-27b","root":"/srv/models/BGE_M3","max_model_len":262144}`, "embed"},
		{"root explicit unknown blocks fuzzy ID", "VLLM", `{"id":"qwen38-27b","root":"/srv/models/root-unknown","max_model_len":262144}`, ""},
		{"ID explicit unknown blocks root", "VLLM", `{"id":"exact-unknown","root":"/srv/models/Qwen3.8-27B","max_model_len":262144}`, ""},
		{"ID exact before root", "VLLM", `{"id":"exact-vector","root":"/srv/models/Qwen3.8-27B","max_model_len":262144}`, "embed"},
		{"unknown raw task blocks fuzzy", "VLLM", `{"id":"qwen38-27b","task":"future-task","max_model_len":262144}`, "future-task"},
		{"unknown raw output blocks fuzzy", "VLLM", `{"id":"qwen38-27b","output_modalities":["decisions"],"max_model_len":262144}`, ""},
		{"known raw task before fuzzy", "VLLM", `{"id":"qwen38-27b","task":"tts","max_model_len":262144}`, "tts"},
		{"other provider cannot use root", "OpenAI", `{"id":"qwen38-27b","root":"/srv/models/BGE_M3","context_length":262144}`, "llm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var entry openaiModelListEntry
			if err := json.Unmarshal([]byte(tc.raw), &entry); err != nil {
				t.Fatal(err)
			}
			got := remoteModelItem(entry, tc.provider, index)
			if got.ModelType != tc.want || got.ID != entry.ID || got.Name != entry.ID {
				t.Fatalf("identity/type priority changed: %+v, want %s", got, tc.want)
			}
			if got.MaxInputTokens == nil || *got.MaxInputTokens != "262144" {
				t.Fatalf("discovery replaced explicit serving context: %+v", got)
			}
		})
	}
	entries := parseRemoteModelEntries([]byte(`{"data":[{"id":"qwen_38_27b"}]}`))
	got := remoteModelItem(entries[0], "OpenAI", index)
	if got.ModelType != "llm" || !reflect.DeepEqual(got.Input, []string{"image", "text"}) || !reflect.DeepEqual(got.Output, []string{"text"}) {
		t.Fatalf("fuzzy metadata did not come from the resolved typed identity: %+v", got)
	}
	entries[0].InputModalities = []string{"text"}
	if got := remoteModelItem(entries[0], "OpenAI", index); !reflect.DeepEqual(got.Input, []string{"text"}) {
		t.Fatalf("fuzzy match widened explicit input: %+v", got)
	}
}
