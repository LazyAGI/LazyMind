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
	env := strings.Join(pythonComponentEnvironment(paths, false), "\n")
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
	env = strings.Join(pythonComponentEnvironment(paths, false), "\n")
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

func TestLocalRAGFlagDisablesKnowledgeProcessesAndEnvironment(t *testing.T) {
	repo := t.TempDir()
	t.Setenv(runtimeRootEnvVar, t.TempDir())
	writeComposeFixture(t, repo)
	t.Setenv(ragEnabledEnvVar, "false")
	cfg, paths, err := NewRuntimeConfig("local", repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if !cfg.Algorithm.RAGDisabled {
		t.Fatal("RAG remained enabled")
	}
	if cfg.ModeProfile.VectorStore.ManagedProcess {
		t.Fatal("managed vector store remained enabled")
	}
	plan := buildRuntimeProcessPlan(cfg)
	for _, name := range []string{scanControlPlaneProcessName, fileWatcherProcessName, milvusLiteProcessName, processorServerProcessName, processorWorkerProcessName, algoProcessName, docServerProcessName} {
		if plan.includes(name) {
			t.Fatalf("started disabled RAG service %s", name)
		}
	}
	if !plan.includes(chatProcessName) || !plan.includes(coreProcessName) {
		t.Fatal("disabled RAG removed a base chat service")
	}
	if env := strings.Join(pythonComponentEnvironment(paths, cfg.Algorithm.RAGDisabled), "\n"); !strings.Contains(env, ragEnabledEnvVar+"=0") {
		t.Fatalf("RAG disable flag missing from service environment: %s", env)
	}
	marker := filepath.Join(paths.RuntimeRoot, "deps", "python-components", "rag", "source.ready")
	if err := os.MkdirAll(filepath.Dir(marker), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(paths.AlgorithmPython), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, paths, err = NewRuntimeConfig("local", repo)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Algorithm.RAGDisabled {
		t.Fatal("manual installation must override initial opt-out")
	}
	if env := strings.Join(pythonComponentEnvironment(paths, false), "\n"); !strings.Contains(env, ragEnabledEnvVar+"=1") {
		t.Fatal(env)
	}
}
