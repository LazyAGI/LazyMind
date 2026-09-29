package systemdeps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"lazymind/core/common"
)

type pdfFontDescriptor struct {
	SchemaVersion int    `json:"schemaVersion"`
	Filename      string `json:"filename"`
	SizeBytes     int64  `json:"sizeBytes"`
	SHA256        string `json:"sha256"`
	URL           string `json:"url"`
	FallbackURL   string `json:"fallbackUrl,omitempty"`
}

// A cancellable single-flight gate serializes download and atomic activation.
// Waiting callers recheck the cache; a cancelled/failed caller never poisons it.
var pdfFontGate = make(chan struct{}, 1)
var pdfFontClient = &http.Client{Timeout: 3 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" || req.URL.User != nil || len(via) >= 10 {
		return errors.New("invalid PDF font redirect")
	}
	return nil
}}

func loadPDFFontDescriptor() (pdfFontDescriptor, error) {
	var entry pdfFontDescriptor
	raw, err := os.ReadFile(os.Getenv("LAZYMIND_PDF_FONT_CATALOG"))
	if err != nil {
		return entry, err
	}
	if err = json.Unmarshal(raw, &entry); err != nil {
		return entry, err
	}
	if validateAssetMirrors(entry.URL, entry.FallbackURL, entry.Filename) != nil || entry.SchemaVersion != 1 ||
		!pythonComponentDigest.MatchString(entry.SHA256) || entry.SizeBytes <= 0 || entry.SizeBytes > 64*1024*1024 ||
		filepath.Base(entry.Filename) != entry.Filename || filepath.Ext(entry.Filename) != ".ttf" {
		return entry, errors.New("invalid PDF font descriptor")
	}
	return entry, nil
}

func validPDFFont(path string, entry pdfFontDescriptor) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != entry.SizeBytes {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	h := sha256.New()
	n, err := io.Copy(h, file)
	return err == nil && n == entry.SizeBytes && hex.EncodeToString(h.Sum(nil)) == entry.SHA256
}

func ensurePDFFont(ctx context.Context, root string, entry pdfFontDescriptor, client *http.Client) (string, error) {
	select {
	case pdfFontGate <- struct{}{}:
		defer func() { <-pdfFontGate }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	dir := filepath.Join(root, "deps", "pdf-font", entry.SHA256)
	target := filepath.Join(dir, entry.Filename)
	if validPDFFont(target, entry) {
		return target, nil
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := downloadMirroredAsset(ctx, client, entry.URL, entry.FallbackURL, entry.Filename,
		tmp.Name(), entry.SizeBytes, entry.SHA256, primaryMirrorPolicy); err != nil {
		return "", err
	}
	// Remove only a corrupt cache; Windows rename cannot replace an existing file.
	if err = os.Remove(target); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err = os.Rename(tmp.Name(), target); err != nil {
		return "", err
	}
	return target, nil
}

func GetPDFFont(w http.ResponseWriter, r *http.Request) {
	if !IsLocalRuntime() {
		common.ReplyErr(w, "PDF font download is only available in local mode", http.StatusForbidden)
		return
	}
	entry, err := loadPDFFontDescriptor()
	if err != nil {
		common.ReplyErr(w, "PDF font catalog is unavailable", http.StatusServiceUnavailable)
		return
	}
	root, err := RuntimeRootFromEnv()
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	font, err := ensurePDFFont(ctx, root, entry, pdfFontClient)
	if err != nil {
		common.ReplyErr(w, "PDF 中文字体准备失败，请检查网络后重试："+err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "font/ttf")
	w.Header().Set("Cache-Control", "private, no-cache")
	http.ServeFile(w, r, font)
}
