package modelprovider

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func TestDefaultSelectionRevalidatesAfterConcurrentModelEdit(t *testing.T) {
	db, model := modelEditFixture(t, "llm", false)
	if err := db.AutoMigrate(&orm.UserSelectedModel{}, &orm.UserSelectedCloudModel{}); err != nil {
		t.Fatal(err)
	}
	read, edited := make(chan struct{}), make(chan struct{})
	var intercepted atomic.Bool
	if err := db.Callback().Query().After("gorm:query").Register("test:pause-default-inventory", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*[]orm.UserModelProviderGroupModel); !ok || !intercepted.CompareAndSwap(false, true) {
			return
		}
		close(read)
		select {
		case <-edited:
		case <-time.After(5 * time.Second):
			t.Error("concurrent metadata edit did not finish")
		}
	}); err != nil {
		t.Fatal(err)
	}
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPut, "/selected", strings.NewReader(`{"selections":[{"model_key":"llm","model_id":"editable"}]}`))
		req.Header.Set("X-User-Id", "user-1")
		rec := httptest.NewRecorder()
		SetSelectedModels(rec, req)
		finished <- rec
	}()
	select {
	case <-read:
	case <-time.After(5 * time.Second):
		t.Fatal("selection never read original model inventory")
	}
	edit := modelEditCall(t, model.ID, "user-1", `{"model_type":"embed"}`)
	close(edited)
	if edit.Code != http.StatusOK {
		t.Fatalf("interleaved edit=%d %s", edit.Code, edit.Body.String())
	}
	select {
	case rec := <-finished:
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("stale default inventory accepted after committed edit: %d %s", rec.Code, rec.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("selection did not resume")
	}
	var count int64
	if err := db.Model(&orm.UserSelectedModel{}).Where("user_model_provider_group_model_id = ?", model.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("concurrent selection persisted incompatible model role")
	}
}

func TestDefaultSelectionAcquiresSupplierLockBeforeSaving(t *testing.T) {
	db, model := modelEditFixture(t, "llm", false)
	if err := db.AutoMigrate(&orm.UserSelectedModel{}, &orm.UserSelectedCloudModel{}); err != nil {
		t.Fatal(err)
	}
	var locked atomic.Bool
	if err := db.Callback().Query().After("gorm:query").Register("test:track-selection-supplier-lock", func(tx *gorm.DB) {
		if tx.Statement.Table == "user_model_providers" {
			if _, ok := tx.Statement.Clauses["FOR"]; ok {
				locked.Store(true)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Create().Before("gorm:create").Register("test:require-selection-supplier-lock", func(tx *gorm.DB) {
		if tx.Statement.Table == "user_selected_models" && !locked.Load() {
			t.Error("default reference saved without metadata edit lock")
		}
	}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/selected", strings.NewReader(`{"selections":[{"model_key":"llm","model_id":"`+model.ID+`"}]}`))
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	SetSelectedModels(rec, req)
	if rec.Code != 200 {
		t.Fatalf("default selection=%d %s", rec.Code, rec.Body.String())
	}
	if !locked.Load() {
		t.Fatal("supplier was never locked")
	}
}

func TestMultimodalDeletionClassificationIncludesPersistedAliases(t *testing.T) {
	for _, kind := range []string{"embed_image", "multimodal_embedding", "cross_modal_embed", " MULTIMODAL_EMBEDDING "} {
		if !isMultimodalEmbeddingModelType(kind) {
			t.Errorf("multimodal deletion would not reset lazy mode for %q", kind)
		}
	}
	if isMultimodalEmbeddingModelType("embed") {
		t.Fatal("text embedding must keep its own index downgrade path")
	}
}
