package modelconfig

import (
	"context"
	"net/url"
	"sync"
	"sync/atomic"
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

type blockingRuntimeTestClient struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (client *blockingRuntimeTestClient) Origin() string { return "https://cloud.example" }
func (client *blockingRuntimeTestClient) GetProviderBootstrap(ctx context.Context, _ string) (cloudclient.ModelProviderBootstrap, error) {
	if client.calls.Add(1) == 1 {
		close(client.entered)
		select {
		case <-client.release:
		case <-ctx.Done():
			return cloudclient.ModelProviderBootstrap{}, ctx.Err()
		}
	}
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

func TestRuntimeConfigFromCloudBootstrapMapsEveryPublishedModelRole(t *testing.T) {
	models := []cloudclient.PublicCloudModel{
		{ModelKey: "lazymind-text-default", Capabilities: []string{"chat"}, Status: "available"},
		{ModelKey: "lazymind-evolution-default", Capabilities: []string{"chat", "stream"}, Status: "available"},
		{ModelKey: "lazymind-vision-default", Capabilities: []string{"chat", "vision"}, Status: "available"},
		{ModelKey: "lazymind-embedding-default", Capabilities: []string{"embedding"}, Status: "available"},
		{ModelKey: "lazymind-multimodal-embedding-default", Capabilities: []string{"multimodal_embedding"}, Status: "available"},
		{ModelKey: "lazymind-rerank-default", Capabilities: []string{"rerank"}, Status: "available"},
		{ModelKey: "lazymind-image-default", Capabilities: []string{"image_generation"}, Status: "available"},
		{ModelKey: "lazymind-image-edit-default", Capabilities: []string{"image_editing"}, Status: "available"},
		{ModelKey: "lazymind-video-default", Capabilities: []string{"video_generation"}, Status: "available"},
		{ModelKey: "lazymind-stt-default", Capabilities: []string{"speech_to_text"}, Status: "available"},
		{ModelKey: "lazymind-tts-default", Capabilities: []string{"text_to_speech"}, Status: "available"},
	}
	config := RuntimeConfigFromCloudBootstrap(
		cloudclient.ModelProviderBootstrap{Models: models},
		"https://cloud.example/v1/",
		"cloud-access",
	)
	want := map[string]string{
		"llm": "lazymind-text-default", "evo_llm": "lazymind-evolution-default",
		"vlm": "lazymind-vision-default", "embed_main": "lazymind-embedding-default",
		"embed_image": "lazymind-multimodal-embedding-default", "reranker": "lazymind-rerank-default",
		"text2image": "lazymind-image-default", "image_editing": "lazymind-image-edit-default",
		"text2video": "lazymind-video-default", "stt": "lazymind-stt-default",
		"tts": "lazymind-tts-default",
	}
	for modelType, modelKey := range want {
		if config.Models[modelType] != modelKey {
			t.Errorf("Cloud role %s=%q want=%q; all=%#v", modelType, config.Models[modelType], modelKey, config.Models)
		}
	}
}

func TestRuntimeConfigFromVersionedCloudCatalogUsesOnlyActiveDefaults(t *testing.T) {
	config := RuntimeConfigFromCloudBootstrap(cloudclient.ModelProviderBootstrap{
		CatalogRevision: "sha256:catalog",
		Models: []cloudclient.PublicCloudModel{
			{ModelKey: "cloud-active-default", ModelType: "llm", Status: "available", Lifecycle: "active", DefaultForType: true},
			{ModelKey: "cloud-active-secondary", ModelType: "llm", Status: "available", Lifecycle: "active"},
			{ModelKey: "cloud-deprecated-default", ModelType: "vlm", Status: "available", Lifecycle: "deprecated", DefaultForType: true},
			{ModelKey: "cloud-retired-default", ModelType: "embed_main", Status: "available", Lifecycle: "retired", DefaultForType: true},
		},
	}, "https://cloud.example/v1/", "cloud-access")
	if config.Models["llm"] != "cloud-active-default" {
		t.Fatalf("versioned default=%q want cloud-active-default", config.Models["llm"])
	}
	if _, found := config.Models["vlm"]; found {
		t.Fatalf("deprecated model entered automatic fallback: %#v", config.Models)
	}
	if _, found := config.Models["embed_main"]; found {
		t.Fatalf("retired model entered automatic fallback: %#v", config.Models)
	}
}

func TestCloudRuntimeProviderAllowsAvailableNonChatModelsWithoutCloudChat(t *testing.T) {
	bootstrap := withCloudEntitlement(cloudclient.ModelProviderBootstrap{
		Available: true,
		Models: []cloudclient.PublicCloudModel{{
			ModelKey: "lazymind-embedding-default", Capabilities: []string{"embedding"}, Status: "available",
		}},
	}, true, false, "model_unavailable")
	provider := &CloudRuntimeProvider{
		Session: runtimeTestTokens{},
		Client:  entitlementRuntimeTestClient{bootstrap: bootstrap},
	}
	config, available, err := provider.RuntimeConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !available || config.Models["embed_main"] != "lazymind-embedding-default" {
		t.Fatalf("non-Chat Cloud catalog was disabled: available=%v config=%+v", available, config)
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

func TestCloudRuntimeRefreshDoesNotSerializeLocalFallbackCallersBehindNetwork(t *testing.T) {
	client := &blockingRuntimeTestClient{entered: make(chan struct{}), release: make(chan struct{})}
	provider := &CloudRuntimeProvider{Session: runtimeTestTokens{}, Client: client}
	type result struct {
		available bool
		err       error
	}
	firstDone := make(chan result, 1)
	go func() {
		_, available, err := provider.RuntimeConfig(context.Background())
		firstDone <- result{available: available, err: err}
	}()
	<-client.entered

	secondDone := make(chan result, 1)
	go func() {
		_, available, err := provider.RuntimeConfig(context.Background())
		secondDone <- result{available: available, err: err}
	}()

	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(client.release) }) }
	defer release()
	select {
	case second := <-secondDone:
		if second.err != nil || second.available {
			t.Fatalf("in-flight fallback result=%+v want immediate unavailable without an error", second)
		}
	case <-time.After(150 * time.Millisecond):
		release()
		<-firstDone
		<-secondDone
		t.Fatal("a concurrent Cloud refresh serialized another caller behind network I/O")
	}

	release()
	first := <-firstDone
	if first.err != nil || !first.available {
		t.Fatalf("first refresh result=%+v", first)
	}
	if client.calls.Load() != 1 {
		t.Fatalf("bootstrap calls=%d want=1", client.calls.Load())
	}
}

func TestFillMissingRolesDoesNothingWithoutCloudSession(t *testing.T) {
	got := FillMissingRoles(nil, CloudRuntimeConfig{Source: "openai", BaseURL: "https://cloud.example/v1", Models: map[string]string{"llm": "cloud-text"}})
	if len(got) != 0 {
		t.Fatalf("cloud config must remain absent without an access token: %+v", got)
	}
}
