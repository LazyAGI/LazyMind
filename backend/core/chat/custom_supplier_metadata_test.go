package chat

import (
	"context"
	"testing"

	"lazymind/core/common/orm"
)

func TestChatProviderMetadataKeepsCustomIdentityAndTechnicalProtocol(t *testing.T) {
	db := newChatModelTestDB(t).DB
	seedAvailableChatModel(t, db, "owner", "custom", "private-store", "chat", "OpenAI", "Proxy Alpha", "gpt-4o-mini", "llm", true, "selected-key")
	if err := db.Model(&orm.UserModelProvider{}).Where("id = ?", "custom").Updates(map[string]any{"name": "Proxy Alpha", "protocol_name": "OpenAI", "is_custom": true}).Error; err != nil {
		t.Fatal(err)
	}
	seedSelectedChatModel(t, db, "owner", "chat", false)
	response, err := buildChatModelsResponse(context.Background(), db, "owner", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Providers) != 1 || response.Providers[0].Name != "Proxy Alpha" || !response.Providers[0].IsCustom {
		t.Fatal("chat provider metadata lost custom supplier identity")
	}
	models, err := loadAvailableChatModels(context.Background(), db, "owner")
	if err != nil || len(models) != 1 || models[0].ProviderName != "OpenAI" {
		t.Fatal("display identity replaced technical protocol")
	}
	config, err := LoadDefaultChatLLMConfig(context.Background(), db, "owner")
	if err != nil || config["source"] != "openai" {
		t.Fatal("custom display name changed runtime adapter")
	}
}
