package modelprovider

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestCustomSupplierDeleteChecksEmbeddingAfterTakingSupplierLock(t *testing.T) {
	db := setupListProviderTestDB(t)
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	p := orm.UserModelProvider{ID: "p", Name: "Proxy", ProtocolName: "OpenAI", IsCustom: true, Category: "model", Capabilities: "has_models", BaseModel: orm.BaseModel{CreateUserID: "user-1"}}
	g := orm.UserModelProviderGroup{ID: "g", UserModelProviderID: p.ID, Name: p.Name, BaseURL: "https://proxy.example/v1", BaseModel: p.BaseModel}
	for _, row := range []any{&p, &g} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	injected := false
	if err := db.Callback().Query().After("gorm:query").Register("test:late-embedding", func(tx *gorm.DB) {
		_, locked := tx.Statement.Clauses["FOR"]
		if injected || !locked || tx.Statement.Table != "user_model_providers" {
			return
		}
		injected = true
		late := orm.UserModelProviderGroupModel{ID: "late", UserModelProviderID: p.ID, UserModelProviderGroupID: g.ID, ProviderName: "OpenAI", Name: "late-embedding", ModelType: "embed", BaseModel: p.BaseModel}
		if err := tx.Session(&gorm.Session{NewDB: true, SkipHooks: true}).Create(&late).Error; err != nil {
			t.Error(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	DeleteCustomProvider(rec, customProviderRequest(http.MethodDelete, p.ID, ""))
	if !injected || rec.Code != http.StatusConflict {
		t.Fatalf("delete skipped newly visible embedding: injected=%v status=%d", injected, rec.Code)
	}
	var parent orm.UserModelProvider
	db.First(&parent, "id = ?", p.ID)
	if parent.DeletedAt != nil {
		t.Fatal("unconfirmed delete changed supplier")
	}
}

func TestModelCreateAndReviveRecheckDeletedSupplierUnderLock(t *testing.T) {
	for _, revive := range []bool{false, true} {
		name := "create"
		if revive {
			name = "revive"
		}
		t.Run(name, func(t *testing.T) {
			db := setupListProviderTestDB(t)
			store.Init(db, db, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			p := orm.UserModelProvider{ID: "p", Name: "Proxy", ProtocolName: "OpenAI", IsCustom: true, Category: "model", Capabilities: "has_models", BaseURL: "https://proxy.example/v1", BaseModel: orm.BaseModel{CreateUserID: "user-1"}}
			g := orm.UserModelProviderGroup{ID: "g", UserModelProviderID: p.ID, Name: p.Name, BaseURL: p.BaseURL, BaseModel: p.BaseModel}
			for _, row := range []any{&p, &g} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			if revive {
				now := time.Now()
				if err := db.Create(&orm.UserModelProviderGroupModel{ID: "old", UserModelProviderID: p.ID, UserModelProviderGroupID: g.ID, ProviderName: "OpenAI", Name: "new-model", ModelType: "llm", BaseModel: orm.BaseModel{CreateUserID: "user-1", DeletedAt: &now}}).Error; err != nil {
					t.Fatal(err)
				}
			}
			injected := false
			if err := db.Callback().Query().After("gorm:query").Register("test:delete-after-group-read", func(tx *gorm.DB) {
				if injected || tx.Statement.Table != "user_model_provider_groups" {
					return
				}
				injected = true
				now := time.Now()
				clean := db.Session(&gorm.Session{NewDB: true, SkipHooks: true})
				if err := clean.Model(&orm.UserModelProvider{}).Where("id = ?", p.ID).Update("deleted_at", now).Error; err != nil {
					t.Error(err)
				}
				if err := clean.Model(&orm.UserModelProviderGroup{}).Where("id = ?", g.ID).Update("deleted_at", now).Error; err != nil {
					t.Error(err)
				}
			}); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/models", strings.NewReader(`{"name":"new-model","model_type":"llm"}`))
			req.Header.Set("X-User-Id", "user-1")
			req = mux.SetURLVars(req, map[string]string{"model_provider_id": p.ID, "group_id": g.ID})
			rec := httptest.NewRecorder()
			AddGroupModel(rec, req)
			if !injected || rec.Code != http.StatusConflict {
				t.Fatalf("stale model write allowed: injected=%v status=%d body=%s", injected, rec.Code, rec.Body.String())
			}
			var n int64
			db.Model(&orm.UserModelProviderGroupModel{}).Where("deleted_at IS NULL").Count(&n)
			if n != 0 {
				t.Fatal("model became live under deleted supplier")
			}
		})
	}
}
