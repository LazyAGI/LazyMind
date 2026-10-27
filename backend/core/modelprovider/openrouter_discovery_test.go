package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestOpenRouterDiscoveryRequestsAllModalitiesAndPreservesMetadata(t *testing.T) {
	remoteModelsAllowPrivateHosts = true
	t.Cleanup(func() { remoteModelsAllowPrivateHosts = false; store.Init(nil, nil, nil) })
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("output_modalities") != "all" {
			t.Errorf("missing all-modality discovery: %s", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("missing stored key authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[
		 {"id":"opaque-chat","architecture":{"input_modalities":["text","image"],"output_modalities":["text"]}},
		 {"id":"opaque-vector","model_type":"llm","context_length":8192,"architecture":{"input_modalities":["text"],"output_modalities":["embeddings"]}},
		 {"id":"opaque-image","architecture":{"input_modalities":["text","image"],"output_modalities":["image","text"]}},
		 {"id":"opaque-video","architecture":{"input_modalities":["text"],"output_modalities":["video"]}},
		 {"id":"opaque-speech","architecture":{"input_modalities":["text"],"output_modalities":["speech"]}},
		 {"id":"opaque-stt","architecture":{"input_modalities":["audio"],"output_modalities":["transcription"]}},
		 {"id":"opaque-rerank","architecture":{"input_modalities":["text"],"output_modalities":["rerank"]}},
		 {"id":"opaque-multi-vector","architecture":{"input_modalities":["image"],"output_modalities":["embeddings"]}},
		 {"id":"opaque-unknown","model_type":"llm","architecture":{"input_modalities":["text"],"output_modalities":["decisions"]}},
		 {"id":"opaque-mixed","architecture":{"input_modalities":["text"],"output_modalities":["text","audio"]}}
		]}`))
	}))
	defer upstream.Close()
	db, parent, group := setupEncryptedGroupKeyTest(t, "secret")
	if err := db.AutoMigrate(&orm.UserModelProviderGroupModel{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&parent).Updates(map[string]any{"protocol_name": "OpenRouter", "base_url": upstream.URL}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&group).Update("base_url", upstream.URL).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/remote_models", nil)
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
	rec := httptest.NewRecorder()
	ListRemoteGroupModels(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data remoteGroupModelsResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{"opaque-chat": "llm", "opaque-vector": "embed", "opaque-image": "text2image", "opaque-video": "text2video", "opaque-speech": "tts", "opaque-stt": "stt", "opaque-rerank": "rerank", "opaque-multi-vector": "multimodal_embedding", "opaque-unknown": "", "opaque-mixed": ""}
	if len(body.Data.Models) != len(expected) {
		t.Fatalf("models=%v", body.Data.Models)
	}
	for _, model := range body.Data.Models {
		if model.ModelType != expected[model.Name] {
			t.Errorf("%s type=%q want %q", model.Name, model.ModelType, expected[model.Name])
		}
		if model.Name == "opaque-unknown" && !reflect.DeepEqual(model.Output, []string{"decisions"}) {
			t.Errorf("lost raw task modality: %#v", model)
		}
		if model.Name == "opaque-vector" && (model.MaxInputTokens == nil || *model.MaxInputTokens != "8192") {
			t.Errorf("lost token limit: %#v", model)
		}
	}
}
