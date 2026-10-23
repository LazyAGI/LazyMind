package coreapi

import (
	"context"
	"testing"

	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
)

func TestManagedEndpointsFollowCredentialsWithoutReinstall(t *testing.T) {
	home := t.TempDir()
	store, _ := credentials.NewStore(home, "")
	path, err := connectorconfig.Save(connectorconfig.Config{Home: home, Provider: "codex", HostID: "host"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(connectorconfig.EnvironmentKey, path)
	t.Setenv("LAZYMIND_MCP_URL", "http://obsolete.example/mcp")
	t.Setenv("LAZYMIND_WEB_URL", "file:///invalid")
	api, _ := New(store)
	for _, base := range []string{"http://127.0.0.1:8090", "http://127.0.0.1:8190/prefix"} {
		if err := store.Save(credentials.Credentials{ServerURL: base, AccessToken: "a", RefreshToken: "r"}); err != nil {
			t.Fatal(err)
		}
		if got, err := api.WebURL(context.Background()); err != nil || got != base {
			t.Fatalf("web=%q %v", got, err)
		}
		if got, err := api.MCPURL(context.Background()); err != nil || got != base+"/api/core/mcp/capabilities/v1" {
			t.Fatalf("mcp=%q %v", got, err)
		}
	}
}
