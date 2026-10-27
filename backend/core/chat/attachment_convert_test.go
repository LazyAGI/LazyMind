package chat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestResolveChatAttachmentFilesWithoutOfficeService(t *testing.T) {
	t.Setenv("LAZYMIND_OFFICE_CONVERT_URL", "")
	for _, ocrConfig := range []map[string]any{
		nil,
		{"ocr_type": "paddleocr"},
		{"ocr_type": "mineru", "ocr_url": "http://local-mineru:8000"},
	} {
		for _, files := range []any{
			[]string{"/tmp/native.docx", "/tmp/native.PPTX", "/tmp/native.pdf"},
			[]any{"/tmp/native.docx", "/tmp/native.pptx"},
			map[string]any{"1": []any{"/tmp/native.docx"}},
			map[string][]string{"1": {"/tmp/native.pptx"}},
		} {
			got, err := resolveChatAttachmentFiles(context.Background(), files, ocrConfig)
			if err != nil || !reflect.DeepEqual(got, files) {
				t.Fatalf("native attachments: got %#v, err %v, want %#v", got, err, files)
			}
		}
	}
}

func TestResolveChatAttachmentPathDoesNotHideConfiguredServiceFailure(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "cloud")
	t.Setenv("LAZYMIND_OFFICE_CONVERT_URL", "://invalid-url")
	if _, err := resolveChatAttachmentPath(context.Background(), "/tmp/native.docx", nil); err == nil {
		t.Fatal("expected configured conversion failure to remain visible")
	}
}

func TestDesktopAttachmentsNeverInvokeOfficeService(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	t.Setenv("LAZYMIND_OFFICE_CONVERT_URL", "://invalid-url")
	for _, path := range []string{"/tmp/native.docx", "/tmp/native.pptx"} {
		got, err := resolveChatAttachmentPath(context.Background(), path, map[string]any{"ocr_type": "paddleocr"})
		if err != nil || got != path {
			t.Fatalf("desktop attachment: got %q, err %v", got, err)
		}
	}
	if _, err := resolveChatAttachmentPath(context.Background(), "/tmp/legacy.doc", nil); err == nil {
		t.Fatal("legacy Office files should request conversion by the user")
	}
}

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

func TestChatPPTXBypassesUnavailableConversionService(t *testing.T) {
	t.Setenv("LAZYMIND_OFFICE_CONVERT_URL", "http://127.0.0.1:1/unavailable")
	body := map[string]any{"files": map[string][]string{"1": {"/tmp/template.PPTX"}},
		"ocr_config": map[string]any{"ocr_type": "mineru", "ocr_url": "http://local-mineru"}}
	if err := applyChatAttachmentConversion(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	if body["files"].(map[string][]string)["1"][0] != "/tmp/template.PPTX" {
		t.Fatal("PPTX source was replaced")
	}
}

func TestResolveChatAttachmentFilesKeepsPPTXDespiteExistingPDF(t *testing.T) {
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
	if !ok || len(paths) != 1 || paths[0] != source {
		t.Fatalf("expected original pptx path %q, got %#v", source, out)
	}
}

func TestResolveChatAttachmentFilesKeepsPPTXGroupedByTurn(t *testing.T) {
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
	if !ok || len(paths) != 1 || paths[0] != source {
		t.Fatalf("expected original pptx path %q, got %#v", source, turns["1"])
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
