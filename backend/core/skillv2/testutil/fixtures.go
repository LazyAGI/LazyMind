package testutil

import (
	"archive/zip"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func MinimalPNGBytes() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
		0x42, 0x60, 0x82,
	}
}

func DefaultSkillZipFiles() map[string][]byte {
	return map[string][]byte{
		"SKILL.md":        []byte("# 论文精读\n\n用于阅读和总结论文。\n"),
		"references/a.md": []byte("# 参考资料\n\n这是参考资料。\n"),
		"scripts/run.py":  []byte("print(\"hello skill\")\n"),
		"assets/logo.png": MinimalPNGBytes(),
	}
}

func WriteSkillZip(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	writeZip(t, path, files)
}

func WriteUnsafeSkillZip(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	writeZip(t, path, files)
}

func writeZip(t *testing.T, path string, files map[string][]byte) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create zip dir: %v", err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	defer file.Close()

	writer := zip.NewWriter(file)
	defer writer.Close()

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %q: %v", name, err)
		}
		if _, err := entry.Write(files[name]); err != nil {
			t.Fatalf("write zip entry %q: %v", name, err)
		}
	}
}

func BoolPtr(v bool) *bool {
	return &v
}

func StringPtr(v string) *string {
	return &v
}
