package chat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatAttachmentConversionReplyMessageHidesSidechatPath(t *testing.T) {
	path := "/srv/lazymind/private/tenant-1/source.docx"
	err := errors.New("convert attachment " + path + ": service unavailable")

	sidechatMessage := chatAttachmentConversionReplyMessage(true, err)
	if sidechatMessage != "prepare chat attachments failed" || strings.Contains(sidechatMessage, path) {
		t.Fatalf("sidechat conversion response leaked path: %q", sidechatMessage)
	}
	ordinaryMessage := chatAttachmentConversionReplyMessage(false, err)
	if !strings.Contains(ordinaryMessage, path) {
		t.Fatalf("ordinary chat compatibility response lost detail: %q", ordinaryMessage)
	}
}

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

func TestResolveChatAttachmentFilesConvertsFilesGroupedByTurn(t *testing.T) {
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
	out, err := resolveChatAttachmentFiles(context.Background(), map[string]any{
		"1": []any{source},
	}, ocrConfig)
	if err != nil {
		t.Fatalf("resolveChatAttachmentFiles() error = %v", err)
	}
	turns, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected turn map, got %#v", out)
	}
	paths, ok := turns["1"].([]any)
	if !ok || len(paths) != 1 || paths[0] != pdf {
		t.Fatalf("expected reused pdf path %q, got %#v", pdf, turns["1"])
	}
}

func TestResolveChatAttachmentFilesKeepsOfficialMinerUPPTGroupedByTurn(t *testing.T) {
	pptx := "/tmp/sample.pptx"
	ocrConfig := map[string]any{
		"ocr_type": "mineru",
		"ocr_url":  "https://mineru.net/api/v4/",
	}
	out, err := resolveChatAttachmentFiles(context.Background(), map[string][]string{
		"1": {pptx},
	}, ocrConfig)
	if err != nil {
		t.Fatalf("resolveChatAttachmentFiles() error = %v", err)
	}
	turns, ok := out.(map[string][]string)
	if !ok || len(turns["1"]) != 1 || turns["1"][0] != pptx {
		t.Fatalf("expected original pptx path, got %#v", out)
	}
}
