package modelprovider

import (
	"errors"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestLegacyActiveEmbeddingCannotBeRemovedJustBecauseCoreHasNoIndexedRows(t *testing.T) {
	db := setupListProviderTestDB(t)
	if err := db.AutoMigrate(&orm.EmbeddingIndexState{}, &orm.UserSelectedModel{}, &orm.Dataset{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	base := orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}
	provider := orm.UserModelProvider{ID: "provider", Name: "OpenAI", Category: "model", BaseModel: base}
	group := orm.UserModelProviderGroup{ID: "group", UserModelProviderID: provider.ID, BaseModel: base}
	model := orm.UserModelProviderGroupModel{ID: "legacy-active", UserModelProviderID: provider.ID, UserModelProviderGroupID: group.ID, Name: "embed", ModelType: "embed", Enabled: true, BaseModel: base}
	selected := orm.UserSelectedModel{UserID: "owner", ModelKey: "embed_main", UserModelProviderGroupModelID: model.ID, Share: true, CreatedAt: now, UpdatedAt: now}
	for _, row := range []any{&provider, &group, &model, &selected} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := guardEmbeddingRemoval(db, "owner", model.ID); !errors.Is(err, errEmbeddingInUse) {
		t.Fatalf("legacy active model may have orphaned vectors even without Core rows: %v", err)
	}
}

func TestEmbeddingCanBeRemovedAfterVerifiedOfflineClear(t *testing.T) {
	db := setupListProviderTestDB(t)
	if err := db.AutoMigrate(&orm.EmbeddingIndexState{}, &orm.UserSelectedModel{}, &orm.Dataset{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	base := orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}
	provider := orm.UserModelProvider{ID: "provider", Name: "OpenAI", Category: "model", BaseModel: base}
	group := orm.UserModelProviderGroup{ID: "group", UserModelProviderID: provider.ID, BaseModel: base}
	model := orm.UserModelProviderGroupModel{ID: "old-active", UserModelProviderID: provider.ID, UserModelProviderGroupID: group.ID, Name: "embed", ModelType: "embed", Enabled: true, BaseModel: base}
	selected := orm.UserSelectedModel{UserID: "owner", ModelKey: "embed_main", UserModelProviderGroupModelID: model.ID, Share: true, CreatedAt: now, UpdatedAt: now}
	state := orm.EmbeddingIndexState{Scope: "global", Phase: "cleared", Generation: 2, UpdatedAt: now}
	dataset := orm.Dataset{ID: "kb", KbID: "kb", ProcessingLevel: "indexed", BaseModel: base}
	for _, row := range []any{&provider, &group, &model, &selected, &state, &dataset} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := guardEmbeddingRemoval(db, "owner", model.ID); err != nil {
		t.Fatalf("verified clear removed old vectors and should release the model: %v", err)
	}
}
