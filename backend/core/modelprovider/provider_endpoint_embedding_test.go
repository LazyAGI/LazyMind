package modelprovider

import (
	"errors"
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

var endpointTestFingerprint = "v1:1:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)

func TestEmbeddingEndpointChangeWithEmptyKnowledge(t *testing.T) {
	for _, tc := range []struct {
		name, phase, document string
		want                  int
	}{
		{"legacy selection without knowledge", "", "", 200},
		{"recorded identity without knowledge", "ready", "", 200},
		{"empty knowledge directory", "ready", "empty-directory", 200},
		{"nested empty folders", "ready", "folders", 200},
		{"deleted document", "ready", "deleted", 200},
		{"parsed document", "ready", "parsed", 409},
		{"indexed document", "ready", "indexed", 409},
		{"legacy indexed document", "", "indexed", 409},
		{"another user's document", "ready", "other-user", 409},
		{"cleanup in progress", "clearing", "", 409},
		{"incomplete cleanup", "cleanup_failed", "", 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, parent, group, model := setupEmbeddingEndpointTest(t, tc.phase)
			if tc.document != "" {
				level := tc.document
				if level != "parsed" {
					level = "indexed"
				}
				owner := parent.CreateUserID
				if tc.document == "other-user" {
					owner = "other-user"
				}
				kb := orm.Dataset{ID: "kb", KbID: "kb", ProcessingLevel: level, BaseModel: orm.BaseModel{CreateUserID: owner}}
				if err := db.Create(&kb).Error; err != nil {
					t.Fatal(err)
				}
				if tc.document == "folders" {
					for _, folder := range []orm.Document{
						{ID: "folder", DatasetID: kb.ID, DocumentType: "FOLDER"},
						{ID: "nested-folder", DatasetID: kb.ID, DocumentType: "FOLDER", PID: "folder"},
					} {
						if err := db.Create(&folder).Error; err != nil {
							t.Fatal(err)
						}
					}
				} else if tc.document != "empty-directory" {
					if err := db.Create(&orm.Document{ID: "doc", DatasetID: kb.ID}).Error; err != nil {
						t.Fatal(err)
					}
					if tc.document == "deleted" {
						if err := db.Delete(&orm.Document{}, "id = ?", "doc").Error; err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			r := httptest.NewRequest(http.MethodPut, "/endpoint", strings.NewReader(`{"base_url":"https://new.example/v1"}`))
			r.Header.Set("X-User-Id", parent.CreateUserID)
			r = mux.SetURLVars(r, map[string]string{"model_provider_id": parent.ID})
			w := httptest.NewRecorder()
			UpdateProviderEndpoint(w, r)
			if w.Code != tc.want {
				t.Fatalf("save endpoint: got %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			var saved orm.UserModelProvider
			if err := db.Take(&saved, "id = ?", parent.ID).Error; err != nil {
				t.Fatal(err)
			}
			var selected orm.UserSelectedModel
			if err := db.Take(&selected, "user_id = ? AND model_type = ?", parent.CreateUserID, "embed_main").Error; err != nil {
				t.Fatal(err)
			}
			if selected.UserModelProviderGroupModelID != model.ID {
				t.Fatal("saving endpoint lost the selected model")
			}
			var state orm.EmbeddingIndexState
			stateErr := db.Take(&state, "scope = ?", globalEmbeddingScope).Error
			if tc.want == http.StatusOK {
				generation := int64(8)
				if tc.phase == "" {
					generation = 1
				}
				if stateErr != nil || state.Signature != "" || state.ActiveModelID != model.ID || state.Generation != generation {
					t.Fatalf("old runtime identity not invalidated: %+v err=%v", state, stateErr)
				}
				choice, present, err := ResolveGlobalEmbeddingChoice(t.Context(), db)
				if err != nil || !present || choice.Phase != "unverified" {
					t.Fatalf("new endpoint reused old verification: %+v %v", choice, err)
				}
				if normalizeBaseURLForCompare(saved.BaseURL) != "https://new.example/v1" {
					t.Fatal("endpoint was not saved")
				}
				assertStoredEncryptedAPIKeys(t, db, group.ID, "endpoint-test-key", false)
			} else {
				if saved.BaseURL != parent.BaseURL {
					t.Fatal("rejected endpoint was persisted")
				}
				if tc.phase != "" && (stateErr != nil || state.Generation != 7 || state.Signature != endpointTestFingerprint || state.Phase != tc.phase) {
					t.Fatalf("rejected endpoint changed index state: %+v %v", state, stateErr)
				}
				if tc.phase == "" && !errors.Is(stateErr, gorm.ErrRecordNotFound) {
					t.Fatal("rejected legacy endpoint created an index state")
				}
			}
		})
	}
}

func TestEmbeddingEndpointNoopAndFailedSavePreserveIdentity(t *testing.T) {
	for _, name := range []string{"unchanged", "failed"} {
		t.Run(name, func(t *testing.T) {
			fail := name == "failed"
			db, parent, group, _ := setupEmbeddingEndpointTest(t, "ready")
			endpoint := parent.BaseURL
			writeErr := errors.New("endpoint write failed")
			if fail {
				endpoint = "https://new.example/v1"
				const callback = "test:fail-endpoint-write"
				if err := db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == "user_model_providers" {
						tx.AddError(writeErr)
					}
				}); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { db.Callback().Update().Remove(callback) })
			}
			err := setProviderEndpoint(t.Context(), db, &parent, endpoint, time.Now())
			if (fail && !errors.Is(err, writeErr)) || (!fail && err != nil) {
				t.Fatalf("save error: %v", err)
			}
			var state orm.EmbeddingIndexState
			if err := db.Take(&state, "scope = ?", globalEmbeddingScope).Error; err != nil {
				t.Fatal(err)
			}
			if state.Signature != endpointTestFingerprint || state.Generation != 7 {
				t.Fatalf("no-op/failed save invalidated identity: %+v", state)
			}
			var connection orm.UserModelProviderGroup
			if err := db.Take(&connection, "id = ?", group.ID).Error; err != nil {
				t.Fatal(err)
			}
			if connection.BaseURL != group.BaseURL || connection.CredentialRevision != group.CredentialRevision {
				t.Fatal("no-op/failed save changed credentials")
			}
		})
	}
}

func TestEmbeddingEndpointDoesNotInvalidateAnotherLegacyChoice(t *testing.T) {
	db, parent, _, _ := setupEmbeddingEndpointTest(t, "")
	owner := "other-owner"
	otherProvider := orm.UserModelProvider{ID: "other-provider", BaseModel: orm.BaseModel{CreateUserID: owner}}
	otherGroup := orm.UserModelProviderGroup{ID: "other-group", UserModelProviderID: otherProvider.ID, BaseModel: orm.BaseModel{CreateUserID: owner}}
	otherModel := orm.UserModelProviderGroupModel{ID: "other-model", UserModelProviderID: otherProvider.ID, UserModelProviderGroupID: otherGroup.ID, ModelType: "embed", Enabled: true, BaseModel: orm.BaseModel{CreateUserID: owner}}
	otherSelection := orm.UserSelectedModel{UserID: owner, ModelKey: "embed_main", UserModelProviderGroupModelID: otherModel.ID, UpdatedAt: time.Now().Add(time.Hour)}
	for _, row := range []any{&otherProvider, &otherGroup, &otherModel, &otherSelection} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	before, present, err := ResolveGlobalEmbeddingChoice(t.Context(), db)
	if err != nil || !present || before.ModelID != otherModel.ID {
		t.Fatalf("unexpected global choice: %+v %v", before, err)
	}
	if err := setProviderEndpoint(t.Context(), db, &parent, "https://new.example/v1", time.Now()); err != nil {
		t.Fatal(err)
	}
	after, present, err := ResolveGlobalEmbeddingChoice(t.Context(), db)
	if err != nil || !present || after != before {
		t.Fatalf("unrelated global choice changed: before=%+v after=%+v err=%v", before, after, err)
	}
}

func setupEmbeddingEndpointTest(t *testing.T, phase string) (*gorm.DB, orm.UserModelProvider, orm.UserModelProviderGroup, orm.UserModelProviderGroupModel) {
	t.Helper()
	db, parent, group := setupEncryptedGroupKeyTest(t, "endpoint-test-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := db.AutoMigrate(&orm.Dataset{}, &orm.Document{}, &orm.DocumentProcessingState{}, &orm.EmbeddingIndexState{}, &orm.UserSelectedModel{}, &orm.UserModelProviderGroupModel{}); err != nil {
		t.Fatal(err)
	}
	model := orm.UserModelProviderGroupModel{ID: "selected-embed", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, Name: "test-embed", ModelType: "embed", Enabled: true, BaseModel: orm.BaseModel{CreateUserID: parent.CreateUserID}}
	selection := orm.UserSelectedModel{UserID: parent.CreateUserID, ModelKey: "embed_main", UserModelProviderGroupModelID: model.ID}
	for _, row := range []any{&model, &selection} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if phase != "" {
		state := orm.EmbeddingIndexState{Scope: globalEmbeddingScope, ActiveModelID: model.ID, ActiveSource: "own", Signature: endpointTestFingerprint, Phase: phase, Generation: 7, UpdatedAt: time.Now()}
		if err := db.Create(&state).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db, parent, group, model
}
