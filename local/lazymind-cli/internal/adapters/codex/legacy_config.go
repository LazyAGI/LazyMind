package codex

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	legacyControlBegin = "# >>> LazyMind Codex native control >>>"
	legacyControlEnd   = "# <<< LazyMind Codex native control <<<"
	legacyOriginalLine = "# original-chatgpt-base-url-line: "
)

func CleanupLegacyControl() error {
	home, err := codexHome()
	if err != nil {
		return err
	}
	path := filepath.Join(home, "config.toml")
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	restored, changed, err := removeLegacyControlBlock(body)
	if err != nil || !changed {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, restored, info.Mode().Perm())
}

func removeLegacyControlBlock(body []byte) ([]byte, bool, error) {
	text := string(body)
	start := strings.Index(text, legacyControlBegin)
	if start < 0 {
		return body, false, nil
	}
	relativeEnd := strings.Index(text[start:], legacyControlEnd)
	if relativeEnd < 0 {
		return nil, false, errors.New("incomplete LazyMind Codex native-control marker")
	}
	end := start + relativeEnd + len(legacyControlEnd)
	if end < len(text) && text[end] == '\r' {
		end++
	}
	if end < len(text) && text[end] == '\n' {
		end++
	}
	block := text[start:end]
	encodedOriginal := ""
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, legacyOriginalLine) {
			encodedOriginal = strings.TrimPrefix(line, legacyOriginalLine)
			break
		}
	}
	original, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(encodedOriginal))
	if err != nil {
		return nil, false, errors.New("invalid saved Codex base URL configuration")
	}
	restored := append(append([]byte{}, body[:start]...), original...)
	restored = append(restored, body[end:]...)
	return restored, true, nil
}
