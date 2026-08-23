package builtin

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	skillpackage "lazymind/core/skillv2/skillpackage"
)

func TestCatalogListsMetadataAndLoadsVerifiedArchiveOnDemand(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "packages", "demo.zip")
	files := map[string]string{
		"SKILL.md":       "---\nname: demo\ndescription: catalog demo\n---\n# Demo\n",
		"scripts/run.py": "print('ok')\n",
	}
	writeCatalogZip(t, archivePath, files)
	body, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(body)
	catalogPath := filepath.Join(root, "catalog.json")
	writeCatalog(t, catalogPath, CatalogSkill{
		UID:           "bsk_demo",
		Key:           "demo",
		SourceURL:     "https://example.test/demo.zip",
		ResolvedURL:   "https://example.test/demo.zip",
		Version:       "1.0.0",
		Name:          "demo",
		Description:   "catalog demo",
		Category:      "external",
		Content:       files["SKILL.md"],
		ArchiveSHA256: hex.EncodeToString(hash[:]),
		TreeSHA256: skillpackage.TreeHash(map[string][]byte{
			"SKILL.md":       []byte(files["SKILL.md"]),
			"scripts/run.py": []byte(files["scripts/run.py"]),
		}),
		ArchiveSize: int64(len(body)),
		PackageFile: "packages/demo.zip",
	})

	listed, err := catalogPackages(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Version != "1.0.0" || len(listed[0].Files) != 1 {
		t.Fatalf("listed package = %#v", listed)
	}
	loaded, found, err := catalogPackageByUID(catalogPath, "bsk_demo")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if string(loaded.Files["scripts/run.py"]) != files["scripts/run.py"] || loaded.ArchivePath != archivePath {
		t.Fatalf("loaded package = %#v", loaded)
	}

	if err := os.WriteFile(archivePath, append(body, 'x'), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err = catalogPackageByUID(catalogPath, "bsk_demo")
	if err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("tampered archive error = %v", err)
	}
}

func TestCatalogPathCandidatesCoverLocalAndDesktopLayouts(t *testing.T) {
	localWorkingDirectory := filepath.Join(string(filepath.Separator), "repo", "backend", "core")
	local := catalogPathCandidates(localWorkingDirectory, "builtin-skills")
	if got, want := filepath.Clean(local[1]), filepath.Join(string(filepath.Separator), "repo", "skills", ".runtime", "builtin-skills", "catalog.json"); got != want {
		t.Fatalf("local catalog path = %q, want %q", got, want)
	}
	desktopWorkingDirectory := filepath.Join(string(filepath.Separator), "resources", "runtime", "app", "backend", "core")
	desktop := catalogPathCandidates(desktopWorkingDirectory, "builtin-skills")
	if got, want := filepath.Clean(desktop[2]), filepath.Join(string(filepath.Separator), "resources", "runtime", "builtin-skills", "catalog.json"); got != want {
		t.Fatalf("desktop catalog path = %q, want %q", got, want)
	}
}

func writeCatalog(t *testing.T, path string, entry CatalogSkill) {
	t.Helper()
	body, err := json.Marshal(Catalog{SchemaVersion: CatalogSchemaVersion, Skills: []CatalogSkill{entry}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeCatalogZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
