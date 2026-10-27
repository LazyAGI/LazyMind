package modelconfig

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestRuntimeAndAdminEmbeddingUseSupplierKeyAcrossConnections(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}, &orm.UserSelectedModel{}, &orm.EvolutionModelValidation{})
	base := orm.BaseModel{CreateUserID: "owner", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	keyID := fmt.Sprintf("%x", sha256.Sum256([]byte("keys\x00selected-key")))
	for _, row := range []any{
		&orm.UserModelProvider{ID: "supplier", Name: "OpenAI", Category: "model", Capabilities: "has_models", BaseURL: "https://shared.example/v1", ActiveKeyInitialized: true, ActiveKeyGroupID: "keys", ActiveKeyID: keyID, BaseModel: base},
		&orm.UserModelProviderGroup{ID: "keys", UserModelProviderID: "supplier", Name: "Saved keys", APIKey: "selected-key\ninactive-key", IsVerified: true, BaseModel: base},
		&orm.UserModelProviderGroup{ID: "models", UserModelProviderID: "supplier", Name: "Models without keys", BaseURL: "https://legacy.example/v1", IsVerified: true, BaseModel: base},
		&orm.UserModelProviderGroupModel{ID: "chat", UserModelProviderID: "supplier", UserModelProviderGroupID: "models", ProviderName: "OpenAI", Name: "gpt-4o-mini", ModelType: "llm", BaseModel: base},
		&orm.UserModelProviderGroupModel{ID: "embedding", UserModelProviderID: "supplier", UserModelProviderGroupID: "models", ProviderName: "OpenAI", Name: "text-embedding-3-small", ModelType: "embed", IsDefault: true, BaseModel: base},
		&orm.UserSelectedModel{UserID: "owner", ModelKey: "llm", UserModelProviderGroupModelID: "chat", Share: true},
		&orm.UserSelectedModel{UserID: "owner", ModelKey: "evo_llm", UserModelProviderGroupModelID: "chat", Share: true},
	} {
		if err := db.DB.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	for _, user := range []string{"owner", "shared-reader"} {
		config, err := LoadLLMConfig(ctx, db.DB, user)
		if err != nil {
			t.Fatal(err)
		}
		llm, ok := config["llm"].(map[string]any)
		if !ok || llm["api_key"] != "selected-key" || llm["base_url"] != "https://shared.example/v1/" {
			t.Fatal("own/shared runtime ignored supplier-selected credential")
		}
	}
	embed, err := LoadAdminEmbedConfig(ctx, db.DB)
	if err != nil || embed["api_key"] != "selected-key" || embed["base_url"] != "https://shared.example/v1/" {
		t.Fatal("admin embedding ignored supplier-selected credential")
	}
	evolution, err := LoadEvolutionModels(ctx, db.DB, "owner")
	if err != nil || evolution.AvailableDefaultRef == "" {
		t.Fatal("evolution default unavailable")
	}
	oldRef := evolution.AvailableDefaultRef
	if _, _, err := ResolveEvolutionModel(ctx, db.DB, "owner", oldRef); err != nil {
		t.Fatal("first-use evolution reference was unstable")
	}
	newKeyID := fmt.Sprintf("%x", sha256.Sum256([]byte("keys\x00inactive-key")))
	if err := db.DB.Model(&orm.UserModelProvider{}).Where("id = ?", "supplier").Update("active_key_id", newKeyID).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveEvolutionModel(ctx, db.DB, "owner", oldRef); err == nil {
		t.Fatal("cross-connection key switch reused old evolution reference")
	}
	if config, _, err := ResolveEvolutionModel(ctx, db.DB, "shared-reader", ""); err != nil || config["api_key"] != "inactive-key" {
		t.Fatal("evolution failed to use newly selected supplier key")
	}
	if err := db.DB.Model(&orm.UserModelProvider{}).Where("id = ?", "supplier").Updates(map[string]any{"active_key_group_id": "", "active_key_id": ""}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLLMConfig(ctx, db.DB, "owner"); err == nil {
		t.Fatal("disabled supplier was used for chat runtime")
	}
	if _, err := LoadAdminEmbedConfig(ctx, db.DB); err == nil {
		t.Fatal("disabled supplier was used for admin embedding")
	}
}
