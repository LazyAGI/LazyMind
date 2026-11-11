package modelprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestEmbeddingCleanupStateTransitions(t *testing.T) {
	for _, outcome := range []string{"success", "busy", "failure", "wrong-generation"} {
		t.Run(outcome, func(t *testing.T) {
			db, _, _, _ := setupProbeTest(t, &orm.EmbeddingIndexState{})
			before := orm.EmbeddingIndexState{Scope: "global", ActiveModelID: "old-model", ActiveSource: "own", Signature: "old-fingerprint", Phase: "ready", Generation: 7, UpdatedAt: time.Now().UTC()}
			if err := db.Create(&before).Error; err != nil {
				t.Fatal(err)
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var payload struct {
					Generation int64 `json:"generation"`
					EmptyOnly  bool  `json:"empty_only"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if r.URL.Path != "/internal/knowledge:clear-chunks" || r.Method != http.MethodPost || payload.Generation != 8 || payload.EmptyOnly || r.Header.Get("X-LazyMind-Internal-Token") != "test-internal-token" {
					t.Errorf("wrong cleanup request: %s %+v", r.URL.Path, payload)
				}
				var state orm.EmbeddingIndexState
				if err := db.Take(&state, "scope = ?", "global").Error; err != nil {
					t.Error(err)
				}
				if state.Generation != 8 || state.Phase != "clearing" || state.ActiveModelID != "old-model" {
					t.Errorf("cleanup did not fence old selection: %+v", state)
				}
				switch outcome {
				case "busy":
					w.WriteHeader(http.StatusConflict)
				case "failure":
					w.WriteHeader(http.StatusServiceUnavailable)
				case "wrong-generation":
					_, _ = w.Write([]byte(`{"cleared":true,"generation":7}`))
				default:
					_, _ = w.Write([]byte(`{"cleared":true,"generation":8}`))
				}
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "test-internal-token")
			err := clearEmbeddingChunks(context.Background(), db, before, "new-model", "own", "new-fingerprint", false)
			var after orm.EmbeddingIndexState
			if e := db.Take(&after, "scope = ?", "global").Error; e != nil {
				t.Fatal(e)
			}
			if calls != 1 {
				t.Fatalf("expected one cleanup request, got %d", calls)
			}
			switch outcome {
			case "success":
				if err != nil || after.Phase != "cleared" || after.Generation != 8 || after.ActiveModelID != "" || after.Signature != "" || after.TargetModelID != "" {
					t.Fatalf("unexpected completed reset: %+v err=%v", after, err)
				}
				if assessEmbeddingSwitch(after, 3, "new-model", "own", "fingerprint") != "empty" {
					t.Fatal("retained documents prevented model selection after cleanup")
				}
			case "busy":
				if !errors.Is(err, errEmbeddingIndexBusy) || after.Phase != "ready" || after.Generation != 7 || after.ActiveModelID != before.ActiveModelID {
					t.Fatalf("busy cleanup did not preserve previous state: %+v %v", after, err)
				}
			default:
				if !errors.Is(err, errEmbeddingCleanupFailed) || after.Phase != "cleanup_failed" || after.ActiveModelID != before.ActiveModelID {
					t.Fatalf("partial cleanup became usable: %+v %v", after, err)
				}
			}
			if outcome != "busy" {
				if err := clearEmbeddingChunks(context.Background(), db, before, "new-model", "own", "new-fingerprint", false); !errors.Is(err, errEmbeddingIndexBusy) {
					t.Fatalf("stale confirmation was accepted: %v", err)
				}
				if calls != 1 {
					t.Fatal("stale confirmation reached algorithm")
				}
			}
		})
	}
}

func TestClearEmbeddingChunksRequiresAdminAndExplicitConfirmation(t *testing.T) {
	setupProbeTest(t, &orm.EmbeddingIndexState{})
	for _, tc := range []struct {
		role, body string
		want       int
	}{
		{"user", `{"model_id":"probe-model","source":"own","generation":1,"confirm_clear_chunks":true}`, 403},
		{"admin", `{"model_id":"probe-model","source":"own","generation":1}`, 400},
		{"admin", `{"model_id":"probe-model","source":"own","generation":1,"confirm_clear_chunks":false}`, 400},
		{"admin", `{"model_id":"probe-model","source":"shared","generation":1,"confirm_clear_chunks":true}`, 400},
	} {
		r := httptest.NewRequest(http.MethodPost, "/model_providers/embedding/clear-chunks", strings.NewReader(tc.body))
		r.Header.Set("X-User-Id", "probe-owner")
		r.Header.Set("X-User-Role", tc.role)
		w := httptest.NewRecorder()
		ClearEmbeddingChunks(w, r)
		if w.Code != tc.want {
			t.Fatalf("got %d, want %d: %s", w.Code, tc.want, w.Body.String())
		}
	}
}

func TestEmptyDirectoriesAndDeletedDocumentsDoNotBlockEmbeddingSwitch(t *testing.T) {
	db, _, _, _ := setupProbeTest(t, &orm.EmbeddingIndexState{}, &orm.Dataset{}, &orm.Document{}, &orm.DocumentProcessingState{})
	state := orm.EmbeddingIndexState{Scope: "global", ActiveModelID: "old-model", Phase: "ready", Generation: 1, UpdatedAt: time.Now()}
	for _, row := range []any{&state, &orm.Dataset{ID: "empty", KbID: "empty"}, &orm.Dataset{ID: "removed", KbID: "removed"}, &orm.Document{ID: "removed-doc", DatasetID: "removed"}, &orm.DocumentProcessingState{DatasetID: "removed", DocumentID: "removed-doc", IndexStatus: "pending", UpdatedAt: time.Now()}} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Delete(&orm.Document{}, "id = ?", "removed-doc").Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&orm.Dataset{ID: "folders", KbID: "folders", ProcessingLevel: "indexed"},
		&orm.Document{ID: "folder", DatasetID: "folders", DocumentType: "FOLDER"},
		&orm.Document{ID: "nested-folder", DatasetID: "folders", DocumentType: "FOLDER", PID: "folder"},
		&orm.DocumentProcessingState{DatasetID: "folders", DocumentID: "folder", IndexStatus: "succeeded"},
		&orm.DocumentProcessingState{DatasetID: "folders", DocumentID: "nested-folder", IndexStatus: "pending"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	status, err := loadEmbeddingIndexStatus(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if status.AffectedKnowledgeBases != 0 || status.PendingKnowledgeBases != 0 || status.IndexedKnowledgeBases != 0 {
		t.Fatalf("empty directories still counted: %+v", status)
	}
	if assessEmbeddingSwitch(state, status.AffectedKnowledgeBases, "new-model", "own", "new-fingerprint") != "empty" {
		t.Fatal("empty knowledge base blocked selection")
	}
}

func TestManuallyClearedDocumentsAreNotReportedAsRebuilding(t *testing.T) {
	db, _, _, _ := setupProbeTest(t, &orm.EmbeddingIndexState{}, &orm.Dataset{}, &orm.Document{}, &orm.DocumentProcessingState{})
	state := orm.EmbeddingIndexState{Scope: "global", ActiveModelID: "new-model", Phase: "ready", Generation: 3, Signature: "v1:1:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64), UpdatedAt: time.Now()}
	for _, row := range []any{&state, &orm.Dataset{ID: "kb", KbID: "kb", ProcessingLevel: "indexed"}, &orm.Document{ID: "doc", DatasetID: "kb"}, &orm.DocumentProcessingState{DatasetID: "kb", DocumentID: "doc", EmbeddingVersion: "reembed:manual", UpdatedAt: time.Now()}} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	status, err := loadEmbeddingIndexStatus(context.Background(), db)
	if err != nil || status.Status != "empty" || status.PendingKnowledgeBases != 0 {
		t.Fatalf("manual parsing was presented as auto rebuilding: %+v %v", status, err)
	}
}
