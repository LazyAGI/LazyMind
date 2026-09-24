package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// HistoryInjectionDownload pins the published examples without embedding their
// archive in the installer. It is carried by the signed runtime manifest.
type HistoryInjectionDownload struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func safeHistoryDownloadURL(address string) bool {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || u.User != nil {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	// Local HTTP is useful for deterministic tests and local release validation.
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "http" && ip != nil && ip.IsLoopback()
}

func (d HistoryInjectionDownload) validate() error {
	digest, err := hex.DecodeString(d.SHA256)
	if err != nil || len(digest) != sha256.Size || d.SHA256 != strings.ToLower(d.SHA256) {
		return fmt.Errorf("invalid history example SHA-256")
	}
	if d.Size <= 0 || uint64(d.Size) > historyInjectionPayloadMaxBytes {
		return fmt.Errorf("invalid history example download size")
	}
	if !safeHistoryDownloadURL(d.URL) {
		return fmt.Errorf("history example URL must use HTTPS")
	}
	return nil
}

func historyInjectionArchiveMatches(path string, d HistoryInjectionDownload) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != d.Size {
		return false
	}
	digest, err := historyInjectionPayloadSHA256(path)
	return err == nil && digest == d.SHA256
}

func downloadHistoryInjection(ctx context.Context, d HistoryInjectionDownload, destination string) error {
	if err := d.validate(); err != nil {
		return err
	}
	if historyInjectionArchiveMatches(destination, d) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(destination), ".history-download-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() { file.Close(); os.Remove(temporary) }()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, d.URL, nil)
	if err != nil {
		return err
	}
	client := http.Client{Timeout: 3 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 || !safeHistoryDownloadURL(req.URL.String()) || (strings.HasPrefix(d.URL, "https:") && req.URL.Scheme != "https") {
			return fmt.Errorf("unsafe history example redirect")
		}
		return nil
	}}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download workflow examples: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download workflow examples: HTTP %d", response.StatusCode)
	}
	digest := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, digest), io.LimitReader(response.Body, d.Size+1))
	if err != nil {
		return fmt.Errorf("download workflow examples: %w", err)
	}
	if err = file.Close(); err != nil {
		return err
	}
	if written != d.Size {
		return fmt.Errorf("workflow example size mismatch: got %d, want %d", written, d.Size)
	}
	if hex.EncodeToString(digest.Sum(nil)) != d.SHA256 {
		return fmt.Errorf("workflow example SHA-256 mismatch")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// A corrupt cache can be replaced; the previously installed examples are not
	// touched until extraction and verification have succeeded.
	if err := os.Remove(destination); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(temporary, destination)
}

func (m *RuntimeManager) prepareStartupHistoryInjection(ctx context.Context, paths RuntimePaths) error {
	if paths.HistoryInjectionArchive == "" {
		return nil
	}
	started := m.now()
	m.progressf("preparing workflow example package (download if needed)")
	m.startupEvent("phase.started", "history-injection-payload", started, nil)
	if err := prepareBundledHistoryInjection(ctx, paths); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if paths.HistoryInjectionDownload != nil {
			// Cases are optional content. A network failure must not prevent ordinary
			// chat from starting or remove a previously installed collection of cases.
			m.startupEvent("phase.skipped", "history-injection-payload", started, err)
			m.progressf("workflow examples unavailable; keeping existing examples and retrying next launch: %v", err)
			return nil
		}
		m.startupEvent("phase.failed", "history-injection-payload", started, err)
		return fmt.Errorf("prepare bundled history injection: %w", err)
	}
	m.startupEvent("phase.completed", "history-injection-payload", started, nil)
	m.progressf("workflow examples ready in %s", m.now().Sub(started).Round(time.Millisecond))
	return nil
}
