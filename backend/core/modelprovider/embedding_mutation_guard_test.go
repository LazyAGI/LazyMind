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
	"lazymind/core/credentialvault"
	"lazymind/core/store"
)

func TestEmbeddingRuntimeMutationProtectsOnlyActiveModel(t *testing.T) {
	db := setupListProviderTestDB(t)
	if err := db.AutoMigrate(&orm.Dataset{}, &orm.EmbeddingIndexState{}, &orm.UserSelectedModel{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	active := orm.UserModelProviderGroupModel{ID: "active", UserModelProviderID: "provider-a", UserModelProviderGroupID: "group-a", ModelType: "embed", BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}
	unused := orm.UserModelProviderGroupModel{ID: "unused", UserModelProviderID: "provider-b", UserModelProviderGroupID: "group-b", ModelType: "embed", BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}
	indexed := orm.Dataset{ID: "kb", KbID: "kb", ProcessingLevel: "indexed", BaseModel: orm.BaseModel{CreateUserID: "other-user", CreatedAt: now, UpdatedAt: now}}
	state := orm.EmbeddingIndexState{Scope: globalEmbeddingScope, ActiveModelID: active.ID, Signature: "v1:fingerprint", Phase: "ready", Generation: 5, UpdatedAt: now}
	stalePersonal := orm.UserSelectedModel{UserID: "other-user", ModelKey: "embed_main", UserModelProviderGroupModelID: unused.ID, CreatedAt: now, UpdatedAt: now}
	for _, row := range []any{&active, &unused, &indexed, &state, &stalePersonal} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := guardEmbeddingRuntimeMutation(db, "owner", unused.UserModelProviderID, "", ""); err != nil {
		t.Fatalf("unused candidate should be editable: %v", err)
	}
	if err := guardEmbeddingRuntimeMutation(db, "owner", active.UserModelProviderID, "", ""); !errors.Is(err, errEmbeddingIdentityInUse) {
		t.Fatalf("active model must be protected: %v", err)
	}
	var after orm.EmbeddingIndexState
	if err := db.Take(&after, "scope = ?", globalEmbeddingScope).Error; err != nil {
		t.Fatal(err)
	}
	if after.Signature != state.Signature || after.Generation != state.Generation {
		t.Fatalf("rejected edit altered index identity: %+v", after)
	}
}

func TestActiveEmbeddingSupplierEndpointAndKeyCannotChangeWithIndex(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "first-key\nsecond-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := db.AutoMigrate(&orm.UserModelProviderGroupModel{}, &orm.Dataset{}, &orm.EmbeddingIndexState{}); err != nil {
		t.Fatal(err)
	}
	if err := prepareProviderKeySelection(t.Context(), db, &parent); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	model := orm.UserModelProviderGroupModel{ID: "active-embed", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ProviderName: parent.Name, Name: "text-embedding-v1", ModelType: "embed", BaseModel: orm.BaseModel{CreateUserID: parent.CreateUserID, CreatedAt: now, UpdatedAt: now}}
	for _, row := range []any{
		&model,
		&orm.Dataset{ID: "other-kb", KbID: "other-kb", ProcessingLevel: "indexed", BaseModel: orm.BaseModel{CreateUserID: "another-user", CreatedAt: now, UpdatedAt: now}},
		&orm.EmbeddingIndexState{Scope: globalEmbeddingScope, ActiveModelID: model.ID, Signature: "v1:fingerprint", Phase: "ready", Generation: 1, UpdatedAt: now},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	endpointReq := httptest.NewRequest(http.MethodPut, "/endpoint", strings.NewReader(`{"base_url":"https://new.example/v1"}`))
	endpointReq.Header.Set("X-User-Id", parent.CreateUserID)
	endpointReq = mux.SetURLVars(endpointReq, map[string]string{"model_provider_id": parent.ID})
	endpointRec := httptest.NewRecorder()
	UpdateProviderEndpoint(endpointRec, endpointReq)
	if endpointRec.Code != http.StatusConflict {
		t.Fatalf("active supplier endpoint change=%d %s", endpointRec.Code, endpointRec.Body.String())
	}
	keyReq := httptest.NewRequest(http.MethodPut, "/keys/active", strings.NewReader(`{"group_id":"`+group.ID+`","key_id":"`+groupKeyID(group.ID, "second-key")+`"}`))
	keyReq.Header.Set("X-User-Id", parent.CreateUserID)
	keyReq = mux.SetURLVars(keyReq, map[string]string{"model_provider_id": parent.ID})
	keyRec := httptest.NewRecorder()
	SetActiveProviderKey(keyRec, keyReq)
	if keyRec.Code != http.StatusConflict {
		t.Fatalf("active supplier key change=%d %s", keyRec.Code, keyRec.Body.String())
	}
	requestConfigRec := httptest.NewRecorder()
	SetProviderRequestConfig(requestConfigRec, requestConfigRequest(http.MethodPut, parent.ID, parent.CreateUserID, `{"default_protocol":"openai","advanced":{"extra_body":{"test":"changed"}}}`))
	if requestConfigRec.Code != http.StatusConflict {
		t.Fatalf("active supplier request config change=%d %s", requestConfigRec.Code, requestConfigRec.Body.String())
	}
	var saved orm.UserModelProvider
	if err := db.Take(&saved, "id = ?", parent.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.BaseURL != parent.BaseURL || saved.ActiveKeyID != groupKeyID(group.ID, "first-key") {
		t.Fatalf("rejected supplier change was persisted: %+v", saved)
	}
}

func TestUnusedEmbeddingSupplierEndpointCanChangeWithIndex(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "candidate-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := db.AutoMigrate(&orm.UserModelProviderGroupModel{}, &orm.Dataset{}, &orm.EmbeddingIndexState{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	active := orm.UserModelProviderGroupModel{ID: "active-elsewhere", UserModelProviderID: "other-provider", UserModelProviderGroupID: "other-group", ModelType: "embed", BaseModel: orm.BaseModel{CreateUserID: "other-user", CreatedAt: now, UpdatedAt: now}}
	candidate := orm.UserModelProviderGroupModel{ID: "unused-candidate", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ModelType: "embed", BaseModel: orm.BaseModel{CreateUserID: parent.CreateUserID, CreatedAt: now, UpdatedAt: now}}
	for _, row := range []any{
		&active, &candidate,
		&orm.Dataset{ID: "indexed", KbID: "indexed", ProcessingLevel: "indexed", BaseModel: orm.BaseModel{CreateUserID: "other-user", CreatedAt: now, UpdatedAt: now}},
		&orm.EmbeddingIndexState{Scope: globalEmbeddingScope, ActiveModelID: active.ID, Signature: "v1:fingerprint", Phase: "ready", Generation: 1, UpdatedAt: now},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodPut, "/endpoint", strings.NewReader(`{"base_url":"https://candidate-new.example/v1"}`))
	req.Header.Set("X-User-Id", parent.CreateUserID)
	req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID})
	rec := httptest.NewRecorder()
	UpdateProviderEndpoint(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unused candidate supplier update=%d %s", rec.Code, rec.Body.String())
	}
}

func TestLegacyImplicitFirstKeyInitializationDoesNotChangeEmbeddingIdentity(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "legacy-first\nlegacy-second")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := db.AutoMigrate(&orm.UserModelProviderGroupModel{}, &orm.Dataset{}, &orm.EmbeddingIndexState{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	model := orm.UserModelProviderGroupModel{ID: "legacy-embed", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ModelType: "embed", BaseModel: orm.BaseModel{CreateUserID: parent.CreateUserID, CreatedAt: now, UpdatedAt: now}}
	for _, row := range []any{
		&model,
		&orm.Dataset{ID: "indexed", KbID: "indexed", ProcessingLevel: "indexed", BaseModel: orm.BaseModel{CreateUserID: parent.CreateUserID, CreatedAt: now, UpdatedAt: now}},
		&orm.EmbeddingIndexState{Scope: globalEmbeddingScope, ActiveModelID: model.ID, Signature: "v1:fingerprint", Phase: "ready", Generation: 7, UpdatedAt: now},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := prepareProviderKeySelection(t.Context(), db, &parent); err != nil {
		t.Fatalf("persisting existing implicit first key should remain available: %v", err)
	}
	if parent.ActiveKeyID != groupKeyID(group.ID, "legacy-first") {
		t.Fatalf("legacy first key changed: %+v", parent)
	}
	var state orm.EmbeddingIndexState
	if err := db.Take(&state, "scope = ?", globalEmbeddingScope).Error; err != nil {
		t.Fatal(err)
	}
	if state.Signature != "v1:fingerprint" || state.Generation != 7 {
		t.Fatalf("metadata initialization invalidated embedding identity: %+v", state)
	}
}

func TestAddingFirstKeyToActiveEmbeddingSupplierRequiresVectorCleanup(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := db.AutoMigrate(&orm.UserModelProviderGroupModel{}, &orm.Dataset{}, &orm.EmbeddingIndexState{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	model := orm.UserModelProviderGroupModel{ID: "keyless-embed", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ModelType: "embed", BaseModel: orm.BaseModel{CreateUserID: parent.CreateUserID, CreatedAt: now, UpdatedAt: now}}
	for _, row := range []any{
		&model,
		&orm.Dataset{ID: "indexed", KbID: "indexed", ProcessingLevel: "indexed", BaseModel: orm.BaseModel{CreateUserID: parent.CreateUserID, CreatedAt: now, UpdatedAt: now}},
		&orm.EmbeddingIndexState{Scope: globalEmbeddingScope, ActiveModelID: model.ID, Signature: "v1:fingerprint", Phase: "ready", Generation: 7, UpdatedAt: now},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/keys", strings.NewReader(`{"api_key":"new-key"}`))
	req.Header.Set("X-User-Id", parent.CreateUserID)
	req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
	rec := httptest.NewRecorder()
	AddKey(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("adding first key to active embedding supplier=%d %s", rec.Code, rec.Body.String())
	}
}

func TestCatalogServiceRebindKeepsActiveEmbeddingAdapter(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.DefaultModelProvider{}, &orm.UserModelProvider{}, &orm.UserModelProviderGroupModel{}, &orm.Dataset{}, &orm.EmbeddingIndexState{}).DB
	now := time.Now().UTC()
	defs := []orm.DefaultModelProvider{
		{ID: "standard", Name: "Qwen", Category: "model", CreatedAt: now, UpdatedAt: now},
		{ID: "coding", Name: "QwenCodingPlan", Category: "model", CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&defs).Error; err != nil {
		t.Fatal(err)
	}
	base := orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}
	provider := orm.UserModelProvider{ID: "legacy", DefaultModelProviderID: "standard", Name: "Qwen", ProtocolName: "Qwen", BaseURL: "https://coding.dashscope.aliyuncs.com/v1/", Category: "model", BaseModel: base}
	model := orm.UserModelProviderGroupModel{ID: "embed", UserModelProviderID: provider.ID, UserModelProviderGroupID: "group", Name: "embed-model", ModelType: "embed", ProviderName: "Qwen", BaseModel: base}
	for _, row := range []any{
		&provider, &model,
		&orm.Dataset{ID: "kb", KbID: "kb", ProcessingLevel: "indexed", BaseModel: base},
		&orm.EmbeddingIndexState{Scope: globalEmbeddingScope, ActiveModelID: model.ID, Signature: "v1:fingerprint", Phase: "ready", Generation: 1, UpdatedAt: now},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return rebindLegacyServiceProviders(tx, []orm.UserModelProvider{provider}, defs, now)
	}); err != nil {
		t.Fatal(err)
	}
	var after orm.UserModelProvider
	if err := db.Take(&after, "id = ?", provider.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.Name != "Qwen" || after.ProtocolName != "Qwen" || after.DefaultModelProviderID != "standard" {
		t.Fatalf("catalog refresh changed active embedding adapter: %+v", after)
	}
}

func TestCatalogRefreshPreservesActiveEmbeddingTechnicalType(t *testing.T) {
	db := setupListProviderTestDB(t)
	if err := db.AutoMigrate(&orm.Dataset{}, &orm.EmbeddingIndexState{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	base := orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}
	catalog := orm.DefaultModelProvider{ID: "catalog", Name: "OpenAI", Category: "model", CreatedAt: now, UpdatedAt: now}
	provider := orm.UserModelProvider{ID: "provider", DefaultModelProviderID: catalog.ID, Name: "OpenAI", Category: "model", BaseModel: base}
	group := orm.UserModelProviderGroup{ID: "group", UserModelProviderID: provider.ID, BaseModel: base}
	model := orm.UserModelProviderGroupModel{ID: "embed", UserModelProviderID: provider.ID, UserModelProviderGroupID: group.ID, Name: "embedding-model", ModelType: "embed", IsDefault: true, BaseModel: base}
	for _, row := range []any{
		&catalog, &provider, &group, &model,
		&orm.Dataset{ID: "kb", KbID: "kb", ProcessingLevel: "indexed", BaseModel: base},
		&orm.EmbeddingIndexState{Scope: globalEmbeddingScope, ActiveModelID: model.ID, Signature: "v1:fingerprint", Phase: "ready", Generation: 1, UpdatedAt: now},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return syncDefaultModelToUserGroups(tx, now, catalog.ID, catalog.Name, model.Name, "llm", nil, 0, "", false)
	}); err != nil {
		t.Fatal(err)
	}
	var saved orm.UserModelProviderGroupModel
	if err := db.Take(&saved, "id = ?", model.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.ModelType != "embed" {
		t.Fatalf("catalog refresh reclassified active index model: %+v", saved)
	}
}

func TestCredentialRestoreCannotReplaceActiveEmbeddingKeyPool(t *testing.T) {
	sink, db, scope := newCredentialRestoreSinkFixture(t)
	if err := db.AutoMigrate(&orm.UserModelProviderGroupModel{}, &orm.Dataset{}, &orm.EmbeddingIndexState{}); err != nil {
		t.Fatal(err)
	}
	restored := customRestorePayload("00000000-0000-7000-8000-000000000401", "Proxy A", "https://a.example/v1", "key-a")
	if err := sink.PersistTrusted(t.Context(), scope, []credentialvault.RestoredCredential{restored}, map[string]credentialvault.ConflictResolution{restored.RecordID: credentialvault.ConflictFail}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var provider orm.UserModelProvider
	if err := db.Where("is_custom = ?", true).Take(&provider).Error; err != nil {
		t.Fatal(err)
	}
	var group orm.UserModelProviderGroup
	if err := db.Where("user_model_provider_id = ?", provider.ID).Take(&group).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	model := orm.UserModelProviderGroupModel{ID: "active-embed", UserModelProviderID: provider.ID, UserModelProviderGroupID: group.ID, ModelType: "embed", BaseModel: orm.BaseModel{CreateUserID: provider.CreateUserID, CreatedAt: now, UpdatedAt: now}}
	for _, row := range []any{
		&model,
		&orm.Dataset{ID: "kb", KbID: "kb", ProcessingLevel: "indexed", BaseModel: orm.BaseModel{CreateUserID: provider.CreateUserID, CreatedAt: now, UpdatedAt: now}},
		&orm.EmbeddingIndexState{Scope: globalEmbeddingScope, ActiveModelID: model.ID, Signature: "v1:fingerprint", Phase: "ready", Generation: 1, UpdatedAt: now},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	restored.Revision++
	restored.Provider.APIKey = []byte("key-b")
	if err := sink.PersistTrusted(t.Context(), scope, []credentialvault.RestoredCredential{restored}, map[string]credentialvault.ConflictResolution{restored.RecordID: credentialvault.ConflictReplaceLocal}, time.Now().UTC()); !errors.Is(err, errEmbeddingIdentityInUse) {
		t.Fatalf("active embedding key restore should be rejected: %v", err)
	}
	var after orm.UserModelProviderGroup
	if err := db.Take(&after, "id = ?", group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if key, err := apiKeyForGroup(db, &after); err != nil || key != "key-a" {
		t.Fatalf("rejected restore changed key pool: key=%q err=%v", key, err)
	}
}

func TestEmbeddingRuntimeMutationProtectsRecordedIdentityEvenWhenCoreIndexEmpty(t *testing.T) {
	db := setupListProviderTestDB(t)
	if err := db.AutoMigrate(&orm.Dataset{}, &orm.EmbeddingIndexState{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	model := orm.UserModelProviderGroupModel{ID: "active", UserModelProviderID: "provider-a", UserModelProviderGroupID: "group-a", ModelType: "embed", BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}
	state := orm.EmbeddingIndexState{Scope: globalEmbeddingScope, ActiveModelID: model.ID, Signature: "v1:fingerprint", Phase: "ready", Generation: 5, UpdatedAt: now}
	for _, row := range []any{&model, &state} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := guardEmbeddingRuntimeMutation(db, "owner", model.UserModelProviderID, "", ""); !errors.Is(err, errEmbeddingIdentityInUse) {
		t.Fatalf("persisted active identity must be protected despite empty Core count: %v", err)
	}
	var after orm.EmbeddingIndexState
	if err := db.Take(&after, "scope = ?", globalEmbeddingScope).Error; err != nil {
		t.Fatal(err)
	}
	if after.Signature != state.Signature || after.Generation != state.Generation || after.ActiveModelID != model.ID {
		t.Fatalf("rejected runtime edit changed active identity: %+v", after)
	}
}

func TestModelEditorAllowsUnusedEmbeddingCandidateWithGlobalIndex(t *testing.T) {
	db, active := modelEditFixture(t, "embed", false)
	if err := db.AutoMigrate(&orm.Dataset{}, &orm.EmbeddingIndexState{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	unused := active
	unused.ID = "unused-embedding"
	unused.Name = "candidate-original"
	if err := db.Create(&unused).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&orm.Dataset{ID: "other-user-kb", KbID: "other-user-kb", ProcessingLevel: "indexed", BaseModel: orm.BaseModel{CreateUserID: "other-user", CreatedAt: now, UpdatedAt: now}},
		&orm.EmbeddingIndexState{Scope: globalEmbeddingScope, ActiveModelID: active.ID, Signature: "v1:fingerprint", Phase: "ready", Generation: 1, UpdatedAt: now},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if rec := modelEditCall(t, unused.ID, "user-1", `{"name":"candidate-updated"}`); rec.Code != 200 {
		t.Fatalf("unused embedding edit=%d %s", rec.Code, rec.Body.String())
	}
	if rec := modelEditCall(t, active.ID, "user-1", `{"name":"active-updated"}`); rec.Code != 409 {
		t.Fatalf("active embedding edit=%d %s", rec.Code, rec.Body.String())
	}
	if loadEditedModel(t, db, active.ID).Name != active.Name {
		t.Fatal("active embedding changed despite indexed knowledge bases")
	}
}
