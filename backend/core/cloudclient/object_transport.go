package cloudclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type DownloadedObject struct {
	Path   string
	Size   int64
	SHA256 string
}

func (c *Client) DownloadSignedRequest(ctx context.Context, signed SignedRequest, expectedSize int64, expectedHash string, maxBytes int64) (DownloadedObject, error) {
	if c == nil || c.httpClient == nil || maxBytes <= 0 {
		return DownloadedObject{}, errors.New("object download is not configured")
	}
	parsed, err := url.Parse(strings.TrimSpace(signed.URL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || signed.Method != http.MethodGet {
		return DownloadedObject{}, errors.New("invalid signed object request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return DownloadedObject{}, err
	}
	for key, value := range signed.Headers {
		request.Header.Set(key, value)
	}
	objectClient := *c.httpClient
	objectClient.Jar = nil
	objectClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	response, err := objectClient.Do(request)
	if err != nil {
		return DownloadedObject{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return DownloadedObject{}, fmt.Errorf("signed object download failed: status=%d", response.StatusCode)
	}
	if response.ContentLength > maxBytes {
		return DownloadedObject{}, errors.New("signed object exceeds the download limit")
	}
	file, err := os.CreateTemp("", "lazymind-cloud-download-*.zip")
	if err != nil {
		return DownloadedObject{}, err
	}
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hasher), io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		cleanup()
		return DownloadedObject{}, err
	}
	if written > maxBytes {
		cleanup()
		return DownloadedObject{}, errors.New("signed object exceeds the download limit")
	}
	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if expectedSize > 0 && written != expectedSize {
		cleanup()
		return DownloadedObject{}, errors.New("signed object size mismatch")
	}
	if expectedHash = strings.TrimSpace(expectedHash); expectedHash != "" && actualHash != expectedHash {
		cleanup()
		return DownloadedObject{}, errors.New("signed object hash mismatch")
	}
	if err := file.Chmod(0o600); err != nil {
		cleanup()
		return DownloadedObject{}, err
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return DownloadedObject{}, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return DownloadedObject{}, err
	}
	return DownloadedObject{Path: file.Name(), Size: written, SHA256: actualHash}, nil
}
