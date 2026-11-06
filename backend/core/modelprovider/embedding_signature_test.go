package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

func TestEmbeddingSignatureUsesSavedRuntimeAndReturnsOnlyDigests(t *testing.T) {
	db, parent, group, model := setupProbeTest(t)
	if err := db.Model(&model).Updates(map[string]any{
		"model_type":     "embed",
		"model_settings": `{"embedding":{"query_prefix":"q: ","passage_prefix":"p: "}}`,
	}).Error; err != nil {
		t.Fatal(err)
	}
	var requestBody map[string]any
	algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/model/embedding-signature" || r.Method != http.MethodPost {
			t.Errorf("unexpected algorithm request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"status":"ready","code":"ok","model_type":"embed","dimensions":2,"passage_signature":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","query_signature":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","api_key":"do-not-return","embedding":[0.1,0.2]}`))
	}))
	defer algorithm.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)

	rec := httptest.NewRecorder()
	SignGroupEmbeddingModel(rec, probeRequest(parent.ID, group.ID, model.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("signature status=%d body=%s", rec.Code, rec.Body.String())
	}
	var reply struct {
		Data EmbeddingSignatureResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Data.Status != "ready" || reply.Data.Dimensions != 2 || reply.Data.PassageSignature != strings.Repeat("a", 64) || reply.Data.QuerySignature != strings.Repeat("b", 64) {
		t.Fatalf("bad signature: %+v", reply.Data)
	}
	settings, ok := requestBody["embedding"].(map[string]any)
	if !ok || settings["query_prefix"] != "q: " || settings["passage_prefix"] != "p: " {
		t.Fatalf("saved options missing: %#v", requestBody["embedding"])
	}
	if requestBody["source"] != "openai" || requestBody["model_type"] != "embed" || requestBody["api_key"] != "active-test-key" || requestBody["model"] != model.Name {
		t.Fatalf("runtime identity incorrect: %#v", requestBody)
	}
	if strings.Contains(rec.Body.String(), "active-test-key") || strings.Contains(rec.Body.String(), "do-not-return") || strings.Contains(rec.Body.String(), "embedding") {
		t.Fatalf("signature leaked private data: %s", rec.Body.String())
	}
	var retained orm.UserModelProviderGroupModel
	if err := db.Take(&retained, "id = ?", model.ID).Error; err != nil || retained.ModelType != "embed" {
		t.Fatal("signature mutated model")
	}
}

func TestEmbeddingSignatureRejectsNonEmbeddingForeignAndInvalidResponses(t *testing.T) {
	db, parent, group, model := setupProbeTest(t)
	calls := 0
	algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"status":"ready","code":"ok","dimensions":2,"passage_signature":"bad","query_signature":"bad"}`))
	}))
	defer algorithm.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)

	rec := httptest.NewRecorder()
	SignGroupEmbeddingModel(rec, probeRequest(parent.ID, group.ID, model.ID))
	if rec.Code != http.StatusOK || calls != 0 || strings.Contains(rec.Body.String(), "ready") {
		t.Fatalf("nonembedding model was signed: %d %s", rec.Code, rec.Body.String())
	}
	if err := db.Model(&model).Update("model_type", "embed").Error; err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	SignGroupEmbeddingModel(rec, probeRequest("foreign", group.ID, model.ID))
	if rec.Code != http.StatusNotFound || calls != 0 {
		t.Fatalf("foreign model was signed: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	SignGroupEmbeddingModel(rec, probeRequest(parent.ID, group.ID, model.ID))
	if rec.Code != http.StatusOK || calls != 1 || strings.Contains(rec.Body.String(), `"ready"`) {
		t.Fatalf("invalid upstream digest accepted: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	SignGroupEmbeddingModel(rec, probeRequest(CloudSystemProviderID, "cloud-system", "cloud-model"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"unsupported"`) || calls != 1 {
		t.Fatalf("cloud signature must be unsupported: %d %s", rec.Code, rec.Body.String())
	}
}

func TestEmbeddingSignatureDiscardsResultWhenSavedConfigurationChanges(t *testing.T) {
	db, parent, group, model := setupProbeTest(t)
	if err := db.Model(&model).Update("model_type", "embed").Error; err != nil {
		t.Fatal(err)
	}
	algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := db.Model(&parent).Update("base_url", "https://changed.example/v1/").Error; err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"status":"ready","code":"ok","dimensions":2,"passage_signature":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","query_signature":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`))
	}))
	defer algorithm.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
	rec := httptest.NewRecorder()
	SignGroupEmbeddingModel(rec, probeRequest(parent.ID, group.ID, model.ID))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"configuration_changed"`) || strings.Contains(rec.Body.String(), "aaaa") {
		t.Fatalf("stale signature was exposed: %d %s", rec.Code, rec.Body.String())
	}
}
