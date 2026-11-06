package modelconfig

import (
	"context"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestLoadLLMConfigUsesOneGlobalEmbeddingForEveryUser(t *testing.T) {
	db := orm.MigrateTestDB(t,
		&orm.DefaultModelProvider{}, &orm.UserModelProvider{}, &orm.UserModelProviderGroup{},
		&orm.UserModelProviderGroupModel{}, &orm.UserSelectedModel{}, &orm.UserSelectedCloudModel{},
		&orm.EmbeddingIndexState{}, &orm.Dataset{}).DB
	now := time.Now().UTC()
	for _, item := range []struct{ user, id, model string }{{"alice", "a", "embed-a"}, {"bob", "b", "embed-b"}} {
		base := orm.BaseModel{CreateUserID: item.user, CreatedAt: now, UpdatedAt: now}
		for _, row := range []any{
			&orm.UserModelProvider{ID: "provider-" + item.id, Name: "OpenAI", ProtocolName: "OpenAI", Category: "model", Capabilities: "has_models", BaseModel: base},
			&orm.UserModelProviderGroup{ID: "group-" + item.id, UserModelProviderID: "provider-" + item.id, BaseURL: "https://example.com/v1", APIKey: "key-" + item.id, IsVerified: true, BaseModel: base},
			&orm.UserModelProviderGroupModel{ID: "model-" + item.id, UserModelProviderID: "provider-" + item.id, UserModelProviderGroupID: "group-" + item.id, ProviderName: "OpenAI", Name: item.model, ModelType: "embed", Enabled: true, BaseModel: base},
			&orm.UserSelectedModel{UserID: item.user, ModelKey: "embed_main", UserModelProviderGroupModelID: "model-" + item.id, Share: true, CreatedAt: now, UpdatedAt: now},
		} {
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	signed := "v1:1:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)
	state := orm.EmbeddingIndexState{Scope: "global", ActiveModelID: "model-a", ActiveSource: "own", ActiveModelName: "embed-a", Signature: signed, Phase: "ready", Generation: 1, UpdatedAt: now}
	if err := db.Create(&state).Error; err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"alice", "bob"} {
		config, err := LoadLLMConfig(context.Background(), db, user)
		if err != nil {
			t.Fatal(err)
		}
		embed, ok := config["embed_main"].(map[string]any)
		if !ok || embed["model"] != "embed-a" {
			t.Fatalf("%s used different embedding: %#v", user, config)
		}
	}
	bobBase := orm.BaseModel{CreateUserID: "bob", CreatedAt: now, UpdatedAt: now}
	for _, row := range []any{
		&orm.UserModelProviderGroup{ID: "chat-group", UserModelProviderID: "provider-b", BaseURL: "https://example.com/v1", APIKey: "chat-key", IsVerified: true, BaseModel: bobBase},
		&orm.UserModelProviderGroupModel{ID: "chat-model", UserModelProviderID: "provider-b", UserModelProviderGroupID: "chat-group", ProviderName: "OpenAI", Name: "chat-b", ModelType: "llm", Enabled: true, BaseModel: bobBase},
		&orm.UserSelectedModel{UserID: "bob", ModelKey: "llm", UserModelProviderGroupModelID: "chat-model", CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "group-a").Updates(map[string]any{
		"api_key": "", "api_key_ciphertext": "invalid-test-ciphertext", "credential_version": 1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	config, err := LoadLLMConfig(context.Background(), db, "bob")
	if err != nil {
		t.Fatalf("unavailable global embedding must not break unrelated chat: %v", err)
	}
	chat, ok := config["llm"].(map[string]any)
	if !ok || chat["model"] != "chat-b" {
		t.Fatalf("chat role did not survive unavailable global embedding: %#v", config)
	}
	if _, present := config["embed_main"]; present {
		t.Fatalf("unavailable global embedding must not fall back to Bob's stale choice: %#v", config)
	}
	if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "group-a").Updates(map[string]any{
		"api_key": "key-a", "api_key_ciphertext": "", "credential_version": 0,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.EmbeddingIndexState{}).Where("scope = ?", "global").Update("signature", "").Error; err != nil {
		t.Fatal(err)
	}
	config, err = LoadLLMConfig(context.Background(), db, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if _, present := config["embed_main"]; present {
		t.Fatalf("persisted active model without a signature cannot use old vectors: %#v", config)
	}
	if err := db.Model(&orm.EmbeddingIndexState{}).Where("scope = ?", "global").Update("signature", signed).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.EmbeddingIndexState{}).Where("scope = ?", "global").Updates(map[string]any{"active_model_id": "", "active_source": ""}).Error; err != nil {
		t.Fatal(err)
	}
	config, err = LoadLLMConfig(context.Background(), db, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if _, present := config["embed_main"]; present {
		t.Fatalf("cleared global slot still fell back to stale user selection: %#v", config)
	}
	if err := db.Delete(&orm.EmbeddingIndexState{}, "scope = ?", "global").Error; err != nil {
		t.Fatal(err)
	}
	config, err = LoadLLMConfig(context.Background(), db, "bob")
	if err != nil {
		t.Fatal(err)
	}
	embed, ok := config["embed_main"].(map[string]any)
	if !ok || embed["model"] != "embed-a" {
		t.Fatalf("legacy selections did not converge on a deterministic global model: %#v", config)
	}
	if err := db.Create(&orm.Dataset{ID: "old-kb", KbID: "old-kb", ProcessingLevel: "indexed", BaseModel: orm.BaseModel{CreateUserID: "bob", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatal(err)
	}
	config, err = LoadLLMConfig(context.Background(), db, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if _, present := config["embed_main"]; present {
		t.Fatalf("unsigned legacy vectors cannot be read using an arbitrary prior selection: %#v", config)
	}
	chat, ok = config["llm"].(map[string]any)
	if !ok || chat["model"] != "chat-b" {
		t.Fatalf("blocking unsafe vectors also blocked unrelated chat: %#v", config)
	}
}
