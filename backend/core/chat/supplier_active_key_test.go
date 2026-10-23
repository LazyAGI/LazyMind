package chat

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"

	"lazymind/core/common/orm"
)

func TestChatBuilderUsesSingleSupplierKeyFromAnotherConnection(t *testing.T) {
	db := newChatModelTestDB(t).DB
	seedAvailableChatModel(t, db, "owner", "supplier", "models", "chat", "OpenAI", "Models", "gpt-4o-mini", "llm", true, "inactive-model-key")
	group := orm.UserModelProviderGroup{ID: "keys", UserModelProviderID: "supplier", Name: "Saved keys", APIKey: "enabled-key\nunused-key", IsVerified: true, BaseModel: orm.BaseModel{CreateUserID: "owner"}}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	keyID := fmt.Sprintf("%x", sha256.Sum256([]byte("keys\x00enabled-key")))
	if err := db.Model(&orm.UserModelProvider{}).Where("id = ?", "supplier").Updates(map[string]any{"base_url": "https://shared.example/v1", "active_key_initialized": true, "active_key_group_id": "keys", "active_key_id": keyID}).Error; err != nil {
		t.Fatal(err)
	}
	model := &availableChatModel{ProviderGroupID: "models", ProviderName: "OpenAI", ModelName: "gpt-4o-mini", ModelType: "llm", BaseURL: "https://stale.example/v1", APIKey: "inactive-model-key"}
	for i := 0; i < 3; i++ {
		value, err := buildChatLLMConfig(context.Background(), db, model)
		if err != nil {
			t.Fatal(err)
		}
		config, ok := value.(map[string]any)
		if !ok || config["api_key"] != "enabled-key" || config["base_url"] != "https://shared.example/v1/" {
			t.Fatal("chat builder used an inactive key or a connection-level URL")
		}
	}
	if err := db.Model(&orm.UserModelProvider{}).Where("id = ?", "supplier").Updates(map[string]any{"active_key_group_id": "", "active_key_id": ""}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := buildChatLLMConfig(context.Background(), db, model); err == nil {
		t.Fatal("disabled supplier key still usable")
	}
}
