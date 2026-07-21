package doc

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"
)

func TestNormalizeUploadedTextFileConvertsGBKInPlace(t *testing.T) {
	t.Setenv(uploadTextUTF8ConvertEnv, "true")

	path := filepath.Join(t.TempDir(), "notes.TXT")
	gbk := []byte{'h', 'i', ',', 0xd6, 0xd0, 0xce, 0xc4, '\n'}
	if err := os.WriteFile(path, gbk, 0o644); err != nil {
		t.Fatalf("write GBK fixture: %v", err)
	}

	size, err := normalizeUploadedTextFileInPlace(path, "notes.TXT", int64(len(gbk)))
	if err != nil {
		t.Fatalf("normalize uploaded text file: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read normalized file: %v", err)
	}
	if !utf8.Valid(got) {
		t.Fatalf("normalized file is not UTF-8: %x", got)
	}
	if string(got) != "hi,中文\n" {
		t.Fatalf("unexpected normalized content: %q", string(got))
	}
	if size != int64(len(got)) {
		t.Fatalf("expected returned size %d, got %d", len(got), size)
	}
}

func TestNormalizeUploadedTextFileKeepsUTF8(t *testing.T) {
	t.Setenv(uploadTextUTF8ConvertEnv, "true")

	path := filepath.Join(t.TempDir(), "notes.md")
	utf8Data := []byte("hi,中文\n")
	if err := os.WriteFile(path, utf8Data, 0o644); err != nil {
		t.Fatalf("write UTF-8 fixture: %v", err)
	}

	size, err := normalizeUploadedTextFileInPlace(path, "notes.md", int64(len(utf8Data)))
	if err != nil {
		t.Fatalf("normalize uploaded text file: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read normalized file: %v", err)
	}
	if !bytes.Equal(got, utf8Data) {
		t.Fatalf("UTF-8 file should remain unchanged: %x", got)
	}
	if size != int64(len(utf8Data)) {
		t.Fatalf("expected returned size %d, got %d", len(utf8Data), size)
	}
}

func TestNormalizeUploadedTextFileSkipsWhenDisabled(t *testing.T) {
	t.Setenv(uploadTextUTF8ConvertEnv, "false")

	path := filepath.Join(t.TempDir(), "notes.csv")
	gbk := []byte{'h', 'i', ',', 0xd6, 0xd0, 0xce, 0xc4, '\n'}
	if err := os.WriteFile(path, gbk, 0o644); err != nil {
		t.Fatalf("write GBK fixture: %v", err)
	}

	size, err := normalizeUploadedTextFileInPlace(path, "notes.csv", int64(len(gbk)))
	if err != nil {
		t.Fatalf("normalize uploaded text file: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if !bytes.Equal(got, gbk) {
		t.Fatalf("disabled conversion should leave file unchanged: %x", got)
	}
	if size != int64(len(gbk)) {
		t.Fatalf("expected returned size %d, got %d", len(gbk), size)
	}
}

func TestNormalizeUploadedTextFileSkipsUnsupportedExtension(t *testing.T) {
	t.Setenv(uploadTextUTF8ConvertEnv, "true")

	path := filepath.Join(t.TempDir(), "report.pdf")
	data := []byte{0xff, 0xfe, 0x00, 0x01}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write binary fixture: %v", err)
	}

	size, err := normalizeUploadedTextFileInPlace(path, "report.pdf", int64(len(data)))
	if err != nil {
		t.Fatalf("normalize uploaded text file: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("unsupported extension should leave file unchanged: %x", got)
	}
	if size != int64(len(data)) {
		t.Fatalf("expected returned size %d, got %d", len(data), size)
	}
}

func TestNormalizeUploadedTextFileSupportsCommonSourceAndConfigExtensions(t *testing.T) {
	for _, name := range []string{"config.yaml", "app.toml", "main.py", "view.tsx", "query.sql", ".env"} {
		if !shouldNormalizeUploadedTextFile("", name) {
			t.Errorf("expected %q to be treated as a text upload", name)
		}
	}
}
