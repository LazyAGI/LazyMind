//go:build !darwin && !linux

package doc

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"

	"lazymind/core/pdfir"
)

func guideFileIdentity(path string) (string, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "missing", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, pdfir.MaxArchiveBytes+1))
	if err != nil || n > pdfir.MaxArchiveBytes {
		return "", fmt.Errorf("cannot identify guide artifact %q", path)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
