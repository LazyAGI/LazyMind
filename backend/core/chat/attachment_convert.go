package chat

import (
	"context"
	"fmt"
	"strings"

	"lazymind/core/doc"
	"lazymind/core/log"
)

func resolveChatAttachmentFiles(ctx context.Context, files any, ocrConfig map[string]any) (any, error) {
	if files == nil {
		return nil, nil
	}

	switch xs := files.(type) {
	case []string:
		if len(xs) == 0 {
			return nil, nil
		}
		out := make([]string, 0, len(xs))
		for _, path := range xs {
			resolved, err := doc.ResolveParsePath(ctx, path, ocrConfig)
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
			resolved, err := doc.ResolveParsePath(ctx, path, ocrConfig)
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
