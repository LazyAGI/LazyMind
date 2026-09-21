package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type HTTPZipDownloader struct{}

const MaxSkillDownloadBytes int64 = 20 << 20

const skillArchiveDownloadTimeout = 5 * time.Minute

func newSkillArchiveHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{
		Transport: transport,
		Timeout:   skillArchiveDownloadTimeout,
	}
}

func (HTTPZipDownloader) Download(ctx context.Context, rawURL string) (DownloadedZip, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return DownloadedZip{}, fmt.Errorf("url required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return DownloadedZip{}, err
	}
	client := newSkillArchiveHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return DownloadedZip{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return DownloadedZip{}, fmt.Errorf("download failed: %s", resp.Status)
	}
	f, err := os.CreateTemp("", "lazymind-skill-*.zip")
	if err != nil {
		return DownloadedZip{}, err
	}
	if resp.ContentLength > MaxSkillDownloadBytes {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return DownloadedZip{}, fmt.Errorf("skill package download exceeds %d bytes", MaxSkillDownloadBytes)
	}
	written, err := io.Copy(f, io.LimitReader(resp.Body, MaxSkillDownloadBytes+1))
	if err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return DownloadedZip{}, err
	}
	if written > MaxSkillDownloadBytes {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return DownloadedZip{}, fmt.Errorf("skill package download exceeds %d bytes", MaxSkillDownloadBytes)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return DownloadedZip{}, err
	}
	return DownloadedZip{Path: f.Name(), Cleanup: func() { _ = os.Remove(f.Name()) }}, nil
}
