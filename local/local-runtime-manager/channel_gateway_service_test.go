package main

import (
	"path/filepath"
	"strconv"
	"testing"
)

func TestChannelGatewayEnvUsesLocalPersistentStateAndCore(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}

	env := channelGatewayEnv(cfg, paths)
	assertEnvContains(t, env, "LAZYMIND_CHANNEL_GATEWAY_DATABASE_DSN="+sqliteURL(paths.ChannelGatewayDBPath))
	assertEnvContains(t, env, "LAZYMIND_CHANNEL_GATEWAY_CREDENTIAL_KEY_PATH="+paths.ChannelGatewayKeyPath)
	assertEnvContains(
		t,
		env,
		"LAZYMIND_CHANNEL_GATEWAY_CORE_BASE_URL=http://127.0.0.1:"+strconv.Itoa(cfg.LocalProxy.CoreHostPort),
	)
	if paths.ChannelGatewayDBPath != filepath.Join(paths.RuntimeRoot, "data", "stores", "sqlite", channelGatewayProcessName, "channel-gateway.db") {
		t.Fatalf("channel gateway db path = %q", paths.ChannelGatewayDBPath)
	}
}
