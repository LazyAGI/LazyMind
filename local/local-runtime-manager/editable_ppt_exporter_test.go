package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func exporterFixture(t *testing.T) (RuntimePaths, string, string) {
	t.Helper()
	root := t.TempDir()
	paths := RuntimePaths{RepoRoot: filepath.Join(root, "LazyMind.app", "app"), RuntimeRoot: filepath.Join(root, "user-data")}
	source := filepath.Join(paths.RepoRoot, "workflows", "ppt-workflow", "runtime", "scripts", "export_pptx")
	deps := filepath.Join(paths.RuntimeRoot, "deps", "editable-ppt")
	for _, dir := range []string{filepath.Join(source, "lib"), filepath.Join(deps, "node_modules")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{"html_to_pptx.mjs": "import './lib/helper.mjs';", "lib/helper.mjs": "export const version = 1;"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return paths, source, deps
}

func TestDesktopExporterEnvironmentKeepsSignedSourceUnchanged(t *testing.T) {
	paths, source, deps := exporterFixture(t)
	env := algorithmServiceEnv(RuntimeConfig{Profile: "desktop"}, paths, chatProcessName)
	var cli string
	for _, value := range env {
		if strings.HasPrefix(value, "LAZYMIND_PPT_EXPORT_CLI=") {
			cli = strings.TrimPrefix(value, "LAZYMIND_PPT_EXPORT_CLI=")
		}
	}
	if cli == "" || !pathIsUnderRoot(cli, deps) {
		t.Fatalf("desktop exporter must execute from user data: %s", cli)
	}
	if _, err := os.Lstat(filepath.Join(source, "node_modules")); !os.IsNotExist(err) {
		t.Fatalf("desktop startup modified sealed application resources: %v", err)
	}
	if target, ok := directoryLinkTarget(filepath.Join(filepath.Dir(cli), "node_modules")); !ok || !sameExporterDirectory(target, filepath.Join(deps, "node_modules")) {
		t.Fatalf("staged exporter dependencies not linked: %s, %v", target, ok)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(cli), "lib", "helper.mjs"))
	if err != nil || string(data) != "export const version = 1;" {
		t.Fatalf("exporter's relative imports were not materialized: %s, %v", data, err)
	}
}

func TestExporterUpgradeCreatesNewCopyAndPreservesPriorVersion(t *testing.T) {
	_, source, deps := exporterFixture(t)
	first, err := stageEditablePPTExporter(source, deps)
	if err != nil {
		t.Fatal(err)
	}
	reused, err := stageEditablePPTExporter(source, deps)
	if err != nil || reused != first {
		t.Fatalf("unchanged exporter not reused: %s, %v", reused, err)
	}
	if err := os.WriteFile(filepath.Join(source, "lib", "helper.mjs"), []byte("export const version = 2;"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := stageEditablePPTExporter(source, deps)
	if err != nil || first == second {
		t.Fatalf("changed exporter must use a new version: %s, %v", second, err)
	}
	data, err := os.ReadFile(filepath.Join(first, "lib", "helper.mjs"))
	if err != nil || string(data) != "export const version = 1;" {
		t.Fatalf("upgrade overwrote prior exporter's code: %s, %v", data, err)
	}
}

func TestDesktopExporterStagingFailureNeverFallsBackToWritingApp(t *testing.T) {
	paths, source, deps := exporterFixture(t)
	if err := os.WriteFile(filepath.Join(deps, "export-runtime"), []byte("blocked staging path"), 0o644); err != nil {
		t.Fatal(err)
	}
	algorithmServiceEnv(RuntimeConfig{Profile: "desktop"}, paths, chatProcessName)
	if _, err := os.Lstat(filepath.Join(source, "node_modules")); !os.IsNotExist(err) {
		t.Fatalf("staging failure wrote into signed app: %v", err)
	}
}

func TestLocalExporterKeepsSourceDependencyLink(t *testing.T) {
	paths, source, deps := exporterFixture(t)
	algorithmServiceEnv(RuntimeConfig{Profile: "local"}, paths, chatProcessName)
	if target, ok := directoryLinkTarget(filepath.Join(source, "node_modules")); !ok || !sameExporterDirectory(target, filepath.Join(deps, "node_modules")) {
		t.Fatalf("local source dependency link changed: %s, %v", target, ok)
	}
}
