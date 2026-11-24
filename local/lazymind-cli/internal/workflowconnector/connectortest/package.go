// Package connectortest creates synthetic sidecars for isolated installation tests.
package connectortest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"lazymind/agentconnector/internal/workflowconnector"
)

func WritePackage(t testing.TB, executable, host, version string, files map[string]string) workflowconnector.Manifest {
	t.Helper()
	dir := filepath.Join(filepath.Dir(executable), "workflow-connectors", host)
	hash := sha256.New()
	keys := slices.Sorted(maps.Keys(files))
	for _, name := range keys {
		data := []byte(files[name])
		sum := sha256.Sum256(data)
		hash.Write([]byte(name))
		hash.Write([]byte{0})
		hash.Write(sum[:])
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := workflowconnector.Manifest{Schema: 1, Host: host, Version: version, Release: hex.EncodeToString(hash.Sum(nil)),
		Entry: "main.mjs", Control: "adapter.mjs", Presentation: "presentation.mjs", Files: keys}
	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "connector.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func WriteMinimalPackage(t testing.TB, executable, host string) {
	t.Helper()
	WritePackage(t, executable, host, "0.1.0", map[string]string{
		"main.mjs": "", "adapter.mjs": "", "presentation.mjs": "",
	})
}
