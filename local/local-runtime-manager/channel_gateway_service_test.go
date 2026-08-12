package main

import (
	"path/filepath"
	"strconv"
	"strings"
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

func TestChannelGatewayProcessEnvIsolatesPythonPaths(t *testing.T) {
	t.Setenv("PYTHONHOME", "polluted-home")
	t.Setenv("PYTHONPATH", "polluted-path")
	t.Setenv("VIRTUAL_ENV", "polluted-venv")
	t.Setenv("HTTPS_PROXY", "http://proxy.example")
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}

	env := channelGatewayProcessEnv(cfg, paths)
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		switch strings.ToUpper(key) {
		case "PYTHONHOME", "PYTHONPATH", "VIRTUAL_ENV":
			t.Fatalf("polluting Python variable was inherited: %s", key)
		}
	}
	assertEnvContains(t, env, "HTTPS_PROXY=http://proxy.example")
}
