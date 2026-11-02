package modelprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEmbeddingProbeSendsSavedOptionsAndMeasuredDimensions(t *testing.T) {
	db, parent, group, model := setupProbeTest(t)
	settings := `{"embedding":{"query_prefix":"  query: ","passage_prefix":"passage:  ","batch_size":16}}`
	if err := db.Model(&model).Updates(map[string]any{"model_type": "embed", "model_settings": settings}).Error; err != nil {
		t.Fatal(err)
	}
	algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		options, ok := body["embedding"].(map[string]any)
		if !ok || options["query_prefix"] != "  query: " || options["passage_prefix"] != "passage:  " || options["batch_size"] != float64(16) {
			t.Errorf("saved embedding options did not reach probe: %#v", body["embedding"])
		}
		_, _ = w.Write([]byte(`{"status":"passed","code":"ok","embedding_dimensions":1536,"embedding":[0.1,0.2]}`))
	}))
	defer algorithm.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
	rec := httptest.NewRecorder()
	ProbeGroupModel(rec, probeRequest(parent.ID, group.ID, model.ID))
	if result := decodeProbeTest(t, rec); result.Status != "passed" || result.ModelType != "embed" {
		t.Fatal(rec.Body.String())
	}
	var reply struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Data["embedding_dimensions"] != float64(1536) || strings.Contains(rec.Body.String(), "query:") || strings.Contains(rec.Body.String(), "0.1") {
		t.Fatalf("missing measured dimensions or leaked inference input/output: %s", rec.Body.String())
	}
}

func TestEmbeddingProbeRejectsSuccessAfterPrefixChanges(t *testing.T) {
	db, parent, group, model := setupProbeTest(t)
	if err := db.Model(&model).Updates(map[string]any{"model_type": "embed", "model_settings": `{"embedding":{"query_prefix":"old: "}}`}).Error; err != nil {
		t.Fatal(err)
	}
	algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := db.Model(&model).Update("model_settings", `{"embedding":{"query_prefix":"new: "}}`).Error; err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"status":"passed","code":"ok","embedding_dimensions":1024}`))
	}))
	defer algorithm.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
	rec := httptest.NewRecorder()
	ProbeGroupModel(rec, probeRequest(parent.ID, group.ID, model.ID))
	if result := decodeProbeTest(t, rec); result.Status != "failed" || result.Code != "configuration_changed" || strings.Contains(rec.Body.String(), "embedding_dimensions") {
		t.Fatalf("stale measured dimensions were exposed: %s", rec.Body.String())
	}
}

func TestProbeMeasuredDimensionsAreTaskAndOutcomeBounded(t *testing.T) {
	for _, fixture := range []struct {
		modelType, status, code, dimensions string
		want                                bool
	}{
		{"embed", "passed", "ok", "1024", true},
		{"multimodal_embedding", "passed", "ok", "768", true},
		{"llm", "passed", "ok", "1024", false},
		{"embed", "failed", "invalid_response", "1024", false},
		{"embed", "passed", "ok", "0", false},
		{"embed", "passed", "ok", "-1", false},
		{"embed", "passed", "ok", "9007199254740992", false},
		{"embed", "passed", "ok", "1.5", false},
		{"embed", "passed", "ok", `"1024"`, false},
	} {
		t.Run(fixture.modelType+"/"+fixture.status+"/"+fixture.dimensions, func(t *testing.T) {
			algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprintf(w, `{"status":%q,"code":%q,"embedding_dimensions":%s}`, fixture.status, fixture.code, fixture.dimensions)
			}))
			defer algorithm.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
			result := executeModelProbe(context.Background(), modelProbeSnapshot{Model: "fixture", ModelType: fixture.modelType}, time.Second)
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "embedding_dimensions") != fixture.want {
				t.Fatalf("unexpected measured dimension metadata: %s", encoded)
			}
		})
	}
}

func TestProbeHonorsSavedVisionFeatureRestriction(t *testing.T) {
	db, parent, group, model := setupProbeTest(t)
	if err := db.Model(&model).Update("model_settings", `{"feature_enabled":{"vision":false}}`).Error; err != nil {
		t.Fatal(err)
	}
	algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["vision"] != false {
			t.Error("disabled vision was still enabled in probe")
		}
		_, _ = w.Write([]byte(`{"status":"passed","code":"ok"}`))
	}))
	defer algorithm.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
	ProbeGroupModel(httptest.NewRecorder(), probeRequest(parent.ID, group.ID, model.ID))
}

func TestProbePassesSavedOptionalFeatureGatesToAlgorithm(t *testing.T) {
	db, parent, group, model := setupProbeTest(t)
	if err := db.Model(&model).Update("model_settings", `{"feature_enabled":{"tool_calling":false,"reasoning":false}}`).Error; err != nil {
		t.Fatal(err)
	}
	algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		features, ok := body["features"].(map[string]any)
		if !ok || features["tool_calling"] != false || features["reasoning"] != false {
			t.Errorf("saved model feature gates missing from probe: %#v", body["features"])
		}
		_, _ = w.Write([]byte(`{"status":"passed","code":"ok"}`))
	}))
	defer algorithm.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
	ProbeGroupModel(httptest.NewRecorder(), probeRequest(parent.ID, group.ID, model.ID))
}
