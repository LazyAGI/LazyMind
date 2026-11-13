package modelprovider

import (
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

const validTestEmbeddingFingerprint = "v1:1536:" +
	"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:" +
	"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func requireEmbeddingDestructiveConflict(t *testing.T, err, sentinel error, wantReason string) {
	t.Helper()
	if !errors.Is(err, sentinel) {
		t.Fatalf("conflict = %v, want errors.Is(%v)", err, sentinel)
	}
	var conflict *embeddingDestructiveOperationError
	if !errors.As(err, &conflict) {
		t.Fatalf("conflict = %T %v, want typed embedding conflict", err, err)
	}
	if conflict.Reason != wantReason {
		t.Fatalf("reason = %q, want %q", conflict.Reason, wantReason)
	}
}

func setupEmbeddingDestructiveGuardTest(t *testing.T) (*gorm.DB, orm.UserModelProviderGroupModel, orm.UserModelProviderGroupModel) {
	t.Helper()
	db := setupListProviderTestDB(t)
	if err := db.AutoMigrate(&orm.EmbeddingIndexState{}, &orm.UserSelectedModel{}, &orm.UserSelectedCloudModel{}, &orm.Dataset{}, &orm.DocumentProcessingState{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	target := orm.UserModelProviderGroupModel{
		ID: "target", UserModelProviderID: "provider-target", UserModelProviderGroupID: "group-target",
		Name: "target-embed", ModelType: "embed", IsDefault: true,
		BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now},
	}
	other := orm.UserModelProviderGroupModel{
		ID: "other", UserModelProviderID: "provider-other", UserModelProviderGroupID: "group-other",
		Name: "other-embed", ModelType: "embed", IsDefault: true,
		BaseModel: orm.BaseModel{CreateUserID: "other-owner", CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&[]orm.UserModelProviderGroupModel{target, other}).Error; err != nil {
		t.Fatal(err)
	}
	return db, target, other
}

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

func TestEmbeddingRemovalUsesIndexEvidenceInsteadOfDefaultMetadata(t *testing.T) {
	t.Run("valid signed owner only", func(t *testing.T) {
		db, target, other := setupEmbeddingDestructiveGuardTest(t)
		state := orm.EmbeddingIndexState{
			Scope: globalEmbeddingScope, ActiveModelID: target.ID, ActiveSource: "own",
			ActiveModelName: target.Name, ActiveProviderName: "Provider", Signature: validTestEmbeddingFingerprint,
			Phase: "ready", Generation: 1, UpdatedAt: time.Now().UTC(),
		}
		if err := db.Create(&state).Error; err != nil {
			t.Fatal(err)
		}
		if err := guardEmbeddingRemoval(db, "other-owner", other.ID); err != nil {
			t.Fatalf("unrelated default model should be removable: %v", err)
		}
		requireEmbeddingDestructiveConflict(t, guardEmbeddingRemoval(db, "owner", target.ID), errEmbeddingInUse, "active_index_owner")
	})

	t.Run("empty state releases default", func(t *testing.T) {
		db, target, _ := setupEmbeddingDestructiveGuardTest(t)
		if err := db.Create(&orm.EmbeddingIndexState{Scope: globalEmbeddingScope, Phase: "ready", Generation: 1, UpdatedAt: time.Now().UTC()}).Error; err != nil {
			t.Fatal(err)
		}
		if err := guardEmbeddingRemoval(db, "owner", target.ID); err != nil {
			t.Fatalf("empty index should not pin a default model: %v", err)
		}
	})

	t.Run("strict clear releases default despite historical rows", func(t *testing.T) {
		db, target, _ := setupEmbeddingDestructiveGuardTest(t)
		now := time.Now().UTC()
		for _, row := range []any{
			&orm.EmbeddingIndexState{Scope: globalEmbeddingScope, Phase: "cleared", Generation: 2, UpdatedAt: now},
			&orm.Dataset{ID: "pending-reindex", KbID: "pending-reindex", ProcessingLevel: "indexed", BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}},
		} {
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := guardEmbeddingRemoval(db, "owner", target.ID); err != nil {
			t.Fatalf("strict offline clear should release the model: %v", err)
		}
	})

	t.Run("single unsigned legacy owner", func(t *testing.T) {
		db, target, other := setupEmbeddingDestructiveGuardTest(t)
		now := time.Now().UTC()
		if err := db.Create(&orm.UserSelectedModel{UserID: "owner", ModelKey: "embed_main", UserModelProviderGroupModelID: target.ID, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		requireEmbeddingDestructiveConflict(t, guardEmbeddingRemoval(db, "owner", target.ID), errEmbeddingInUse, "legacy_selected_owner")
		if err := guardEmbeddingRemoval(db, "other-owner", other.ID); err != nil {
			t.Fatalf("a distinct candidate is not the sole legacy owner: %v", err)
		}
	})
}

func TestEmbeddingRemovalFailsClosedWhenIndexIdentityCannotBeProved(t *testing.T) {
	t.Run("historical vectors without selection", func(t *testing.T) {
		db, target, other := setupEmbeddingDestructiveGuardTest(t)
		now := time.Now().UTC()
		if err := db.Create(&orm.Dataset{ID: "historical", KbID: "historical", ProcessingLevel: "indexed", BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
			t.Fatal(err)
		}
		for _, modelID := range []string{target.ID, other.ID} {
			requireEmbeddingDestructiveConflict(t, guardEmbeddingRemoval(db, "owner", modelID), errEmbeddingInUse, "index_identity_unverified")
		}
	})

	t.Run("blank processing level is historical", func(t *testing.T) {
		db, target, _ := setupEmbeddingDestructiveGuardTest(t)
		now := time.Now().UTC()
		dataset := orm.Dataset{ID: "blank-level", KbID: "blank-level", ProcessingLevel: "indexed", BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}
		if err := db.Create(&dataset).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&dataset).Update("processing_level", "").Error; err != nil {
			t.Fatal(err)
		}
		requireEmbeddingDestructiveConflict(t, guardEmbeddingRemoval(db, "owner", target.ID), errEmbeddingInUse, "index_identity_unverified")
	})

	t.Run("malformed signature is not ownership", func(t *testing.T) {
		db, target, other := setupEmbeddingDestructiveGuardTest(t)
		state := orm.EmbeddingIndexState{Scope: globalEmbeddingScope, ActiveModelID: target.ID, ActiveSource: "own", Signature: "v1:fingerprint", Phase: "ready", Generation: 1, UpdatedAt: time.Now().UTC()}
		if err := db.Create(&state).Error; err != nil {
			t.Fatal(err)
		}
		for _, modelID := range []string{target.ID, other.ID} {
			requireEmbeddingDestructiveConflict(t, guardEmbeddingRemoval(db, "owner", modelID), errEmbeddingInUse, "index_identity_unverified")
		}
	})

	t.Run("signature without active model is not ownership", func(t *testing.T) {
		db, target, _ := setupEmbeddingDestructiveGuardTest(t)
		state := orm.EmbeddingIndexState{Scope: globalEmbeddingScope, Signature: validTestEmbeddingFingerprint, Phase: "ready", Generation: 1, UpdatedAt: time.Now().UTC()}
		if err := db.Create(&state).Error; err != nil {
			t.Fatal(err)
		}
		requireEmbeddingDestructiveConflict(t, guardEmbeddingRemoval(db, "owner", target.ID), errEmbeddingInUse, "index_identity_unverified")
	})

	t.Run("unknown phase", func(t *testing.T) {
		db, target, _ := setupEmbeddingDestructiveGuardTest(t)
		state := orm.EmbeddingIndexState{Scope: globalEmbeddingScope, Phase: "future_phase", Generation: 1, UpdatedAt: time.Now().UTC()}
		if err := db.Create(&state).Error; err != nil {
			t.Fatal(err)
		}
		requireEmbeddingDestructiveConflict(t, guardEmbeddingRemoval(db, "owner", target.ID), errEmbeddingInUse, "index_identity_unverified")
	})

	t.Run("blank persisted phase", func(t *testing.T) {
		db, target, _ := setupEmbeddingDestructiveGuardTest(t)
		state := orm.EmbeddingIndexState{Scope: globalEmbeddingScope, Phase: "ready", Generation: 1, UpdatedAt: time.Now().UTC()}
		if err := db.Create(&state).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&state).Update("phase", "").Error; err != nil {
			t.Fatal(err)
		}
		requireEmbeddingDestructiveConflict(t, guardEmbeddingRemoval(db, "owner", target.ID), errEmbeddingInUse, "index_identity_unverified")
	})

	residueCases := map[string]func(*orm.EmbeddingIndexState){
		"active model":     func(state *orm.EmbeddingIndexState) { state.ActiveModelID = "old" },
		"active source":    func(state *orm.EmbeddingIndexState) { state.ActiveSource = "own" },
		"active name":      func(state *orm.EmbeddingIndexState) { state.ActiveModelName = "old" },
		"provider name":    func(state *orm.EmbeddingIndexState) { state.ActiveProviderName = "provider" },
		"signature":        func(state *orm.EmbeddingIndexState) { state.Signature = validTestEmbeddingFingerprint },
		"target model":     func(state *orm.EmbeddingIndexState) { state.TargetModelID = "next" },
		"target source":    func(state *orm.EmbeddingIndexState) { state.TargetSource = "own" },
		"target signature": func(state *orm.EmbeddingIndexState) { state.TargetSignature = validTestEmbeddingFingerprint },
	}
	for name, mutate := range residueCases {
		t.Run("inconsistent cleared "+name, func(t *testing.T) {
			db, target, _ := setupEmbeddingDestructiveGuardTest(t)
			state := orm.EmbeddingIndexState{Scope: globalEmbeddingScope, Phase: "cleared", Generation: 2, UpdatedAt: time.Now().UTC()}
			mutate(&state)
			if err := db.Create(&state).Error; err != nil {
				t.Fatal(err)
			}
			requireEmbeddingDestructiveConflict(t, guardEmbeddingRemoval(db, "owner", target.ID), errEmbeddingInUse, "index_identity_unverified")
		})
	}
}

func TestEmbeddingRemovalTreatsAllDistinctLegacyCandidatesAsAmbiguous(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *gorm.DB, orm.UserModelProviderGroupModel, orm.UserModelProviderGroupModel)
	}{
		{
			name: "different users own different models",
			setup: func(t *testing.T, db *gorm.DB, target, other orm.UserModelProviderGroupModel) {
				now := time.Now().UTC()
				rows := []orm.UserSelectedModel{
					{UserID: "owner", ModelKey: "embed_main", UserModelProviderGroupModelID: target.ID, CreatedAt: now, UpdatedAt: now},
					{UserID: "other-owner", ModelKey: "embed_main", UserModelProviderGroupModelID: other.ID, CreatedAt: now, UpdatedAt: now},
				}
				if err := db.Create(&rows).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "own and cloud",
			setup: func(t *testing.T, db *gorm.DB, target, _ orm.UserModelProviderGroupModel) {
				now := time.Now().UTC()
				if err := db.Create(&orm.UserSelectedModel{UserID: "owner", ModelKey: "embed_main", UserModelProviderGroupModelID: target.ID, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&orm.UserSelectedCloudModel{UserID: "cloud-user", ModelType: "embed_main", PublicModelKey: "cloud-embed", DisplayNameSnapshot: "Cloud Embed", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "disabled and deleted rows remain evidence",
			setup: func(t *testing.T, db *gorm.DB, target, other orm.UserModelProviderGroupModel) {
				now := time.Now().UTC()
				if err := db.Model(&target).Update("enabled", false).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Model(&other).Update("deleted_at", now).Error; err != nil {
					t.Fatal(err)
				}
				rows := []orm.UserSelectedModel{
					{UserID: "disabled-owner", ModelKey: "embed_main", UserModelProviderGroupModelID: target.ID, CreatedAt: now, UpdatedAt: now},
					{UserID: "deleted-owner", ModelKey: "embed_main", UserModelProviderGroupModelID: other.ID, CreatedAt: now, UpdatedAt: now},
				}
				if err := db.Create(&rows).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, target, other := setupEmbeddingDestructiveGuardTest(t)
			test.setup(t, db, target, other)
			candidate := orm.UserModelProviderGroupModel{
				ID: "unrelated", UserModelProviderID: "provider-unrelated", UserModelProviderGroupID: "group-unrelated",
				Name: "unrelated", ModelType: "embed", BaseModel: orm.BaseModel{CreateUserID: "third", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
			}
			if err := db.Create(&candidate).Error; err != nil {
				t.Fatal(err)
			}
			requireEmbeddingDestructiveConflict(t, guardEmbeddingRemoval(db, "third", candidate.ID), errEmbeddingInUse, "index_identity_unverified")
		})
	}
}

func TestEmbeddingDestructiveErrorDoesNotExposeIdentityDetails(t *testing.T) {
	err := newEmbeddingDestructiveOperationError(errEmbeddingInUse, "index_identity_unverified")
	requireEmbeddingDestructiveConflict(t, err, errEmbeddingInUse, "index_identity_unverified")
	if strings.Contains(err.Error(), "index_identity_unverified") {
		t.Fatalf("public error string leaked classifier detail: %q", err.Error())
	}
}
