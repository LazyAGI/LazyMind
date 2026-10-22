package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workflowhost"
)

func TestConnectionResolverReadsCurrentStateAndRejectsRevocation(t *testing.T) {
	home, profile, root := t.TempDir(), t.TempDir(), t.TempDir()
	store, _ := credentials.NewStore(home, "")
	value := credentials.Credentials{ServerURL: "http://127.0.0.1:8090", Username: "alice", AccessToken: "secret-access", RefreshToken: "secret-refresh"}
	if err := store.SaveDesktop(value, root); err != nil {
		t.Fatal(err)
	}
	account, _ := store.AccountScope()
	pair, err := workflowhost.Ensure(home, profile, account)
	if err != nil {
		t.Fatal(err)
	}
	path, err := connectorconfig.Save(connectorconfig.Config{Home: home, Provider: "deepseek-harness", HostID: "host", Profile: profile, PairingFile: filepath.Join(home, "workflow-hosts", pair.ConnectorID+".json"), Controlled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(connectorconfig.EnvironmentKey, path)
	t.Setenv("LAZYMIND_WEB_URL", "file:///obsolete")
	t.Setenv("LAZYMIND_ASSISTANT_BRIDGE_URL", "http://remote.example:9999")
	read := func(want string) {
		t.Helper()
		var out bytes.Buffer
		if err := runInternal(context.Background(), []string{"connection", "resolve"}, &out, &out); err != nil {
			t.Fatal(err)
		}
		var result map[string]string
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result["webUrl"] != want || result["bridgeUrl"] != connectorconfig.DefaultBridgeURL {
			t.Fatalf("unexpected endpoints: %+v", result)
		}
		for _, secret := range []string{value.AccessToken, value.RefreshToken, pair.Token} {
			if strings.Contains(out.String(), secret) {
				t.Fatal("resolver exposed credential")
			}
		}
	}
	read(value.ServerURL)
	value.ServerURL = "http://127.0.0.1:8190"
	if err := store.SaveDesktop(value, root); err != nil {
		t.Fatal(err)
	}
	read(value.ServerURL)
	if err := workflowhost.Disable(home, pair.ConnectorID); err != nil {
		t.Fatal(err)
	}
	if err := resolveConnection(context.Background(), &bytes.Buffer{}); err == nil {
		t.Fatal("revoked pairing resolved")
	}
	if err := os.WriteFile(path, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := resolveConnection(context.Background(), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("missing configuration diagnosis: %v", err)
	}
}
