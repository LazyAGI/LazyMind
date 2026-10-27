package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
)

func TestChatOnlySuppliersRejectEmbeddingAtEveryEntry(t *testing.T) {
	for _, source := range []string{"DeepSeek", "Kimi"} {
		t.Run(source, func(t *testing.T) {
			db, parent, group, _ := batchModelFixture(t)
			if err := db.Model(&parent).Update("protocol_name", source).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.AutoMigrate(&orm.UserSelectedModel{}, &orm.UserSelectedCloudModel{}); err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			AddGroupModel(rec, batchModelRequest(parent.ID, group.ID, parent.CreateUserID, []byte(`{"name":"unsupported-vector","model_type":"embed"}`)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("single add=%d %s", rec.Code, rec.Body.String())
			}
			rec, batch := callBatchModelAdd(t, parent.ID, group.ID, parent.CreateUserID, []addGroupModelRequest{
				{Name: "chat", ModelType: "llm"}, {Name: "unsupported-vector", ModelType: "embed"},
			})
			if rec.Code != http.StatusOK || batch.AddedCount != 1 || len(batch.Failures) != 1 || batch.Models[0].ModelType != "llm" {
				t.Fatalf("batch add=%d %s", rec.Code, rec.Body.String())
			}
			legacy := orm.UserModelProviderGroupModel{ID: "old-vector", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, Name: "old-vector", ProviderName: source, ModelType: "embed", BaseModel: parent.BaseModel}
			if err := db.Create(&legacy).Error; err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/selected", strings.NewReader(`{"selections":[{"model_key":"embed_main","model_id":"old-vector"}]}`))
			req.Header.Set("X-User-Id", parent.CreateUserID)
			rec = httptest.NewRecorder()
			SetSelectedModels(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("default selection=%d %s", rec.Code, rec.Body.String())
			}
			var calls atomic.Int32
			algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
			defer algorithm.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
			req = httptest.NewRequest(http.MethodPost, "/probe", nil)
			req.Header.Set("X-User-Id", parent.CreateUserID)
			req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID, "model_id": legacy.ID})
			rec = httptest.NewRecorder()
			ProbeGroupModel(rec, req)
			if result := decodeProbeTest(t, rec); result.Status != "unsupported" || calls.Load() != 0 {
				t.Fatalf("unsupported probe=%s calls=%d", rec.Body.String(), calls.Load())
			}
			var retained orm.UserModelProviderGroupModel
			if err := db.Take(&retained, "id = ?", legacy.ID).Error; err != nil || retained.ModelType != "embed" {
				t.Fatalf("unsupported historical inventory was modified: %+v %v", retained, err)
			}
		})
	}
}

func TestRegisteredRerankSuppliersCanAddSelectAndProbe(t *testing.T) {
	for _, source := range []string{"OpenAI", "Qwen", "GLM", "SiliconFlow", "Aiping"} {
		t.Run(source, func(t *testing.T) {
			db, parent, group, _ := batchModelFixture(t)
			if err := db.Model(&parent).Update("protocol_name", source).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&group).Updates(map[string]any{"api_key": "test-key", "is_verified": true}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.AutoMigrate(&orm.UserSelectedModel{}, &orm.UserSelectedCloudModel{}); err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			AddGroupModel(rec, batchModelRequest(parent.ID, group.ID, parent.CreateUserID, []byte(`{"name":"test-rerank","model_type":"rerank"}`)))
			if rec.Code != http.StatusOK {
				t.Fatalf("rerank add=%d %s", rec.Code, rec.Body.String())
			}
			var added struct {
				Data addGroupModelResponse `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &added); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/selected", strings.NewReader(`{"selections":[{"model_key":"reranker","model_id":"`+added.Data.ID+`"}]}`))
			req.Header.Set("X-User-Id", parent.CreateUserID)
			rec = httptest.NewRecorder()
			SetSelectedModels(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("rerank selection=%d %s", rec.Code, rec.Body.String())
			}
			var selection orm.UserSelectedModel
			if err := db.Take(&selection, "user_id = ? AND model_type = ?", parent.CreateUserID, "reranker").Error; err != nil || selection.UserModelProviderGroupModelID != added.Data.ID {
				t.Fatalf("rerank selection not persisted: %+v %v", selection, err)
			}
			var calls atomic.Int32
			algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var snapshot modelProbeSnapshot
				if err := json.NewDecoder(r.Body).Decode(&snapshot); err != nil || snapshot.Source != source || snapshot.ModelType != "rerank" || snapshot.Model != "test-rerank" {
					t.Errorf("unexpected rerank runtime contract: %+v %v", snapshot, err)
				}
				_, _ = w.Write([]byte(`{"status":"passed","code":"ok","model_type":"rerank","elapsed_ms":1}`))
			}))
			defer algorithm.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
			req = httptest.NewRequest(http.MethodPost, "/probe", nil)
			req.Header.Set("X-User-Id", parent.CreateUserID)
			req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID, "model_id": added.Data.ID})
			rec = httptest.NewRecorder()
			ProbeGroupModel(rec, req)
			if result := decodeProbeTest(t, rec); result.Status != "passed" || calls.Load() != 1 {
				t.Fatalf("rerank probe=%s calls=%d", rec.Body.String(), calls.Load())
			}
		})
	}
}
