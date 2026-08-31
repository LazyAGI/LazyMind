package skillpatch

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	skillpackage "lazymind/core/skillv2/skillpackage"
)

func TestLoadCatalogAndApplyOperations(t *testing.T) {
	root := t.TempDir()
	files := map[string][]byte{
		"SKILL.md":       []byte("---\nname: demo\ndescription: demo\n---\n"),
		"scripts/run.py": []byte("print('old')\n"),
		"obsolete.txt":   []byte("remove me\n"),
	}
	writePatchFixture(t, root, files)

	catalog, err := LoadCatalog(filepath.Join(root, "catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := Apply(Target{
		UID:            "bsk_demo",
		Version:        "1.0.0",
		OriginTreeHash: skillpackage.TreeHash(files),
	}, files, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(result.Files["scripts/run.py"]); got != "print('patched')\n" {
		t.Fatalf("patched content = %q", got)
	}
	if _, exists := result.Files["obsolete.txt"]; exists {
		t.Fatal("deleted file is still present")
	}
	if got := string(files["scripts/run.py"]); got != "print('old')\n" {
		t.Fatalf("input files were mutated: %q", got)
	}
	if len(result.AppliedPatches) != 1 || result.AppliedPatches[0].ID != "demo/fix-runtime-v1" || result.PatchSetSHA256 == "" {
		t.Fatalf("patch provenance = %#v, set=%q", result.AppliedPatches, result.PatchSetSHA256)
	}
	if err := catalog.ValidateApplied(map[string]int{"demo/fix-runtime-v1": 1}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.ValidateApplied(nil); err == nil || !strings.Contains(err.Error(), "exactly once") {
		t.Fatalf("unused patch error = %v", err)
	}
}

func TestApplyRejectsOriginAndFileHashMismatch(t *testing.T) {
	root := t.TempDir()
	files := map[string][]byte{
		"SKILL.md":       []byte("---\nname: demo\ndescription: demo\n---\n"),
		"scripts/run.py": []byte("print('old')\n"),
		"obsolete.txt":   []byte("remove me\n"),
	}
	writePatchFixture(t, root, files)
	catalog, err := LoadCatalog(filepath.Join(root, "catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = Apply(Target{UID: "bsk_demo", Version: "1.0.0", OriginTreeHash: strings.Repeat("0", 64)}, files, catalog)
	if err == nil || !strings.Contains(err.Error(), "origin tree mismatch") {
		t.Fatalf("origin mismatch error = %v", err)
	}

	changed := cloneFiles(files)
	changed["scripts/run.py"] = []byte("print('changed')\n")
	_, err = Apply(Target{UID: "bsk_demo", Version: "1.0.0", OriginTreeHash: skillpackage.TreeHash(files)}, changed, catalog)
	if err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("file mismatch error = %v", err)
	}
}

func TestLoadCatalogRejectsUnknownFieldsAndUnsafePayloads(t *testing.T) {
	t.Run("unknown field", func(t *testing.T) {
		root := t.TempDir()
		writeFixtureFile(t, filepath.Join(root, "catalog.yaml"), "schema_version: 1\nunknown: true\npatches: []\n")
		_, err := LoadCatalog(filepath.Join(root, "catalog.yaml"))
		if err == nil || !strings.Contains(err.Error(), "field unknown not found") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("payload escapes files directory", func(t *testing.T) {
		root := t.TempDir()
		writeFixtureFile(t, filepath.Join(root, "catalog.yaml"), "schema_version: 1\npatches:\n  - demo/fix/patch.yaml\n")
		writeFixtureFile(t, filepath.Join(root, "demo", "fix", "patch.yaml"), `schema_version: 1
id: demo/fix
target:
  uid: bsk_demo
  version: 1.0.0
  origin_tree_sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
operations:
  - op: upsert
    path: SKILL.md
    file: payload.md
    before_sha256: absent
`)
		writeFixtureFile(t, filepath.Join(root, "demo", "fix", "payload.md"), "x")
		_, err := LoadCatalog(filepath.Join(root, "catalog.yaml"))
		if err == nil || !strings.Contains(err.Error(), "under files") {
			t.Fatalf("error = %v", err)
		}
	})
}

func writePatchFixture(t *testing.T, root string, files map[string][]byte) {
	t.Helper()
	runHash := sha256.Sum256(files["scripts/run.py"])
	obsoleteHash := sha256.Sum256(files["obsolete.txt"])
	writeFixtureFile(t, filepath.Join(root, "catalog.yaml"), "schema_version: 1\npatches:\n  - demo/fix-runtime-v1/patch.yaml\n")
	definition := `schema_version: 1
id: demo/fix-runtime-v1
description: test patch
target:
  uid: bsk_demo
  version: 1.0.0
  origin_tree_sha256: ` + skillpackage.TreeHash(files) + `
operations:
  - op: upsert
    path: scripts/run.py
    file: files/scripts/run.py
    before_sha256: ` + hex.EncodeToString(runHash[:]) + `
  - op: delete
    path: obsolete.txt
    before_sha256: ` + hex.EncodeToString(obsoleteHash[:]) + "\n"
	writeFixtureFile(t, filepath.Join(root, "demo", "fix-runtime-v1", "patch.yaml"), definition)
	writeFixtureFile(t, filepath.Join(root, "demo", "fix-runtime-v1", "files", "scripts", "run.py"), "print('patched')\n")
}

func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
