package chat

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveChatAttachmentFilesKeepsOfficialMinerUPPT(t *testing.T) {
	pptx := "/tmp/sample.pptx"
	ocrConfig := map[string]any{
		"ocr_type": "mineru",
		"ocr_url":  "https://mineru.net/api/v4/",
	}
	out, err := resolveChatAttachmentFiles(context.Background(), []any{pptx}, ocrConfig)
	if err != nil {
		t.Fatalf("resolveChatAttachmentFiles() error = %v", err)
	}
	paths, ok := out.([]any)
	if !ok || len(paths) != 1 || paths[0] != pptx {
		t.Fatalf("expected original pptx path, got %#v", out)
	}
}

func TestResolveChatAttachmentFilesReusesExistingPDF(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "deck.pptx")
	pdf := filepath.Join(dir, "deck.pdf")
	if err := os.WriteFile(source, []byte("pptx"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(pdf, []byte("%PDF"), 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}

	ocrConfig := map[string]any{
		"ocr_type": "mineru",
		"ocr_url":  "http://local-mineru:8000/api/v1/pdf_parse",
	}
	out, err := resolveChatAttachmentFiles(context.Background(), []string{source}, ocrConfig)
	if err != nil {
		t.Fatalf("resolveChatAttachmentFiles() error = %v", err)
	}
	paths, ok := out.([]string)
	if !ok || len(paths) != 1 || paths[0] != pdf {
		t.Fatalf("expected reused pdf path %q, got %#v", pdf, out)
	}
}
