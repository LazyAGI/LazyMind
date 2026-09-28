package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/workflowhost"
)

func TestCodexWorkflowPair(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LAZYMIND_HOME", home)
	var output bytes.Buffer
	if err := runInternalSession([]string{"set"}, strings.NewReader(`{"server_url":"http://127.0.0.1:8090","access_token":"access","refresh_token":"refresh"}`), &output); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	profile := filepath.Join(home, "codex")
	if err := runInternal(context.Background(), []string{"codex-workflow-pair", "--codex-home", profile}, &output, &output); err != nil {
		t.Fatal(err)
	}
	var result map[string]string
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(result["pairing_file"])
	if err != nil {
		t.Fatal(err)
	}
	var pair workflowhost.Pairing
	if err := json.Unmarshal(body, &pair); err != nil || pair.Provider != "codex" || pair.Profile != profile || !pair.Enabled || len(pair.Token) != 64 {
		t.Fatalf("unexpected pairing shape: %v", err)
	}
	if bytes.Contains(output.Bytes(), []byte(pair.Token)) {
		t.Fatal("pairing command exposed its secret")
	}
	if err := runCodexWorkflowPair([]string{"--codex-home", "relative"}, &output, &output); err == nil {
		t.Fatal("relative profile path was accepted")
	}
}
