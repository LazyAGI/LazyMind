package chat

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lazymind/core/cloudclient"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	"lazymind/core/modelprovider"
)

type chatCloudTokens struct{}

func (chatCloudTokens) AccessToken(context.Context, time.Duration) (string, error) {
	return "chat-cloud-access-canary", nil
}

type chatCloudClient struct{}

func (chatCloudClient) Origin() string { return "https://cloud.example" }

func (chatCloudClient) GetProviderBootstrap(context.Context, string) (cloudclient.ModelProviderBootstrap, error) {
	return cloudclient.ModelProviderBootstrap{
		ProviderKey: "lazymind-cloud", DisplayName: "LazyMind Cloud",
		Available: true, HasTokenPlan: true, CloudChatAvailable: true,
		ModelKey: "lazymind-text-default",
		Models: []cloudclient.PublicCloudModel{{
			ModelKey: "lazymind-text-default", DisplayName: "LazyMind Text",
			Capabilities: []string{"chat", "stream", "tool_calls"}, Status: "available",
		}},
	}, nil
}

type staticChatCloudCatalog struct {
	catalog modelprovider.CloudModelCatalog
}

func (provider staticChatCloudCatalog) CloudModelCatalog(context.Context) (modelprovider.CloudModelCatalog, error) {
	return provider.catalog, nil
}

func installChatCloudCatalog(t *testing.T) {
	t.Helper()
	provider := &modelconfig.CloudRuntimeProvider{Session: chatCloudTokens{}, Client: chatCloudClient{}}
	modelconfig.SetRuntimeProvider(provider)
	modelprovider.SetCloudReadinessProvider(provider)
	modelprovider.SetCloudCatalogProvider(provider)
	t.Cleanup(func() {
		modelconfig.SetRuntimeProvider(nil)
		modelprovider.SetCloudReadinessProvider(nil)
		modelprovider.SetCloudCatalogProvider(nil)
	})
}

func TestChatCatalogUsesCloudAsDefaultWhenNoLocalModelExists(t *testing.T) {
	database := newPromptTestDB(t)
	installChatCloudCatalog(t)

	response, err := buildChatModelsResponse(context.Background(), database.DB, "user-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !response.AutoAvailable || response.DefaultSelection.Source != "cloud" || response.DefaultSelection.ModelID != "lazymind-text-default" {
		t.Fatalf("Cloud-only Chat default=%#v auto_available=%v", response.DefaultSelection, response.AutoAvailable)
	}
	if len(response.Providers) != 1 || response.Providers[0].Source != "cloud" || response.Providers[0].Name != "LazyMind Cloud" {
		t.Fatalf("Cloud Chat providers=%#v", response.Providers)
	}
	if len(response.Providers[0].Models) != 1 || response.Providers[0].Models[0].Name != "LazyMind Text" {
		t.Fatalf("Cloud Chat models=%#v", response.Providers[0].Models)
	}
}

func TestConversationFixedCloudSelectionBuildsOnlyInMemoryRuntimeConfig(t *testing.T) {
	database := newPromptTestDB(t)
	db := database.DB
	installChatCloudCatalog(t)
	mode := chatModelModeFixed
	modelID := "lazymind-text-default"
	snapshot, err := json.Marshal(chatModelSnapshot{
		ModelID: modelID, ProviderID: "lazymind-cloud", ProviderName: "LazyMind Cloud",
		ModelName: "LazyMind Text", Source: "cloud",
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation := orm.Conversation{
		ID: "conversation-cloud-fixed", ChatModelMode: &mode, ChatModelID: &modelID,
		ChatModelSnapshot: snapshot, ChatModelVersion: 1,
		BaseModel: orm.BaseModel{
			CreateUserID: "user-1", CreateUserName: "User",
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		},
	}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"conversation_id": conversation.ID}
	if err := applyConversationChatModelConfig(context.Background(), db, "user-1", body); err != nil {
		t.Fatalf("apply fixed Cloud model: %v", err)
	}
	config, _ := body["llm_config"].(map[string]any)
	llm, _ := config["llm"].(map[string]any)
	if llm["model"] != modelID || llm["base_url"] != "https://cloud.example/v1/" || llm["api_key"] != "chat-cloud-access-canary" {
		t.Fatalf("fixed Cloud llm_config=%#v", llm)
	}
	if route := chatModelRouteFromBody(body); route == nil || route.Source != "cloud" || route.ModelID != modelID {
		t.Fatalf("fixed Cloud route=%#v", route)
	}
}

func TestDeprecatedCloudChatModelRemainsVisibleButIsNotAnAutomaticCandidate(t *testing.T) {
	database := newPromptTestDB(t)
	modelprovider.SetCloudCatalogProvider(staticChatCloudCatalog{catalog: modelprovider.CloudModelCatalog{
		Known: true, Available: true, ProviderID: modelprovider.CloudSystemProviderID,
		ProviderName: modelprovider.CloudSystemProviderName,
		Models: []modelprovider.CloudCatalogModel{{
			ModelKey: "cloud-deprecated", DisplayName: "Deprecated Cloud Chat", ModelType: "llm",
			Status: "available", Lifecycle: "deprecated", DefaultForType: true,
			Capabilities: []string{"chat"},
		}},
	}})
	t.Cleanup(func() { modelprovider.SetCloudCatalogProvider(nil) })

	response, err := buildChatModelsResponse(context.Background(), database.DB, "user-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.AutoAvailable || response.DefaultSelection.ModelID != "" {
		t.Fatalf("deprecated Cloud model entered automatic selection: %#v", response)
	}
	if len(response.Providers) != 1 || len(response.Providers[0].Models) != 1 ||
		response.Providers[0].Models[0].Lifecycle != "deprecated" {
		t.Fatalf("deprecated Cloud model was not retained in the catalog: %#v", response.Providers)
	}
}
