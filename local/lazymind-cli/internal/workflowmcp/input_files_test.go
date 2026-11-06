package workflowmcp

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/connectorconfig"
)

func TestInputImportUsesExplicitPathOutsideProcessDirectory(t *testing.T) {
	t.Setenv(connectorconfig.EnvironmentKey, "")
	path := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(path, []byte("conversation source"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := readLocalFile(path)
	if err != nil || file.Name != "source.txt" || file.Base64 != base64.StdEncoding.EncodeToString([]byte("conversation source")) {
		t.Fatalf("external workspace file unavailable: %+v %v", file, err)
	}
	if _, err := readLocalFile("source.txt"); err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("relative source used process cwd: %v", err)
	}
}

func TestConfiguredInputRootsRejectOutsideFilesAndSymlinkEscapes(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "source.txt")
	outsideRoot := root + "-outside"
	if err := os.Mkdir(outsideRoot, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outsideRoot) })
	outside := filepath.Join(outsideRoot, "source.txt")
	for _, path := range []string{inside, outside} {
		if err := os.WriteFile(path, []byte("source"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	config := connectorconfig.Config{Home: t.TempDir(), Provider: "cursor", HostID: "host", WorkflowInputRoots: []string{root}}
	configPath, err := connectorconfig.Save(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(connectorconfig.EnvironmentKey, configPath)
	if _, err := readLocalFile(inside); err != nil {
		t.Fatal(err)
	}
	if _, err := readLocalFile(outside); err == nil || !strings.Contains(err.Error(), "outside configured") {
		t.Fatalf("sibling prefix escaped input roots: %v", err)
	}
	link := filepath.Join(root, "linked.txt")
	if err := os.Symlink(outside, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Log("symlink creation unavailable:", err)
			return
		}
		t.Fatal(err)
	}
	if _, err := readLocalFile(link); err == nil || !strings.Contains(err.Error(), "outside configured") {
		t.Fatalf("symlink escaped input roots: %v", err)
	}
}

func TestInputImportRejectsMalformedRootConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(path, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "connection.json")
	if err := os.WriteFile(configPath, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(connectorconfig.EnvironmentKey, configPath)
	if _, err := readLocalFile(path); err == nil || !strings.Contains(err.Error(), "decode connection configuration") {
		t.Fatalf("malformed file policy ignored: %v", err)
	}
}
