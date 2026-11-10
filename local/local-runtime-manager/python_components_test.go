package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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

func TestPythonClusterComponentActivatesIndependentlyOfRAG(t *testing.T) {
	paths := RuntimePaths{ResourcesRoot: t.TempDir(), RuntimeRoot: t.TempDir(), RepoRoot: t.TempDir(), CoreBin: "bundled-core"}
	cfg := RuntimeConfig{Profile: "desktop", Algorithm: AlgorithmConfig{RAGDisabled: true}}
	runner := &fakeRunner{handlers: []func(Command) (CommandResult, error){
		func(cmd Command) (CommandResult, error) {
			if cmd.Name != paths.CoreBin || !slices.Equal(cmd.Args, []string{"--prepare-cluster-component"}) ||
				!slices.Contains(cmd.Env, "LAZYMIND_RUNTIME_ROOT="+paths.RuntimeRoot) ||
				!slices.Contains(cmd.Env, "LAZYMIND_PYTHON_COMPONENT_CATALOG="+filepath.Join(paths.ResourcesRoot, "config", "python-components.json")) {
				t.Fatalf("unexpected clustering preparation command: %+v", cmd)
			}
			return CommandResult{}, errors.New("offline")
		},
	}}
	manager := NewRuntimeManager(runner, "manager")
	writeJSON := func(path string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rag := pythonComponent{ID: "rag", Revision: strings.Repeat("a", 64), SHA256: strings.Repeat("b", 64), BaseFingerprint: strings.Repeat("c", 64)}
	cluster := pythonComponent{ID: "cluster", Revision: strings.Repeat("d", 64), SHA256: strings.Repeat("e", 64), BaseFingerprint: rag.BaseFingerprint,
		Platform: runtime.GOOS, Arch: runtime.GOARCH, PythonABI: "cp311", Packages: map[string]string{"umap-learn": "0.5.12"},
		CatalogURL: "https://offline.invalid/python-components.json"}
	cluster.Tag = cluster.Revision
	catalog := pythonComponentCatalog{SchemaVersion: 1, Platform: runtime.GOOS, Arch: runtime.GOARCH, Components: map[string]pythonComponent{"rag": rag}}
	catalogPath := filepath.Join(paths.ResourcesRoot, "config", "python-components.json")
	writeJSON(catalogPath, catalog)
	manager.prepareDesktopCluster(context.Background(), cfg, paths)
	if len(runner.calls) != 0 {
		t.Fatal("older installers must not attempt to download clustering")
	}
	if env := strings.Join(pythonComponentEnvironment(paths, true), "\n"); strings.Contains(env, "LAZYMIND_CLUSTER_ENABLED=") {
		t.Fatalf("older catalog unexpectedly set the cluster gate: %s", env)
	}
	catalog.Components[cluster.ID] = cluster
	writeJSON(catalogPath, catalog)
	manager.prepareDesktopCluster(context.Background(), cfg, paths)
	if len(runner.calls) != 1 {
		t.Fatal("missing clustering must be prepared at Desktop startup")
	}
	if !slices.Contains(coreServiceEnv(cfg, paths), "LAZYMIND_CLUSTER_ENABLED=0") ||
		!slices.Contains(algorithmServiceEnv(cfg, paths, chatProcessName), "LAZYMIND_CLUSTER_ENABLED=0") {
		t.Fatal("Core and Chat must both gate an uninstalled clustering component")
	}
	selected := cluster
	selected.Revision, selected.SHA256 = strings.Repeat("f", 64), strings.Repeat("1", 64)
	root := filepath.Join(paths.RuntimeRoot, "deps", "python-components", selected.ID, selected.Revision)
	site := filepath.Join(root, "site-packages")
	if err := os.MkdirAll(site, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(filepath.Join(root, "bundle-manifest.json"), selected)
	writeJSON(filepath.Join(filepath.Dir(root), "catalog-"+selected.Tag+".json"), selected)
	if sites := pythonComponentSites(paths); len(sites) != 0 {
		t.Fatalf("partial install added to import path: %v", sites)
	}
	if err := os.WriteFile(filepath.Join(root, ".ready"), []byte(selected.SHA256), 0o644); err != nil {
		t.Fatal(err)
	}
	manager.prepareDesktopCluster(context.Background(), cfg, paths)
	if len(runner.calls) != 1 {
		t.Fatal("installed clustering must be reused on restart")
	}
	for service, env := range map[string][]string{"Core": coreServiceEnv(cfg, paths), "Chat": algorithmServiceEnv(cfg, paths, chatProcessName)} {
		if !slices.Contains(env, "LAZYMIND_CLUSTER_ENABLED=1") || !slices.Contains(env, "LAZYMIND_RAG_ENABLED=0") ||
			!slices.Contains(env, "LAZYMIND_PYTHON_COMPONENT_PATHS="+site) {
			t.Fatalf("%s did not activate clustering independently of RAG", service)
		}
	}
	if sites := pythonComponentSites(paths); len(sites) != 1 || sites[0] != site {
		t.Fatalf("unexpected Python import paths: %v", sites)
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

func TestPythonComponentOnlineReceiptSurvivesOfflineRestart(t *testing.T) {
	paths := RuntimePaths{ResourcesRoot: t.TempDir(), RuntimeRoot: t.TempDir()}
	pinned := pythonComponent{ID: "rag", Revision: strings.Repeat("a", 64), Tag: strings.Repeat("a", 64),
		SHA256: strings.Repeat("b", 64), BaseFingerprint: strings.Repeat("c", 64),
		Platform: runtime.GOOS, Arch: runtime.GOARCH, PythonABI: "cp311", Packages: map[string]string{"orjson": "3.11.3"},
		CatalogURL: "https://offline.invalid/python-components.json"}
	catalog := pythonComponentCatalog{SchemaVersion: 1, Platform: runtime.GOOS, Arch: runtime.GOARCH, Components: map[string]pythonComponent{"rag": pinned}}
	write := func(path string, value any) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(value)
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(paths.ResourcesRoot, "config/python-components.json"), catalog)
	selected := pinned
	selected.Revision, selected.SHA256 = strings.Repeat("d", 64), strings.Repeat("e", 64)
	dir := filepath.Join(paths.RuntimeRoot, "deps/python-components/rag", selected.Revision)
	if err := os.MkdirAll(filepath.Join(dir, "site-packages"), 0755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(dir, "bundle-manifest.json"), selected)
	if err := os.WriteFile(filepath.Join(dir, ".ready"), []byte(selected.SHA256), 0644); err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(filepath.Dir(dir), "catalog-"+pinned.Tag+".json")
	write(receipt, selected)
	if sites := pythonComponentSites(paths); len(sites) != 1 || sites[0] != filepath.Join(dir, "site-packages") {
		t.Fatalf("receipt not reused: %v", sites)
	}
	selected.BaseFingerprint = strings.Repeat("f", 64)
	write(receipt, selected)
	if sites := pythonComponentSites(paths); len(sites) != 0 {
		t.Fatal("accepted incompatible receipt")
	}
}
