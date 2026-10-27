package modelprovider

import (
	"context"
	"lazymind/core/common/orm"
	"testing"
)

func TestExplicitReadinessUsesEnabledCredentialInsteadOfVerification(t *testing.T) {
	db := setupListProviderTestDB(t)
	if err := db.AutoMigrate(&orm.UserSelectedModel{}); err != nil {
		t.Fatal(err)
	}
	base := orm.BaseModel{CreateUserID: "owner"}
	for _, row := range []any{
		&orm.UserModelProvider{ID: "p", Name: "OpenAI", Capabilities: "has_models", BaseURL: "https://api.openai.com/v1/", ActiveKeyInitialized: true, ActiveKeyGroupID: "g", ActiveKeyID: groupKeyID("g", "saved-key"), BaseModel: base},
		&orm.UserModelProviderGroup{ID: "g", UserModelProviderID: "p", APIKey: "saved-key", BaseModel: base},
		&orm.UserModelProviderGroupModel{ID: "m", UserModelProviderID: "p", UserModelProviderGroupID: "g", ProviderName: "OpenAI", Name: "private-model", ModelType: "llm", Vision: true, BaseModel: base},
		&orm.UserSelectedModel{UserID: "owner", ModelKey: "llm", UserModelProviderGroupModelID: "m", Share: true},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	if ready, err := hasValidModelSelection(ctx, db, "owner", "llm", false); err != nil || !ready {
		t.Fatalf("explicit unverified readiness: %v %v", ready, err)
	}
	if ready, err := hasVisionLLMSelection(ctx, db, "owner"); err != nil || !ready {
		t.Fatalf("explicit unverified vision readiness: %v %v", ready, err)
	}
	if ready, err := hasValidModelSelection(ctx, db, "other", "llm", true); err != nil || ready {
		t.Fatalf("unverified shared auto readiness: %v %v", ready, err)
	}
	if err := db.Model(&orm.UserModelProvider{}).Where("id = ?", "p").Updates(map[string]any{"active_key_id": "", "active_key_group_id": ""}).Error; err != nil {
		t.Fatal(err)
	}
	if ready, err := hasValidModelSelection(ctx, db, "owner", "llm", false); err != nil || ready {
		t.Fatalf("disabled explicit readiness: %v %v", ready, err)
	}
	if ready, err := hasVisionLLMSelection(ctx, db, "owner"); err != nil || ready {
		t.Fatalf("disabled explicit vision readiness: %v %v", ready, err)
	}
	sharedBase := orm.BaseModel{CreateUserID: "admin"}
	for _, row := range []any{
		&orm.UserModelProvider{ID: "shared-p", Name: "OpenAI", Capabilities: "has_models", BaseModel: sharedBase},
		&orm.UserModelProviderGroup{ID: "shared-g", UserModelProviderID: "shared-p", APIKey: "shared-key", BaseURL: "https://api.openai.com/v1/", IsVerified: true, BaseModel: sharedBase},
		&orm.UserModelProviderGroupModel{ID: "shared-m", UserModelProviderID: "shared-p", UserModelProviderGroupID: "shared-g", ProviderName: "OpenAI", Name: "shared-model", ModelType: "llm", Vision: true, BaseModel: sharedBase},
		&orm.UserSelectedModel{UserID: "admin", ModelKey: "llm", UserModelProviderGroupModelID: "shared-m", Share: true},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if ready, err := hasValidModelSelection(ctx, db, "owner", "llm", true); err != nil || ready {
		t.Fatalf("disabled explicit preference borrowed shared credentials: %v %v", ready, err)
	}
	if ready, err := hasVisionLLMSelection(ctx, db, "owner"); err != nil || ready {
		t.Fatalf("disabled explicit vision preference borrowed shared credentials: %v %v", ready, err)
	}
}
