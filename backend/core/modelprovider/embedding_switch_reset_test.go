package modelprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func TestConcurrentConfirmedEmbeddingSwitchRejectsEditAndBusyRetryWithoutLosingTarget(t *testing.T) {
	db, parent, _, model := setupProbeTest(t, &orm.EmbeddingIndexState{}, &orm.UserSelectedModel{}, &orm.UserSelectedCloudModel{}, &orm.Dataset{}, &orm.Document{}, &orm.DocumentProcessingState{})
	if err := db.Model(&model).Updates(map[string]any{"model_type": "embed", "provider_name": "OpenAI"}).Error; err != nil {
		t.Fatal(err)
	}
	before := orm.EmbeddingIndexState{Scope: "global", Phase: "ready", Generation: 7, ActiveModelID: "old-model", UpdatedAt: time.Now()}
	if err := db.Create(&before).Error; err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	cleanupLock := make(chan struct{}, 1)
	algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/model/embedding-signature" {
			_, _ = w.Write([]byte(`{"status":"ready","code":"ok","dimensions":2,"passage_signature":"` + strings.Repeat("a", 64) + `","query_signature":"` + strings.Repeat("b", 64) + `"}`))
			return
		}
		if r.URL.Path != "/internal/knowledge:clear-chunks" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		select {
		case cleanupLock <- struct{}{}:
			defer func() { <-cleanupLock }()
		default:
			w.WriteHeader(http.StatusConflict)
			return
		}
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write([]byte(`{"cleared":true,"generation":8}`))
	}))
	defer algorithm.Close()
	defer releaseOnce.Do(func() { close(release) })
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/embedding/clear-chunks", strings.NewReader(`{"model_id":"probe-model","source":"own","generation":7,"confirm_clear_chunks":true}`))
		r.Header.Set("X-User-Id", model.CreateUserID)
		r.Header.Set("X-User-Role", "admin")
		w := httptest.NewRecorder()
		ClearEmbeddingChunks(w, r)
		return w
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- request() }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("switch did not reach cleanup")
	}
	r := httptest.NewRequest(http.MethodPut, "/endpoint", strings.NewReader(`{"base_url":"https://changed.example/v1"}`))
	r.Header.Set("X-User-Id", model.CreateUserID)
	r = mux.SetURLVars(r, map[string]string{"model_provider_id": parent.ID})
	edit := httptest.NewRecorder()
	UpdateProviderEndpoint(edit, r)
	if edit.Code != http.StatusConflict {
		t.Fatalf("concurrent target edit accepted: %d %s", edit.Code, edit.Body.String())
	}
	busy := request()
	if busy.Code != http.StatusConflict {
		t.Fatalf("overlapping busy cleanup accepted: %d %s", busy.Code, busy.Body.String())
	}
	var pending orm.EmbeddingIndexState
	if err := db.Take(&pending, "scope = ?", "global").Error; err != nil {
		t.Fatal(err)
	}
	if pending.Phase != "clearing" || pending.Generation != 8 || pending.TargetModelID != model.ID {
		t.Fatalf("busy retry lost target: %+v", pending)
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case w := <-done:
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"activated":true`) {
			t.Fatalf("original request failed: %d %s", w.Code, w.Body.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("switch did not complete")
	}
	var after orm.EmbeddingIndexState
	if err := db.Take(&after, "scope = ?", "global").Error; err != nil {
		t.Fatal(err)
	}
	if after.Phase != "ready" || after.Generation != 8 || after.ActiveModelID != model.ID {
		t.Fatalf("concurrent request corrupted activation: %+v", after)
	}
}

func TestPendingEmbeddingSwitchProtectsTargetRuntimeAndRejectsUnrelatedConfirmations(t *testing.T) {
	for _, phase := range []string{"clearing", "cleanup_failed", "cleared"} {
		t.Run(phase, func(t *testing.T) {
			db, parent, group, model := setupProbeTest(t, &orm.EmbeddingIndexState{}, &orm.Dataset{}, &orm.Document{}, &orm.DocumentProcessingState{})
			if err := db.Model(&model).Update("model_type", "embed").Error; err != nil {
				t.Fatal(err)
			}
			fingerprint := "v1:2:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)
			state := orm.EmbeddingIndexState{Scope: "global", Phase: phase, Generation: 8, TargetModelID: model.ID, TargetSource: "own", TargetSignature: fingerprint, UpdatedAt: time.Now()}
			if err := db.Create(&state).Error; err != nil {
				t.Fatal(err)
			}
			if err := guardEmbeddingRuntimeMutation(db, model.CreateUserID, parent.ID, group.ID, model.ID); !errors.Is(err, errEmbeddingIdentityInUse) {
				t.Fatalf("pending target was editable: %v", err)
			}
			if err := guardEmbeddingRemoval(db, model.CreateUserID, model.ID); !errors.Is(err, errEmbeddingInUse) {
				t.Fatalf("pending target was removable: %v", err)
			}
			// Endpoint edits have an empty-KB exception; it must not bypass the pending target guard.
			r := httptest.NewRequest(http.MethodPut, "/endpoint", strings.NewReader(`{"base_url":"https://changed.example/v1"}`))
			r.Header.Set("X-User-Id", model.CreateUserID)
			r = mux.SetURLVars(r, map[string]string{"model_provider_id": parent.ID})
			w := httptest.NewRecorder()
			UpdateProviderEndpoint(w, r)
			if w.Code != http.StatusConflict {
				t.Fatalf("pending endpoint edit accepted: %d %s", w.Code, w.Body.String())
			}
			if outcome := assessEmbeddingSwitch(state, 0, model.ID, "own", fingerprint); outcome != "clear_required" {
				t.Fatalf("normal save bypassed pending activation: %s", outcome)
			}
			snapshot, unavailable, err := loadProbeSnapshot(t.Context(), db, model.CreateUserID, parent.ID, group.ID, model.ID)
			if err != nil || unavailable != nil {
				t.Fatalf("fixture unavailable: %v %+v", err, unavailable)
			}
			target := embeddingSwitchActivation{UserID: model.CreateUserID, ModelID: model.ID, Snapshot: snapshot}
			for _, req := range []EmbeddingClearRequest{
				{ModelID: model.ID, Source: "own", Generation: 6, ConfirmClearChunks: true},
				{ModelID: "other-model", Source: "own", Generation: 7, ConfirmClearChunks: true},
			} {
				if err := target.run(t.Context(), db, req, fingerprint); !errors.Is(err, errEmbeddingIndexBusy) {
					t.Fatalf("unrelated confirmation accepted: %+v %v", req, err)
				}
			}
			var after orm.EmbeddingIndexState
			if err := db.Take(&after, "scope = ?", "global").Error; err != nil {
				t.Fatal(err)
			}
			if after.Phase != state.Phase || after.Generation != state.Generation || after.TargetModelID != state.TargetModelID {
				t.Fatalf("rejected request changed pending operation: %+v", after)
			}
		})
	}
}

func TestConfirmedEmbeddingSwitchRejectsDisabledTargetBeforePurging(t *testing.T) {
	db, _, _, model := setupProbeTest(t, &orm.EmbeddingIndexState{})
	if err := db.Model(&model).Updates(map[string]any{"model_type": "embed", "enabled": false}).Error; err != nil {
		t.Fatal(err)
	}
	before := orm.EmbeddingIndexState{Scope: "global", Phase: "ready", Generation: 7, ActiveModelID: "old-model", UpdatedAt: time.Now()}
	if err := db.Create(&before).Error; err != nil {
		t.Fatal(err)
	}
	algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/model/embedding-signature" {
			t.Errorf("disabled target caused cleanup: %s", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ready","code":"ok","dimensions":2,"passage_signature":"` + strings.Repeat("a", 64) + `","query_signature":"` + strings.Repeat("b", 64) + `"}`))
	}))
	defer algorithm.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
	r := httptest.NewRequest(http.MethodPost, "/embedding/clear-chunks", strings.NewReader(`{"model_id":"probe-model","source":"own","generation":7,"confirm_clear_chunks":true}`))
	r.Header.Set("X-User-Id", model.CreateUserID)
	r.Header.Set("X-User-Role", "admin")
	w := httptest.NewRecorder()
	ClearEmbeddingChunks(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("disabled target accepted: %d %s", w.Code, w.Body.String())
	}
	var after orm.EmbeddingIndexState
	if err := db.Take(&after, "scope = ?", "global").Error; err != nil {
		t.Fatal(err)
	}
	if after.Phase != before.Phase || after.Generation != before.Generation || after.ActiveModelID != before.ActiveModelID || after.TargetModelID != "" {
		t.Fatalf("disabled target mutated index: %+v", after)
	}
}

func TestConfirmedEmbeddingSwitchCompletesAndRecoversWithoutSecondSelectionRequest(t *testing.T) {
	for _, scenario := range []string{"success", "disconnect", "save-failure", "configuration-change", "cleanup-failure", "restart-clearing", "restart-cleared"} {
		t.Run(scenario, func(t *testing.T) {
			db, _, _, model := setupProbeTest(t, &orm.EmbeddingIndexState{}, &orm.UserSelectedModel{}, &orm.UserSelectedCloudModel{})
			if err := db.Model(&model).Updates(map[string]any{"model_type": "embed", "provider_name": "OpenAI"}).Error; err != nil {
				t.Fatal(err)
			}
			fingerprint := "v1:2:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)
			before := orm.EmbeddingIndexState{Scope: "global", ActiveModelID: "old-model", ActiveSource: "own", Signature: "old-signature", Phase: "ready", Generation: 7, UpdatedAt: time.Now()}
			if scenario == "restart-clearing" || scenario == "restart-cleared" {
				before.Generation, before.TargetModelID, before.TargetSource, before.TargetSignature = 8, model.ID, "own", fingerprint
				before.Phase = strings.TrimPrefix(scenario, "restart-")
				if before.Phase == "cleared" {
					before.ActiveModelID, before.ActiveSource, before.Signature = "", "", ""
				}
			}
			if err := db.Create(&before).Error; err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cleanupCalls := 0
			probeUnavailable := false
			failSave := scenario == "save-failure"
			if failSave {
				callback := "test:fail-embedding-selection"
				if err := db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
					if failSave && tx.Statement.Schema != nil && tx.Statement.Schema.Table == "user_selected_models" {
						_ = tx.AddError(errors.New("test selection write failed"))
					}
				}); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Callback().Create().Remove(callback) })
			}
			algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/model/embedding-signature" {
					if probeUnavailable {
						t.Error("completed replay unexpectedly probed the unavailable provider")
						w.WriteHeader(503)
						return
					}
					_, _ = w.Write([]byte(`{"status":"ready","code":"ok","dimensions":2,"passage_signature":"` + strings.Repeat("a", 64) + `","query_signature":"` + strings.Repeat("b", 64) + `"}`))
					return
				}
				if r.URL.Path != "/internal/knowledge:clear-chunks" {
					t.Errorf("unexpected algorithm path %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				cleanupCalls++
				if scenario == "disconnect" {
					cancel()
				}
				if scenario == "configuration-change" && cleanupCalls == 1 {
					if err := db.Model(&model).Update("name", "changed-runtime").Error; err != nil {
						t.Error(err)
					}
				}
				if scenario == "cleanup-failure" && cleanupCalls == 1 {
					w.WriteHeader(503)
					return
				}
				_, _ = w.Write([]byte(`{"cleared":true,"generation":8}`))
			}))
			defer algorithm.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
			request := func(requestCtx context.Context) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodPost, "/embedding/clear-chunks", strings.NewReader(`{"model_id":"probe-model","source":"own","generation":7,"confirm_clear_chunks":true}`)).WithContext(requestCtx)
				r.Header.Set("X-User-Id", model.CreateUserID)
				r.Header.Set("X-User-Role", "admin")
				w := httptest.NewRecorder()
				ClearEmbeddingChunks(w, r)
				return w
			}
			w := request(ctx)
			if scenario == "save-failure" || scenario == "configuration-change" || scenario == "cleanup-failure" {
				if w.Code != http.StatusConflict || strings.Contains(w.Body.String(), `"activated":true`) {
					t.Fatalf("failed switch reported success: %d %s", w.Code, w.Body.String())
				}
				var pending orm.EmbeddingIndexState
				if err := db.Take(&pending, "scope = ?", "global").Error; err != nil {
					t.Fatal(err)
				}
				if pending.TargetModelID != model.ID || pending.TargetSignature != fingerprint || pending.Generation != 8 || pending.Phase == "ready" {
					t.Fatalf("lost recoverable target: %+v", pending)
				}
				var selected int64
				if err := db.Model(&orm.UserSelectedModel{}).Count(&selected).Error; err != nil || selected != 0 {
					t.Fatalf("failed activation partially saved selection: %d %v", selected, err)
				}
				if scenario == "configuration-change" {
					if err := db.Model(&model).Update("name", model.Name).Error; err != nil {
						t.Fatal(err)
					}
				}
				failSave = false
				w = request(t.Context())
			}
			var result struct {
				Data struct {
					Cleared   bool `json:"cleared"`
					Activated bool `json:"activated"`
				} `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || !result.Data.Cleared || !result.Data.Activated {
				t.Fatalf("server switch failed: %d %s (%v)", w.Code, w.Body.String(), err)
			}
			var after orm.EmbeddingIndexState
			if err := db.Take(&after, "scope = ?", "global").Error; err != nil {
				t.Fatal(err)
			}
			if after.Phase != "ready" || after.ActiveModelID != model.ID || after.Signature != fingerprint || after.TargetModelID != "" || after.Generation != 8 {
				t.Fatalf("switch did not activate atomically: %+v", after)
			}
			var selection orm.UserSelectedModel
			if err := db.Take(&selection, "user_id = ? AND model_type = ?", model.CreateUserID, "embed_main").Error; err != nil || selection.UserModelProviderGroupModelID != model.ID {
				t.Fatalf("selection not committed: %+v %v", selection, err)
			}
			wantCalls := 1
			if scenario == "restart-cleared" {
				wantCalls = 0
			}
			if scenario == "cleanup-failure" {
				wantCalls = 2
			}
			if cleanupCalls != wantCalls {
				t.Fatalf("cleanup calls=%d, want=%d", cleanupCalls, wantCalls)
			}
			// A lost success response must not purge or probe the newly active index.
			probeUnavailable = true
			w = request(t.Context())
			if w.Code != 200 || cleanupCalls != wantCalls {
				t.Fatalf("retry was not idempotent: %d %s calls=%d", w.Code, w.Body.String(), cleanupCalls)
			}
		})
	}
}
