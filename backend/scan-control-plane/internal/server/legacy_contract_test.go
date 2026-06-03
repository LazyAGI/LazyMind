package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWritableScanAPICodeHasNoLegacyTerms(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	for _, dir := range []string{
		filepath.Join(root, "internal", "server"),
		filepath.Join(root, "internal", "sourceengine", "source"),
		filepath.Join(root, "internal", "sourceengine", "tree"),
	} {
		assertNoLegacyTerms(t, dir)
	}
}

func assertNoLegacyTerms(t *testing.T, dir string) {
	t.Helper()

	forbidden := []string{"cloud" + "_", "Origin" + "Type", "root" + "_path", "cloud" + "sync"}
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, term := range forbidden {
			if strings.Contains(string(content), term) {
				t.Fatalf("legacy term %q found in %s", term, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
}
