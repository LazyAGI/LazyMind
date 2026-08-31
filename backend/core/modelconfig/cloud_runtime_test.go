package modelconfig

import (
	"context"
	"net/url"
	"testing"
	"time"

	"lazymind/core/cloudclient"
)

type runtimeTestTokens struct{}

func (runtimeTestTokens) AccessToken(context.Context, time.Duration) (string, error) {
	return "cloud-access", nil
}

type runtimeTestClient struct{ calls int }

func (c *runtimeTestClient) Origin() string { return "https://cloud.example" }
func (c *runtimeTestClient) GetProviderBootstrap(context.Context, string) (cloudclient.ModelProviderBootstrap, error) {
	c.calls++
	return withCloudEntitlement(cloudclient.ModelProviderBootstrap{Available: true, Models: []cloudclient.PublicCloudModel{{
		ModelKey: "cloud-text", Capabilities: []string{"chat"}, Status: "available",
	}}}, true, true, ""), nil
}

func TestFillMissingRolesPreservesExistingSelections(t *testing.T) {
	existing := []SelectedRuntimeModel{{ModelType: "llm", ProviderName: "openai", ModelName: "personal", BaseURL: "https://personal.example/v1", APIKey: "personal-key"}}
	got := FillMissingRoles(existing, CloudRuntimeConfig{
		Source: "openai", BaseURL: "https://cloud.example/v1", AccessToken: "cloud-token",
		Models: map[string]string{"llm": "cloud-text", "embed_main": "cloud-embed"},
	})
	byType := map[string]SelectedRuntimeModel{}
	for _, row := range got {
		byType[row.ModelType] = row
	}
	if byType["llm"].ModelName != "personal" || byType["llm"].APIKey != "personal-key" {
		t.Fatalf("existing selection was replaced: %+v", byType["llm"])
	}
	if byType["embed_main"].ModelName != "cloud-embed" || byType["embed_main"].APIKey != "cloud-token" {
		t.Fatalf("missing role was not filled: %+v", byType["embed_main"])
	}
}

func TestRuntimeConfigFromCloudBootstrapUsesExistingProviderProtocol(t *testing.T) {
	config := RuntimeConfigFromCloudBootstrap(cloudclient.ModelProviderBootstrap{Models: []cloudclient.PublicCloudModel{
		{ModelKey: "cloud-text", Capabilities: []string{"chat", "stream", "tool_calls"}, Status: "available"},
		{ModelKey: "cloud-embed", Capabilities: []string{"embedding"}, Status: "available"},
	}}, "https://cloud.example/v1/", "cloud-access")
	if config.Source != "openai" || config.BaseURL != "https://cloud.example/v1/" || config.AccessToken != "cloud-access" {
		t.Fatalf("config = %+v", config)
	}
	if config.Models["llm"] != "cloud-text" || config.Models["embed_main"] != "cloud-embed" {
		t.Fatalf("models = %+v", config.Models)
	}
}

func TestCloudRuntimeBaseURLPreservesVersionPathForOpenAIEndpoints(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "https://cloud.example/v1", want: "https://cloud.example/v1/"},
		{input: " https://cloud.example/v1/// ", want: "https://cloud.example/v1/"},
		{input: "", want: ""},
	}
	for _, test := range tests {
		if got := normalizeCloudModelBaseURL(test.input); got != test.want {
			t.Errorf("normalizeCloudModelBaseURL(%q)=%q want=%q", test.input, got, test.want)
		}
	}

	base, err := url.Parse(normalizeCloudModelBaseURL("http://127.0.0.1:8080/v1"))
	if err != nil {
		t.Fatal(err)
	}
	resolved := base.ResolveReference(&url.URL{Path: "chat/completions"})
	if got := resolved.String(); got != "http://127.0.0.1:8080/v1/chat/completions" {
		t.Fatalf("resolved Cloud Chat endpoint=%q", got)
	}
}

func TestCloudRuntimeProviderCachesBootstrapButUsesCurrentMemoryToken(t *testing.T) {
	client := &runtimeTestClient{}
	provider := &CloudRuntimeProvider{Session: runtimeTestTokens{}, Client: client}
	for range 2 {
		config, available, err := provider.RuntimeConfig(context.Background())
		if err != nil || !available || config.Models["llm"] != "cloud-text" || config.AccessToken != "cloud-access" {
			t.Fatalf("config=%+v available=%v err=%v", config, available, err)
		}
	}
	if client.calls != 1 {
		t.Fatalf("bootstrap calls=%d want=1", client.calls)
	}
}

func TestFillMissingRolesDoesNothingWithoutCloudSession(t *testing.T) {
	got := FillMissingRoles(nil, CloudRuntimeConfig{Source: "openai", BaseURL: "https://cloud.example/v1", Models: map[string]string{"llm": "cloud-text"}})
	if len(got) != 0 {
		t.Fatalf("cloud config must remain absent without an access token: %+v", got)
	}
}
