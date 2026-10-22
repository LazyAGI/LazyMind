package workbuddyhost

import (
	"context"
	"encoding/json"
	"lazymind/agentconnector/internal/workflowhost"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDisconnectIsIdempotentAndPairingSurvivesReconnect(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, "wb-profile")
	pair, err := workflowhost.EnsureForProvider(home, "workbuddy", profile, "account-a")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(Config{PairingFile: filepath.Join(home, "workflow-hosts", pair.ConnectorID+".json"), Node: filepath.Join(home, "node"), CDPPort: 1})
	if err := writeAtomic(ConfigPath(home), body); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(home, "account-b"); err == nil {
		t.Fatal("cross-account configuration loaded")
	}
	for i := 0; i < 2; i++ {
		if err := Disable(home, "account-a"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := Load(home, "account-a"); err == nil {
		t.Fatal("disabled connector activated")
	}
	if err := Disable(home, "account-b"); err == nil {
		t.Fatal("wrong account accepted disabled pairing")
	}
	second, err := workflowhost.EnsureForProvider(home, "workbuddy", profile, "account-a")
	if err != nil {
		t.Fatal(err)
	}
	if second.ConnectorID != pair.ConnectorID || second.Token != pair.Token {
		t.Fatal("reconnect changed pairing identity")
	}
}

func TestFindNodeReusesWorkBuddyManagedRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix only")
	}
	profile := t.TempDir()
	versions := filepath.Join(profile, "binaries", "node", "versions")
	node := filepath.Join(versions, "22.22.2-6", "bin", "node")
	if err := os.MkdirAll(filepath.Dir(node), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(node, []byte("#!/bin/sh\nprintf 'v22.22.2\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versions, "current"), []byte("22.22.2-6"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_NODE_BIN", "")
	resolvedNode, err := filepath.EvalSymlinks(node)
	if err != nil {
		t.Fatal(err)
	}
	found, err := findNode(context.Background(), profile)
	if err != nil || found != resolvedNode {
		t.Fatalf("found=%q err=%v", found, err)
	}
}
