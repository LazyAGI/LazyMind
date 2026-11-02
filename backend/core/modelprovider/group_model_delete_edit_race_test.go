package modelprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
)

func TestDeleteGroupModelRevalidatesTypeAfterConcurrentEdit(t *testing.T) {
	for _, test := range []struct {
		name, before, after string
		indexed             bool
		wantStatus          int
	}{
		{"llm becomes embedding requires confirmation", "llm", "embed", true, http.StatusConflict},
		{"embedding becomes llm no longer requires confirmation", "embed", "llm", false, http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, model := modelEditFixture(t, test.before, false)
			if err := db.AutoMigrate(&orm.UserSelectedModel{}, &orm.Dataset{}, &orm.DocumentProcessingState{}); err != nil {
				t.Fatal(err)
			}
			if test.indexed {
				if err := db.Create(&orm.Dataset{ID: "delete-edit-indexed", KbID: "delete-edit-indexed", ProcessingLevel: "indexed", ProcessingRevision: 2, BaseModel: orm.BaseModel{CreateUserID: "user-1"}}).Error; err != nil {
					t.Fatal(err)
				}
			}
			read, edited := make(chan struct{}), make(chan struct{})
			var intercepted, locked, rereadUnderLock atomic.Bool
			const queryCallback = "test:pause-delete-model-before-configuration-edit"
			if err := db.Callback().Query().After("gorm:query").Register(queryCallback, func(tx *gorm.DB) {
				if tx.Error != nil {
					return
				}
				if tx.Statement.Table == "user_model_providers" {
					if locking, ok := tx.Statement.Clauses["FOR"].Expression.(clause.Locking); ok && locking.Strength == "UPDATE" {
						locked.Store(true)
					}
				}
				row, ok := tx.Statement.Dest.(*orm.UserModelProviderGroupModel)
				if !ok || row.ID != model.ID {
					return
				}
				if !intercepted.CompareAndSwap(false, true) {
					// The editor also re-reads under the same lock. Count only a
					// DELETE transaction's read, distinguished by request context.
					if tx.Statement.Context.Value(deleteEditRequestContextKey{}) == true && locked.Load() {
						rereadUnderLock.Store(true)
					}
					return
				}
				close(read)
				select {
				case <-edited:
				case <-time.After(5 * time.Second):
					t.Error("metadata edit never completed")
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Callback().Query().Remove(queryCallback) })
			result := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				req := httptest.NewRequest(http.MethodDelete, "/model", nil)
				req = req.WithContext(context.WithValue(req.Context(), deleteEditRequestContextKey{}, true))
				req.Header.Set("X-User-Id", "user-1")
				req = mux.SetURLVars(req, map[string]string{"model_provider_id": model.UserModelProviderID, "group_id": model.UserModelProviderGroupID, "model_id": model.ID})
				rec := httptest.NewRecorder()
				DeleteGroupModel(rec, req)
				result <- rec
			}()
			select {
			case <-read:
			case <-time.After(5 * time.Second):
				t.Fatal("DELETE never read the original model")
			}
			edit := modelEditCall(t, model.ID, "user-1", `{"model_type":"`+test.after+`"}`)
			// Ignore locks taken by the completed PATCH; only a DELETE lock
			// acquired after this point can protect the final decision.
			locked.Store(false)
			close(edited)
			if edit.Code != http.StatusOK {
				t.Fatalf("concurrent edit=%d %s", edit.Code, edit.Body.String())
			}
			var rec *httptest.ResponseRecorder
			select {
			case rec = <-result:
			case <-time.After(5 * time.Second):
				t.Fatal("DELETE did not resume")
			}
			if rec.Code != test.wantStatus {
				t.Errorf("DELETE after %s -> %s: status=%d want=%d body=%s", test.before, test.after, rec.Code, test.wantStatus, rec.Body.String())
			}
			if test.wantStatus == http.StatusConflict && !strings.Contains(rec.Body.String(), "embedding model is in use") {
				t.Errorf("wrong confirmation error: %s", rec.Body.String())
			}
			if !locked.Load() || !rereadUnderLock.Load() {
				t.Error("DELETE must lock the supplier and re-read the current model before deciding confirmation/deletion")
			}
			saved := loadEditedModel(t, db, model.ID)
			if saved.ModelType != test.after || (saved.DeletedAt != nil) != (test.wantStatus == http.StatusOK) {
				t.Errorf("wrong final model: %+v", saved)
			}
			if test.indexed {
				var dataset orm.Dataset
				if err := db.Take(&dataset, "id = ?", "delete-edit-indexed").Error; err != nil {
					t.Fatal(err)
				}
				if dataset.ProcessingLevel != "indexed" || dataset.ProcessingRevision != 2 {
					t.Fatalf("unconfirmed deletion downgraded knowledge base: %+v", dataset)
				}
			}
		})
	}
}

type deleteEditRequestContextKey struct{}

func TestModelEditorCatalogNoOpDoesNotRequireCustomization(t *testing.T) {
	db, model := modelEditFixture(t, "llm", true)
	rec := modelEditCall(t, model.ID, "user-1", `{"name":"model-original","model_type":"llm","vision":false,"max_input_tokens":"128K"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("unchanged catalog save=%d %s", rec.Code, rec.Body.String())
	}
	saved := loadEditedModel(t, db, model.ID)
	if saved.ID != model.ID || !saved.IsDefault || !saved.UpdatedAt.Equal(model.UpdatedAt) {
		t.Fatalf("no-op modified catalog identity/state: %+v", saved)
	}
	rec = modelEditCall(t, model.ID, "user-1", `{"name":"changed-api-model"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("implicit catalog change=%d %s", rec.Code, rec.Body.String())
	}
	if saved = loadEditedModel(t, db, model.ID); saved.Name != model.Name || !saved.IsDefault || !saved.UpdatedAt.Equal(model.UpdatedAt) {
		t.Fatalf("rejected edit changed catalog: %+v", saved)
	}
}
