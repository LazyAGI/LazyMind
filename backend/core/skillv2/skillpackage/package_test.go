package skillpackage

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadZipNormalizesSingleRootAndHashesDeterministically(t *testing.T) {
	zipPath := writeZip(t, map[string]string{
		"wrapped/SKILL.md":       "---\nname: demo\ndescription: demo\n---\n",
		"wrapped/scripts/run.py": "print('ok')\n",
	})
	files, err := ReadZip(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(files["SKILL.md"]) == "" || string(files["scripts/run.py"]) == "" {
		t.Fatalf("unexpected normalized files: %#v", files)
	}
	if TreeHash(files) != TreeHash(map[string][]byte{
		"scripts/run.py": files["scripts/run.py"],
		"SKILL.md":       files["SKILL.md"],
	}) {
		t.Fatal("tree hash depends on map iteration order")
	}
}

func TestReadZipRejectsUnsafeAndOversizedEntries(t *testing.T) {
	tests := []struct {
		name    string
		entries map[string]string
		want    string
	}{
		{name: "traversal", entries: map[string]string{"../SKILL.md": "x"}, want: "unsafe path"},
		{name: "backslash", entries: map[string]string{`dir\SKILL.md`: "x"}, want: "unsafe path"},
		{name: "large file", entries: map[string]string{"SKILL.md": strings.Repeat("x", MaxFileBytes+1)}, want: "exceeds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ReadZip(writeZip(t, tt.entries))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func writeZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	zipPath := filepath.Join(t.TempDir(), "skill.zip")
	file, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, content := range entries {
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
	return zipPath
}
