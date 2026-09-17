package main

import (
	"strconv"
	"testing"
)

func TestNotificationRuntimeUsesMatchingServiceIdentityAndLoopback(t *testing.T) {
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "synthetic-notification-runtime-token")
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatal(err)
	}
	core, gateway := coreServiceEnv(cfg, paths), channelGatewayEnv(cfg, paths)
	assertEnvContains(t, core, "LAZYMIND_CHANNEL_GATEWAY_BASE_URL=http://127.0.0.1:"+strconv.Itoa(cfg.ChannelGateway.Port))
	assertEnvContains(t, gateway, "LAZYMIND_CHANNEL_GATEWAY_CORE_BASE_URL=http://127.0.0.1:"+strconv.Itoa(cfg.LocalProxy.CoreHostPort))
	for _, env := range [][]string{core, gateway} {
		assertEnvContains(t, env, "LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN=synthetic-notification-runtime-token")
	}
}
