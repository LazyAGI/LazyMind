package doc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lazymind/core/modelconfig"
	"lazymind/core/store"
)

// Checkpoints live beside the job's source, on the existing persistent storage.
// The key includes input and provider identity; retrying never reuses other text.
func translateDocumentChunkCached(ctx context.Context, userID string, payload pdfTranslationPayload, text string, snapshot *modelconfig.TranslationConfigSnapshot) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if snapshot != nil {
		if _, err := modelconfig.ResolveTranslationConfigSnapshot(ctx, store.DB(), userID, *snapshot); err != nil {
			return "", err
		}
	}
	identity, err := json.Marshal([]any{userID, payload.ProviderType, payload.Provider, payload.TargetLanguage, snapshot, text})
	if err != nil {
		return "", err
	}
	path := filepath.Join(payload.SourcePath+".translation-cache", fmt.Sprintf("%x.txt", sha256.Sum256(identity)))
	return cachedTranslation(path, func() (string, error) {
		return translateDocumentChunk(ctx, userID, payload.ProviderType, text, payload.TargetLanguage, snapshot)
	})
}

func cachedTranslation(path string, translate func() (string, error)) (string, error) {
	if value, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(value)) != "" {
		return string(value), nil
	}
	value, err := translate()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("translation returned empty text")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".checkpoint-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	if _, err := file.WriteString(value); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return "", err
	}
	return value, nil
}

// Replaying a completed attempt can retain the same paths under a new artifact
// ID. Never delete files that are still referenced by its replacement.
func removeReplacedTranslationFiles(replaced []pdfArtifactRecord, current pdfArtifactRecord) {
	keep := map[string]bool{current.StoredPath: true, current.SourcePath: true, current.LayoutPath: true, current.DraftPath: true}
	for _, old := range replaced {
		for _, path := range []string{old.StoredPath, old.SourcePath, old.LayoutPath, old.DraftPath} {
			if path != "" && !keep[path] {
				_ = os.Remove(path)
			}
		}
	}
}
