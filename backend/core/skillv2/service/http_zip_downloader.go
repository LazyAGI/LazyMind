package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"lazymind/core/skillv2/httperr"
)

type HTTPZipDownloader struct {
	client           *http.Client
	githubAPIBaseURL string
	githubRawBaseURL string
}

const MaxSkillDownloadBytes int64 = 100 << 20

const skillArchiveDownloadTimeout = 5 * time.Minute

func newSkillArchiveHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{
		Transport: transport,
		Timeout:   skillArchiveDownloadTimeout,
	}
}

func (d HTTPZipDownloader) Download(ctx context.Context, rawURL string) (DownloadedZip, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return DownloadedZip{}, fmt.Errorf("url required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return DownloadedZip{}, err
	}
	client := d.httpClient()
	resp, err := client.Do(req)
	if err != nil {
		return DownloadedZip{}, skillDownloadError(err)
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
		return DownloadedZip{}, httperr.New(http.StatusRequestEntityTooLarge, httperr.CodePayloadTooLarge, fmt.Sprintf("skill package download exceeds %d bytes", MaxSkillDownloadBytes))
	}
	written, err := io.Copy(f, io.LimitReader(resp.Body, MaxSkillDownloadBytes+1))
	if err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return DownloadedZip{}, skillDownloadError(err)
	}
	if written > MaxSkillDownloadBytes {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return DownloadedZip{}, httperr.New(http.StatusRequestEntityTooLarge, httperr.CodePayloadTooLarge, fmt.Sprintf("skill package download exceeds %d bytes", MaxSkillDownloadBytes))
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return DownloadedZip{}, err
	}
	return DownloadedZip{Path: f.Name(), Cleanup: func() { _ = os.Remove(f.Name()) }}, nil
}

func (d HTTPZipDownloader) httpClient() *http.Client {
	if d.client != nil {
		return d.client
	}
	return newSkillArchiveHTTPClient()
}

func skillDownloadError(err error) error {
	var transfer *githubTransferLimitError
	if errors.As(err, &transfer) {
		return githubPackageTooLarge("skill package download exceeds transfer limit")
	}
	return err
}
