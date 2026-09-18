package doc

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlainDocumentExecutorPreservesParagraphSeparators(t *testing.T) {
	executor := newPlainDocumentExecutor([]byte("First paragraph.\n\nSecond paragraph."))
	if len(executor.Units()) != 2 {
		t.Fatalf("units = %d, want 2", len(executor.Units()))
	}
	output := filepath.Join(t.TempDir(), "translated.md")
	translations := map[string]string{executor.Units()[0].ID: "第一段。", executor.Units()[1].ID: "第二段。"}
	if _, err := executor.Build(context.Background(), translations, output); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(output)
	if string(raw) != "第一段。\n\n第二段。" {
		t.Fatalf("output = %q", raw)
	}
}

func TestMarkdownDocumentExecutorPreservesStructureAndSpecialSyntax(t *testing.T) {
	source := "---\ntitle: Keep me\n---\n# Title\n\n## Section *with emphasis* ##\n\n> - [x] Nested item with `code`, $x^2$, and [docs](https://example.com/a_(b))\n\n| Name | Value |\n| :--- | ---: |\n| Alpha | `x=1` |\n\n![diagram](./diagram.png)\n\n[ref]: https://example.com\n\n```go\n# not a heading\nfmt.Println(\"hello\")\n```\n"
	executor := newMarkdownDocumentExecutor([]byte(source))
	if len(executor.Units()) == 0 {
		t.Fatal("expected translatable Markdown units")
	}
	for _, unit := range executor.Units() {
		if strings.Contains(unit.Text, "https://") || strings.Contains(unit.Text, "`code`") || strings.Contains(unit.Text, "```") {
			t.Fatalf("syntax leaked into translation unit: %q", unit.Text)
		}
	}

	translations := make(map[string]string, len(executor.Units()))
	for _, unit := range executor.Units() {
		translations[unit.ID] = "译（" + unit.Text + "）"
	}
	output := filepath.Join(t.TempDir(), "translated.md")
	if _, err := executor.Build(context.Background(), translations, output); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, unchanged := range []string{
		"---\ntitle: Keep me\n---", "# ", "## ", " ##\n", "> - [x] ", "`code`", "$x^2$",
		"[docs](https://example.com/a_(b))", "| :--- | ---: |", "![diagram](./diagram.png)",
		"[ref]: https://example.com", "```go\n# not a heading\nfmt.Println(\"hello\")\n```",
	} {
		if !strings.Contains(got, unchanged) {
			t.Fatalf("translated Markdown lost %q:\n%s", unchanged, got)
		}
	}
	if !strings.Contains(got, "# 译（Title）") || !strings.Contains(got, "> - [x] 译（Nested item with） `code`") ||
		!strings.Contains(got, "| 译（Name） | 译（Value） |") {
		t.Fatalf("visible prose was not translated in place:\n%s", got)
	}
}

func TestMarkdownDocumentExecutorPreservesCRLFAndMissingTranslations(t *testing.T) {
	source := "# Heading\r\n\r\nParagraph with **bold**.\r\n"
	executor := newMarkdownDocumentExecutor([]byte(source))
	output := filepath.Join(t.TempDir(), "translated.markdown")
	translations := map[string]string{executor.Units()[0].ID: "标题"}
	if _, err := executor.Build(context.Background(), translations, output); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(output)
	if string(raw) != "# 标题\r\n\r\nParagraph with **bold**.\r\n" {
		t.Fatalf("output = %q", raw)
	}
}

func TestBackendDocumentTranslationExecutorSelectsMarkdownExecutor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "README.md")
	if err := os.WriteFile(path, []byte("# Title\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	executor, err := newBackendDocumentTranslationExecutor(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := executor.(*markdownDocumentExecutor); !ok {
		t.Fatalf("executor = %T, want *markdownDocumentExecutor", executor)
	}
}

func TestHTMLDocumentExecutorPreservesMarkupAndScripts(t *testing.T) {
	executor := newHTMLDocumentExecutor([]byte(`<p>Hello</p><script>const label = "Keep";</script><strong>World</strong>`))
	if len(executor.Units()) != 2 {
		t.Fatalf("units = %d, want 2", len(executor.Units()))
	}
	output := filepath.Join(t.TempDir(), "translated.html")
	translations := map[string]string{executor.Units()[0].ID: "你好", executor.Units()[1].ID: "世界"}
	if _, err := executor.Build(context.Background(), translations, output); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(output)
	got := string(raw)
	if !strings.Contains(got, `<p>你好</p>`) || !strings.Contains(got, `const label = "Keep";`) || !strings.Contains(got, `<strong>世界</strong>`) {
		t.Fatalf("unexpected HTML: %s", got)
	}
}

func TestOpenXMLDocumentExecutorKeepsPackageAndReplacesParagraphText(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.docx")
	out, err := os.Create(source)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(out)
	content, _ := zw.Create("[Content_Types].xml")
	_, _ = content.Write([]byte(`<Types/>`))
	document, _ := zw.Create("word/document.xml")
	_, _ = document.Write([]byte(`<w:document><w:body><w:p><w:r><w:t>Hello </w:t></w:r><w:r><w:t>world</w:t></w:r></w:p></w:body></w:document>`))
	_ = zw.Close()
	_ = out.Close()

	executor, err := newOpenXMLDocumentExecutor(source, ".docx")
	if err != nil {
		t.Fatal(err)
	}
	if len(executor.Units()) != 1 || executor.Units()[0].Text != "Hello world" {
		t.Fatalf("unexpected units: %+v", executor.Units())
	}
	target := filepath.Join(dir, "target.docx")
	if _, err := executor.Build(context.Background(), map[string]string{executor.Units()[0].ID: "你好，世界"}, target); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(target)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	if len(zr.File) != 2 {
		t.Fatalf("package entries = %d, want 2", len(zr.File))
	}
	for _, file := range zr.File {
		if file.Name != "word/document.xml" {
			continue
		}
		reader, _ := file.Open()
		raw, _ := io.ReadAll(reader)
		_ = reader.Close()
		if !strings.Contains(string(raw), "你好，世界") || strings.Contains(string(raw), "world") {
			t.Fatalf("unexpected document XML: %s", raw)
		}
	}
}
