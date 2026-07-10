package service

import (
	"context"
	"fmt"
)

type FakeZipDownloader struct {
	paths map[string]string
	fails map[string]bool
}

func NewFakeZipDownloader(paths map[string]string) *FakeZipDownloader {
	return &FakeZipDownloader{paths: paths, fails: map[string]bool{}}
}

func (d *FakeZipDownloader) Fail(url string) {
	d.fails[url] = true
}

func (d *FakeZipDownloader) Download(ctx context.Context, url string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	if d.fails[url] {
		return "", fmt.Errorf("download failed: %s", url)
	}
	path, ok := d.paths[url]
	if !ok {
		return "", fmt.Errorf("download not found: %s", url)
	}
	return path, nil
}
