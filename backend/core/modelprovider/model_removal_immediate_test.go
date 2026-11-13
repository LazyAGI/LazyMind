package modelprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
)

func TestSingleModelRemovalNeverWaitsForIndexServices(t *testing.T) {
	for _, phase := range []string{"ready", "clearing", "cleanup_failed", "unknown", "legacy"} {
		t.Run(phase, func(t *testing.T) {
			db, parent, group, model := setupProbeTest(t, &orm.EmbeddingIndexState{}, &orm.UserSelectedModel{}, &orm.Dataset{}, &orm.Document{}, &orm.DocumentProcessingState{})
			db.Model(&parent).Update("capabilities", "has_models")
			db.Model(&model).Update("model_type", "embed")
			selected := orm.UserSelectedModel{UserID: model.CreateUserID, ModelKey: "embed_main", UserModelProviderGroupModelID: model.ID}
			if err := db.Create(&selected).Error; err != nil {
				t.Fatal(err)
			}
			state := orm.EmbeddingIndexState{Scope: globalEmbeddingScope, ActiveModelID: model.ID, ActiveSource: "own", ActiveModelName: "removed model", Phase: phase, Generation: 7, Signature: "old-index-identity"}
			if phase == "clearing" {
				state.TargetModelID = model.ID
				state.TargetSource = "own"
				state.TargetSignature = "pending"
			}
			if phase != "legacy" {
				if err := db.Create(&state).Error; err != nil {
					t.Fatal(err)
				}
			}
			kb := orm.Dataset{ID: "kb", KbID: "kb", ProcessingLevel: "indexed"}
			doc := orm.Document{ID: "doc", DatasetID: "kb"}
			artifact := orm.DocumentProcessingState{DatasetID: "kb", DocumentID: "doc", ParseStatus: "succeeded", IndexStatus: "succeeded", IndexArtifactRef: "retain-vectors"}
			for _, row := range []any{&kb, &doc, &artifact} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(503) }))
			defer upstream.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", upstream.URL)
			req := httptest.NewRequest(http.MethodDelete, "/models/"+model.ID, nil)
			req.Header.Set("X-User-Id", model.CreateUserID)
			req.Header.Set("X-User-Role", "member")
			req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID, "model_id": model.ID})
			rec := httptest.NewRecorder()
			DeleteGroupModel(rec, req)
			if rec.Code != 200 {
				t.Fatalf("delete=%d %s", rec.Code, rec.Body.String())
			}
			if calls.Load() != 0 {
				t.Fatal("removal called the index service")
			}
			var remaining int64
			db.Model(&orm.UserSelectedModel{}).Where("user_model_provider_group_model_id = ?", model.ID).Count(&remaining)
			if remaining != 0 {
				t.Fatal("default model reference retained")
			}
			db.Model(&orm.UserModelProviderGroupModel{}).Where("id = ? AND deleted_at IS NULL", model.ID).Count(&remaining)
			if remaining != 0 {
				t.Fatal("model retained")
			}
			var after orm.EmbeddingIndexState
			if err := db.Take(&after, "scope = ?", globalEmbeddingScope).Error; err != nil {
				t.Fatal(err)
			}
			if after.ActiveModelID != "" || after.TargetModelID != "" || after.ActiveModelName != "" || (phase != "legacy" && after.Generation != 8) {
				t.Fatalf("dangling runtime identity: %+v", after)
			}
			if classifyEmbeddingIndexState(after, true, 1, 0).ReadinessReason == "" {
				t.Fatal("retained old vectors incorrectly usable")
			}
			var retained orm.DocumentProcessingState
			if err := db.Take(&retained, "document_id = ?", "doc").Error; err != nil {
				t.Fatal(err)
			}
			if retained.ParseStatus != "succeeded" || retained.IndexArtifactRef != "retain-vectors" {
				t.Fatalf("removal destroyed parsed/indexed content: %+v", retained)
			}
		})
	}
}

func TestModelRemovalDetachesOnlyMatchingGlobalIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, modelType, active, target, phase string
		detach                                 bool
	}{
		{"unrelated unknown index", "embed", "other", "", "unknown", false},
		{"unrelated active cleanup", "embed", "other", "next", "clearing", false},
		{"pending switch target", "embed", "other", "self", "clearing", true},
		{"stale type still referenced", "llm", "self", "", "ready", true},
		{"ordinary default", "llm", "other", "", "ready", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, parent, group, model := setupProbeTest(t, &orm.EmbeddingIndexState{}, &orm.UserSelectedModel{})
			db.Model(&parent).Update("capabilities", "has_models")
			db.Model(&model).Update("model_type", tc.modelType)
			active, target := tc.active, tc.target
			if active == "self" {
				active = model.ID
			}
			if target == "self" {
				target = model.ID
			}
			state := orm.EmbeddingIndexState{Scope: globalEmbeddingScope, ActiveModelID: active, ActiveSource: "own", TargetModelID: target, TargetSource: "own", Phase: tc.phase, Generation: 9, Signature: validTestEmbeddingFingerprint}
			if err := db.Create(&state).Error; err != nil {
				t.Fatal(err)
			}
			for _, slot := range []string{"chat_main", "embed_main"} {
				if err := db.Create(&orm.UserSelectedModel{UserID: model.CreateUserID, ModelKey: slot, UserModelProviderGroupModelID: model.ID}).Error; err != nil {
					t.Fatal(err)
				}
			}
			req := httptest.NewRequest(http.MethodDelete, "/model", nil)
			req.Header.Set("X-User-Id", model.CreateUserID)
			req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID, "model_id": model.ID})
			rec := httptest.NewRecorder()
			DeleteGroupModel(rec, req)
			if rec.Code != 200 {
				t.Fatalf("delete=%d %s", rec.Code, rec.Body.String())
			}
			var after orm.EmbeddingIndexState
			if err := db.Take(&after, "scope = ?", globalEmbeddingScope).Error; err != nil {
				t.Fatal(err)
			}
			if tc.detach {
				if after.Generation != 10 || after.ActiveModelID != "" || after.TargetModelID != "" || after.Phase != "detached" {
					t.Fatalf("pending/active identity survived: %+v", after)
				}
				if assessEmbeddingSwitch(after, 0, "new", "own", validTestEmbeddingFingerprint) != "clear_required" {
					t.Fatal("new model bypassed orphan-vector cleanup")
				}
			} else if after.ActiveModelID != state.ActiveModelID || after.TargetModelID != state.TargetModelID || after.Phase != state.Phase || after.Generation != 9 {
				t.Fatalf("unrelated index changed: %+v", after)
			}
			var refs int64
			db.Model(&orm.UserSelectedModel{}).Where("user_model_provider_group_model_id = ?", model.ID).Count(&refs)
			if refs != 0 {
				t.Fatal("default slots still reference removed model")
			}
		})
	}
}

func TestModelRemovalFencesLateCleanupResponse(t *testing.T) {
	db, parent, group, model := setupProbeTest(t, &orm.EmbeddingIndexState{}, &orm.UserSelectedModel{})
	db.Model(&parent).Update("capabilities", "has_models")
	state := orm.EmbeddingIndexState{Scope: globalEmbeddingScope, ActiveModelID: model.ID, ActiveSource: "own", Phase: "ready", Generation: 7}
	if err := db.Create(&state).Error; err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte(`{"cleared":true,"generation":8}`))
	}))
	defer service.Close()
	defer once.Do(func() { close(release) })
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", service.URL)
	done := make(chan error, 1)
	go func() { done <- clearEmbeddingChunks(context.Background(), db, state, "", "", "", false) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup never started")
	}
	req := httptest.NewRequest(http.MethodDelete, "/model", nil)
	req.Header.Set("X-User-Id", model.CreateUserID)
	req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID, "model_id": model.ID})
	removed := make(chan *httptest.ResponseRecorder, 1)
	go func() { rec := httptest.NewRecorder(); DeleteGroupModel(rec, req); removed <- rec }()
	select {
	case rec := <-removed:
		if rec.Code != 200 {
			t.Fatalf("delete=%d %s", rec.Code, rec.Body.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("removal waited on cleanup")
	}
	once.Do(func() { close(release) })
	if err := <-done; err == nil {
		t.Fatal("stale cleanup response accepted")
	}
	var after orm.EmbeddingIndexState
	if err := db.Take(&after, "scope = ?", globalEmbeddingScope).Error; err != nil {
		t.Fatal(err)
	}
	if after.Phase != "detached" || after.Generation != 9 {
		t.Fatalf("late response replaced removal: %+v", after)
	}
	projected, err := projectGlobalSelectedEmbedding(t.Context(), db, model.CreateUserID, []selectedModelItem{{ModelKey: "embed_main", ModelID: "stale-other-selection", Source: "own"}})
	if err != nil || len(projected) != 0 {
		t.Fatalf("global default must be empty after removal: %+v %v", projected, err)
	}
}
