package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPythonComponentsLegacyAndDeferredProcessPlans(t *testing.T) {
	cfg := RuntimeConfig{Algorithm: AlgorithmConfig{RAGDisabled: true}}
	cfg.ModeProfile.VectorStore.ManagedProcess = false
	plan := buildRuntimeProcessPlan(cfg)
	if !plan.includes(chatProcessName) || !plan.includes(coreProcessName) {
		t.Fatal("base services missing")
	}
	for _, name := range []string{scanControlPlaneProcessName, fileWatcherProcessName, milvusLiteProcessName, processorWorkerProcessName, algoProcessName, docServerProcessName} {
		if plan.includes(name) {
			t.Fatalf("started optional service %s", name)
		}
	}
	cfg.Algorithm.RAGDisabled = false
	cfg.ModeProfile.VectorStore.ManagedProcess = true
	plan = buildRuntimeProcessPlan(cfg)
	if !plan.includes(milvusLiteProcessName) || !plan.includes(processorWorkerProcessName) {
		t.Fatal("RAG not restored")
	}
}

func TestPythonComponentEnvironmentRequiresAtomicCompatibleInstallation(t *testing.T) {
	paths := RuntimePaths{ResourcesRoot: t.TempDir(), RuntimeRoot: t.TempDir(), AlgorithmPython: "python"}
	if sites := pythonComponentSites(paths); len(sites) != 0 {
		t.Fatal(sites)
	}
	catalog := pythonComponentCatalog{SchemaVersion: 1, Platform: runtime.GOOS, Arch: runtime.GOARCH, Components: map[string]pythonComponent{}}
	for _, id := range []string{"rag"} {
		catalog.Components[id] = pythonComponent{ID: id, Revision: strings.Repeat("a", 64), SHA256: strings.Repeat("b", 64), BaseFingerprint: strings.Repeat("c", 64)}
	}
	data, _ := json.Marshal(catalog)
	os.MkdirAll(filepath.Join(paths.ResourcesRoot, "config"), 0755)
	os.WriteFile(filepath.Join(paths.ResourcesRoot, "config/python-components.json"), data, 0644)
	env := strings.Join(pythonComponentEnvironment(paths), "\n")
	if !strings.Contains(env, "LAZYMIND_RAG_ENABLED=0") {
		t.Fatal(env)
	}
	entry := catalog.Components["rag"]
	dir := filepath.Join(paths.RuntimeRoot, "deps/python-components/rag", entry.Revision)
	os.MkdirAll(filepath.Join(dir, "site-packages"), 0755)
	data, _ = json.Marshal(entry)
	os.WriteFile(filepath.Join(dir, "bundle-manifest.json"), data, 0644)
	if len(pythonComponentSites(paths)) != 0 {
		t.Fatal("accepted incomplete installation")
	}
	os.WriteFile(filepath.Join(dir, ".ready"), []byte(entry.SHA256), 0644)
	env = strings.Join(pythonComponentEnvironment(paths), "\n")
	if !strings.Contains(env, "LAZYMIND_RAG_ENABLED=1") {
		t.Fatal(env)
	}
	if sites := pythonComponentSites(paths); len(sites) != 1 || sites[0] != filepath.Join(dir, "site-packages") {
		t.Fatal(sites)
	}
	entry.BaseFingerprint = strings.Repeat("d", 64)
	data, _ = json.Marshal(entry)
	os.WriteFile(filepath.Join(dir, "bundle-manifest.json"), data, 0644)
	if len(pythonComponentSites(paths)) != 0 {
		t.Fatal("accepted another build's component")
	}
}
