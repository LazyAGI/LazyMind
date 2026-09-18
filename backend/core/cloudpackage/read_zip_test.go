package cloudpackage

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestReadZIPValidatesAndReturnsWorkflowFiles(t *testing.T) {
	path := writeReadZIPFixture(t, map[string]string{
		"workflow.yaml": "id: fixture\n", "scenario/state.yml": "transitions: {}\n", "scenario/scenario.md": "# fixture\n",
	})
	files, err := ReadZIP(path, "workflow", ZIPLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || string(files["workflow.yaml"].Data) != "id: fixture\n" {
		t.Fatalf("files = %+v", files)
	}
}

func TestReadZIPRejectsUnsafeAndDuplicatePaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unsafe.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, name := range []string{"SKILL.md", "../outside"} {
		entry, createErr := writer.Create(name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		_, _ = entry.Write([]byte("fixture"))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadZIP(path, "skill", ZIPLimits{}); err == nil {
		t.Fatal("unsafe ZIP path must fail")
	}
}

func TestReadAndVerifyZIPRejectsExpandedContentMismatch(t *testing.T) {
	path := writeReadZIPFixture(t, map[string]string{"SKILL.md": "fixture"})
	if _, err := ReadAndVerifyZIP(VerifyZIPInput{
		ZIPPath: path, ResourceType: "skill", ResourceName: "fixture", ClientResourceKey: "skill:a",
		DesktopVersion: "1.0.0", ExpectedContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ExpectedContentSize: 7,
	}); err == nil {
		t.Fatal("expanded content hash mismatch must fail")
	}
}

func writeReadZIPFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, body := range files {
		entry, createErr := writer.Create(name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
