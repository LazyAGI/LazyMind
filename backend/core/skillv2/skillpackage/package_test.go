package skillpackage

import (
	"archive/zip"
	"bytes"
	"fmt"
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
	pkg, err := ReadZip(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	files := pkg.Files
	if pkg.PackageRoot != "wrapped" {
		t.Fatalf("PackageRoot = %q, want wrapped", pkg.PackageRoot)
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

func TestReadZipNormalizesRepositoryRootWithoutRootSkillMD(t *testing.T) {
	zipPath := writeZip(t, map[string]string{
		"repository-ref/skills/target/SKILL.md": "content",
		"repository-ref/skills/other/SKILL.md":  "other",
	})
	pkg, err := ReadZip(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	files := pkg.Files
	if pkg.PackageRoot != "repository-ref" {
		t.Fatalf("PackageRoot = %q, want repository-ref", pkg.PackageRoot)
	}
	if string(files["skills/target/SKILL.md"]) != "content" || string(files["repository-ref/skills/target/SKILL.md"]) != "" {
		t.Fatalf("unexpected normalized files: %#v", files)
	}
}

func TestReadZipSubdirectorySelectsSkillFromLargeRepositoryArchive(t *testing.T) {
	entries := map[string]string{
		"repository-main/project-management/execution/summarize-meeting/SKILL.md":            "---\nname: summarize-meeting\ndescription: Summarize meetings.\n---\n",
		"repository-main/project-management/execution/summarize-meeting/scripts/run.py":      "print('ok')\n",
		"repository-main/project-management/execution/other/SKILL.md":                        "other",
		"repository-main/project-management/execution/summarize-meeting/.DS_Store":           "metadata",
		"__MACOSX/repository-main/project-management/execution/summarize-meeting/._SKILL.md": "metadata",
	}
	for index := 0; index < MaxFiles+10; index++ {
		entries[fmt.Sprintf("repository-main/unrelated/file-%03d.txt", index)] = "unrelated"
	}

	pkg, err := ReadZipSubdirectory(
		writeZip(t, entries),
		"project-management/execution/summarize-meeting",
	)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.PackageRoot != "summarize-meeting" {
		t.Fatalf("PackageRoot = %q, want summarize-meeting", pkg.PackageRoot)
	}
	if string(pkg.Files["SKILL.md"]) == "" || string(pkg.Files["scripts/run.py"]) != "print('ok')\n" {
		t.Fatalf("selected files = %#v", pkg.Files)
	}
	if len(pkg.Files) != 2 {
		t.Fatalf("selected file count = %d, want 2", len(pkg.Files))
	}
}

func TestReadZipSubdirectoryRejectsMissingSkillAndUnsafePrefix(t *testing.T) {
	zipPath := writeZip(t, map[string]string{
		"repository-main/skills/other/SKILL.md": "other",
	})
	if _, err := ReadZipSubdirectory(zipPath, "skills/target"); err == nil || !strings.Contains(err.Error(), "must contain SKILL.md") {
		t.Fatalf("missing subdirectory Skill error = %v", err)
	}
	if _, err := ReadZipSubdirectory(zipPath, "../skills/other"); err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("unsafe prefix error = %v", err)
	}
}

func TestReadZipIgnoresSystemMetadataAndNormalizesRoot(t *testing.T) {
	zipPath := writeZip(t, map[string]string{
		"wrapped/SKILL.md":            "---\nname: demo\ndescription: demo\n---\n",
		"wrapped/scripts/run.py":      "print('ok')\n",
		"wrapped/.DS_Store":           "finder metadata",
		"__MACOSX/wrapped/._SKILL.md": "macOS metadata",
		"wrapped/Thumbs.db":           "windows thumbnail",
	})
	pkg, err := ReadZip(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	files := pkg.Files
	if pkg.PackageRoot != "wrapped" {
		t.Fatalf("PackageRoot = %q, want wrapped", pkg.PackageRoot)
	}
	if string(files["SKILL.md"]) == "" || string(files["scripts/run.py"]) == "" {
		t.Fatalf("unexpected normalized files: %#v", files)
	}
	for _, name := range []string{".DS_Store", "Thumbs.db", "__MACOSX/wrapped/._SKILL.md", "wrapped/.DS_Store"} {
		if _, ok := files[name]; ok {
			t.Fatalf("kept system metadata file %q in %#v", name, files)
		}
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

func TestReadZipRejectsDuplicateAndSymlinkEntries(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "duplicate.zip")
	file, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, content := range []string{"first", "second"} {
		entry, err := writer.Create("SKILL.md")
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
	if _, err := ReadZip(zipPath); err == nil || !strings.Contains(err.Error(), "duplicate path") {
		t.Fatalf("duplicate ReadZip error = %v", err)
	}

	symlinkPath := filepath.Join(t.TempDir(), "symlink.zip")
	symlinkFile, err := os.Create(symlinkPath)
	if err != nil {
		t.Fatal(err)
	}
	symlinkWriter := zip.NewWriter(symlinkFile)
	header := &zip.FileHeader{Name: "SKILL.md"}
	header.SetMode(os.ModeSymlink | 0o777)
	entry, err := symlinkWriter.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("target")); err != nil {
		t.Fatal(err)
	}
	if err := symlinkWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := symlinkFile.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadZip(symlinkPath); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink ReadZip error = %v", err)
	}
}

func TestWriteZipIsDeterministicAndReadable(t *testing.T) {
	files := map[string][]byte{
		"SKILL.md":       []byte("---\nname: demo\ndescription: demo\n---\n"),
		"scripts/run.py": []byte("print('ok')\n"),
	}
	first, err := WriteZip(files, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := WriteZip(files, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	firstBody, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBody, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBody, secondBody) {
		t.Fatal("deterministic archives differ")
	}
	read, err := ReadZip(first)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(read.Files["scripts/run.py"], files["scripts/run.py"]) {
		t.Fatalf("archive content = %q", read.Files["scripts/run.py"])
	}
}

func TestWriteZipRejectsUnsafePath(t *testing.T) {
	_, err := WriteZip(map[string][]byte{"../SKILL.md": []byte("x")}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("error = %v", err)
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
