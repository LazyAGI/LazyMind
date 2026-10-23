package mcpclient

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkBuddyRelocatesOnlySameHostManagedEntry(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "mcp.json")
	self, _ := os.Executable()
	oldPath := filepath.Join(home, "removed", "desktop", "dist", "mac-arm64", "LazyMind.app", "Contents", "Resources", "runtime", "bin", "lazymind")
	old := legacyTestStdio(oldPath, home, "host-test", WorkBuddy)
	original, _ := json.Marshal(map[string]any{"custom": "keep", "mcpServers": map[string]any{"lazymind": old, "other": map[string]string{"command": "keep-me"}}})
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	state, err := readManagedConfig(WorkBuddy, path, self, home, "host-test")
	if err != nil || !state.owned || state.current {
		t.Fatalf("migration state: %+v %v", state, err)
	}
	for _, mutate := range []func(*stdioMCPDefinition){
		func(v *stdioMCPDefinition) { v.Env["LAZYMIND_AGENT_HOST_ID"] = "foreign" },
		func(v *stdioMCPDefinition) { v.Env["LAZYMIND_HOME"] = "/other" },
		func(v *stdioMCPDefinition) { v.Command = "/unrelated/lazymind" },
	} {
		candidate := legacyTestStdio(old.Command, home, "host-test", WorkBuddy)
		mutate(&candidate)
		if stateForStdio(candidate, self, home, "host-test", WorkBuddy).owned {
			t.Fatal("foreign entry accepted")
		}
	}
	for i := 0; i < 2; i++ {
		if err := writeManagedConfig(WorkBuddy, path, self, home, "host-test", false); err != nil {
			t.Fatal(err)
		}
	}
	state, err = readManagedConfig(WorkBuddy, path, self, home, "host-test")
	if err != nil || !state.current {
		t.Fatalf("updated state: %+v %v", state, err)
	}
	root, servers, _ := readJSONConfig(path)
	var other map[string]string
	_ = json.Unmarshal(servers["other"], &other)
	if string(root["custom"]) != `"keep"` || other["command"] != "keep-me" {
		t.Fatal("unrelated config changed")
	}
	backup, _ := os.ReadFile(path + ".lazymind-backup")
	if string(backup) != string(original) {
		t.Fatal("original config backup lost")
	}
	if err := removeManagedConfig(WorkBuddy, path); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedConfig(WorkBuddy, path, self, home, "host-test", false); err != nil {
		t.Fatal(err)
	}
	_, servers, _ = readJSONConfig(path)
	if servers["other"] == nil || servers["lazymind"] == nil {
		t.Fatal("reconnect did not preserve config")
	}
}

func legacyTestStdio(self, home, host string, kind Kind) stdioMCPDefinition {
	return stdioMCPDefinition{Type: "stdio", Command: self, Args: []string{"mcp", "proxy"}, Env: map[string]string{"LAZYMIND_HOME": home, "LAZYMIND_AGENT_PROVIDER": string(kind), "LAZYMIND_AGENT_HOST_ID": host, "LAZYMIND_WEB_URL": "http://stale.example"}}
}
