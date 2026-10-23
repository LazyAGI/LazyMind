package chat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lazymind/core/doc"
	"lazymind/core/log"
)

// Chat has native DOCX/PPTX readers, unlike the knowledge-base ingestion pipeline.
// Preserve converted PDFs when available, but do not require an Office service
// merely to read an attachment on desktop.
func resolveChatAttachmentPath(ctx context.Context, path string, ocrConfig map[string]any) (string, error) {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("LAZYMIND_RUNTIME_MODE")), "local") {
		switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
		case ".docx", ".pptx":
			return strings.TrimSpace(path), nil
		}
		if doc.NeedsOfficeConvertForPath(path, "", ocrConfig) {
			return "", fmt.Errorf("desktop does not support Office conversion; upload DOCX, PPTX or PDF instead")
		}
	}
	resolved, err := doc.ResolveParsePath(ctx, path, ocrConfig)
	if err != nil && strings.TrimSpace(os.Getenv("LAZYMIND_OFFICE_CONVERT_URL")) == "" {
		switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
		case ".docx", ".pptx":
			return strings.TrimSpace(path), nil
		}
	}
	return resolved, err
}

func resolveChatAttachmentFiles(ctx context.Context, files any, ocrConfig map[string]any) (any, error) {
	if files == nil {
		return nil, nil
	}

	switch xs := files.(type) {
	case map[string]any:
		if len(xs) == 0 {
			return xs, nil
		}
		out := make(map[string]any, len(xs))
		for turn, turnFiles := range xs {
			resolved, err := resolveChatAttachmentFiles(ctx, turnFiles, ocrConfig)
			if err != nil {
				return nil, fmt.Errorf("resolve turn %q: %w", turn, err)
			}
			out[turn] = resolved
		}
		return out, nil
	case map[string][]string:
		if len(xs) == 0 {
			return xs, nil
		}
		out := make(map[string][]string, len(xs))
		for turn, turnFiles := range xs {
			resolved, err := resolveChatAttachmentFiles(ctx, turnFiles, ocrConfig)
			if err != nil {
				return nil, fmt.Errorf("resolve turn %q: %w", turn, err)
			}
			paths, ok := resolved.([]string)
			if !ok {
				return nil, fmt.Errorf("resolve turn %q: unexpected files type %T", turn, resolved)
			}
			out[turn] = paths
		}
		return out, nil
	case []string:
		if len(xs) == 0 {
			return nil, nil
		}
		out := make([]string, 0, len(xs))
		for _, path := range xs {
			resolved, err := resolveChatAttachmentPath(ctx, path, ocrConfig)
			if err != nil {
				return nil, fmt.Errorf("convert attachment %q: %w", strings.TrimSpace(path), err)
			}
			if strings.TrimSpace(resolved) == "" {
				continue
			}
			out = append(out, resolved)
		}
		if len(out) == 0 {
			return nil, nil
		}
		return out, nil
	case []any:
		if len(xs) == 0 {
			return nil, nil
		}
		out := make([]any, 0, len(xs))
		for _, item := range xs {
			path, ok := item.(string)
			if !ok {
				out = append(out, item)
				continue
			}
			resolved, err := resolveChatAttachmentPath(ctx, path, ocrConfig)
			if err != nil {
				return nil, fmt.Errorf("convert attachment %q: %w", strings.TrimSpace(path), err)
			}
			if strings.TrimSpace(resolved) == "" {
				continue
			}
			out = append(out, resolved)
		}
		if len(out) == 0 {
			return nil, nil
		}
		return out, nil
	default:
		return files, nil
	}
}

func applyChatAttachmentConversion(ctx context.Context, body map[string]any) error {
	if body == nil {
		return nil
	}
	files := body["files"]
	if files == nil {
		return nil
	}
	ocrConfig, _ := body["ocr_config"].(map[string]any)
	resolved, err := resolveChatAttachmentFiles(ctx, files, ocrConfig)
	if err != nil {
		log.Logger.Error().Err(err).Msg("chat attachment conversion failed")
		return err
	}
	if resolved != nil {
		body["files"] = resolved
	}
	return nil
}

func chatAttachmentConversionReplyMessage(sidechat bool, err error) string {
	if sidechat {
		return "prepare chat attachments failed"
	}
	return fmt.Sprintf("%s: %v", "prepare chat attachments failed", err)
}
