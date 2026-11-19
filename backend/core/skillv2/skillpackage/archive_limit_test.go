package skillpackage

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveLimitIncludesPreambleBeforeReadingSelectedFiles(t *testing.T) {
	// A valid self-extracting ZIP can have a large sparse preamble and tiny
	// expanded contents. Expanded-file limits alone do not bound uploaded bytes.
	name := filepath.Join(t.TempDir(), "large.zip")
	file, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	const archiveLimit = 100 << 20
	if _, err := file.Seek(archiveLimit+1, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	writer.SetOffset(archiveLimit + 1)
	entry, err := writer.Create("SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(entry, "# Valid tiny skill\n"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.OpenReader(name)
	if err != nil {
		t.Fatalf("test archive is not valid: %v", err)
	}
	reader.Close()
	for _, read := range []func(string) (Package, error){ReadZip, func(name string) (Package, error) { return ReadGitHubZip(name, "") }} {
		if _, err := read(name); err == nil || !strings.Contains(err.Error(), "skill package archive exceeds") {
			t.Fatalf("oversized archive error=%v", err)
		}
	}
}
