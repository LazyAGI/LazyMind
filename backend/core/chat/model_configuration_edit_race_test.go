package chat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/modelprovider"
	"lazymind/core/store"
)

type editRaceCatalog func(context.Context) (modelprovider.CloudModelCatalog, error)

func (f editRaceCatalog) CloudModelCatalog(ctx context.Context) (modelprovider.CloudModelCatalog, error) {
	return f(ctx)
}

// SQLite's read-uncommitted connection permits the deliberate two-request
// interleaving used here. The production guarantee additionally uses supplier
// row locks; this regression specifically detects committing an old inventory
// after a second request has already changed the model's technical type.
func TestFixedBindingsRevalidateAfterConcurrentModelConfigurationEdit(t *testing.T) {
	for _, mode := range []string{"patch", "create"} {
		t.Run(mode, func(t *testing.T) {
			var database *orm.DB
			if strings.EqualFold(strings.TrimSpace(os.Getenv("TEST_DB_DRIVER")), "postgres") {
				database = newChatModelTestDB(t)
			} else {
				// All connections must share a read-uncommitted cache. Merely
				// setting the PRAGMA on one pooled connection keeps a stale WAL
				// snapshot and can mask missing revalidation with SQLITE_BUSY.
				var err error
				database, err = orm.Connect(orm.DriverSQLite, "file:"+filepath.Join(t.TempDir(), "race.db")+"?cache=shared&_pragma=read_uncommitted(1)")
				if err != nil {
					t.Fatal(err)
				}
				sqlDB, err := database.DB.DB()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = sqlDB.Close() })
				if err := database.AutoMigrate(orm.AllModelsForDDL()...); err != nil {
					t.Fatal(err)
				}
			}
			db := database.DB
			store.Init(db, db, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			model := seedAvailableChatModel(t, db, "user-1", "provider-race", "group-race", "model-race", "OpenAI", "OpenAI", "model-race", "llm", true, "synthetic-key")
			if mode == "patch" {
				conversation := orm.Conversation{ID: "conversation-race", ChatModelVersion: 1, BaseModel: orm.BaseModel{CreateUserID: "user-1"}}
				if err := db.Create(&conversation).Error; err != nil {
					t.Fatal(err)
				}
			}
			inventory, edited := make(chan struct{}), make(chan struct{})
			var paused atomic.Bool
			modelprovider.SetCloudCatalogProvider(editRaceCatalog(func(context.Context) (modelprovider.CloudModelCatalog, error) {
				if paused.CompareAndSwap(false, true) {
					close(inventory)
					select {
					case <-edited:
					case <-time.After(5 * time.Second):
						t.Error("metadata editor did not finish")
					}
				}
				return modelprovider.CloudModelCatalog{}, nil
			}))
			t.Cleanup(func() { modelprovider.SetCloudCatalogProvider(nil) })
			result := make(chan error, 1)
			response := make(chan int, 1)
			go func() {
				if mode == "create" {
					err := db.Transaction(func(tx *gorm.DB) error {
						_, _, err := ensureConversation(context.Background(), tx, "conversation-race", "", nil, nil, "user-1", "User", false, "", map[string]any{}, &initialChatModelSelection{Mode: chatModelModeFixed, ModelID: model.ID, Source: "own"})
						return err
					})
					result <- err
					return
				}
				req := httptest.NewRequest(http.MethodPatch, "/conversation/model", strings.NewReader(`{"mode":"fixed","model_id":"model-race","source":"own","expected_version":1}`))
				req.Header.Set("X-User-Id", "user-1")
				req = mux.SetURLVars(req, map[string]string{"conversation_id": "conversation-race"})
				rec := httptest.NewRecorder()
				PatchConversationModel(rec, req)
				response <- rec.Code
			}()
			select {
			case <-inventory:
			case <-time.After(5 * time.Second):
				t.Fatal("conversation request never loaded model inventory")
			}
			req := httptest.NewRequest(http.MethodPatch, "/model", strings.NewReader(`{"model_type":"embed"}`))
			req.Header.Set("X-User-Id", "user-1")
			req = mux.SetURLVars(req, map[string]string{"model_provider_id": model.UserModelProviderID, "group_id": model.UserModelProviderGroupID, "model_id": model.ID})
			edit := httptest.NewRecorder()
			modelprovider.UpdateGroupModel(edit, req)
			close(edited)
			if edit.Code != http.StatusOK {
				t.Fatalf("concurrent edit=%d %s", edit.Code, edit.Body.String())
			}
			if mode == "create" {
				select {
				case err := <-result:
					if !errors.Is(err, errChatModelUnavailable) {
						t.Fatalf("new conversation must revalidate the edited model, got %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("conversation creation did not resume")
				}
			} else {
				select {
				case status := <-response:
					if status != http.StatusServiceUnavailable {
						t.Fatalf("conversation patch must reject the edited model as unavailable, got %d", status)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("conversation patch did not resume")
				}
			}
			var count int64
			if err := db.Model(&orm.Conversation{}).Where("chat_model_id = ?", model.ID).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatal("persisted incompatible fixed conversation reference")
			}
		})
	}
}
