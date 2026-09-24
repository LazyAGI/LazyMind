package systemdeps

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
	"path"
	"sync/atomic"
	"time"

	appLog "lazymind/core/log"
)

type mirrorDownloadPolicy struct {
	firstByteTimeout time.Duration
	speedWindow      time.Duration
	minimumBytes     int64
}

// Switch sources if no body arrives within 10s, or a 15s window transfers less
// than 64 KiB/s. The fallback retains the caller's overall download deadline.
var primaryMirrorPolicy = mirrorDownloadPolicy{10 * time.Second, 15 * time.Second, 15 * 64 * 1024}

func validateAssetURL(address string) error {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("asset download URL must be HTTPS without embedded credentials or a fragment")
	}
	return nil
}

func validateAssetMirrors(primary, fallback, filename string) error {
	if err := validateAssetURL(primary); err != nil {
		return err
	}
	if fallback == "" {
		return nil
	}
	if err := validateAssetURL(fallback); err != nil {
		return err
	}
	a, _ := url.Parse(primary)
	b, _ := url.Parse(fallback)
	if filename == "" || path.Base(a.Path) != filename || path.Base(b.Path) != filename {
		return errors.New("asset mirrors must reference the catalog filename")
	}
	return nil
}

type countedAssetReader struct {
	io.Reader
	bytes *atomic.Int64
}

func (r countedAssetReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.bytes.Add(int64(n))
	return n, err
}

func downloadAssetAttempt(ctx context.Context, client *http.Client, address, destination string,
	size int64, digest string, policy mirrorDownloadPolicy) (downloadErr error) {
	if err := validateAssetURL(address); err != nil {
		return err
	}
	attemptCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var received atomic.Int64
	if policy.firstByteTimeout > 0 {
		go func() {
			first := time.NewTimer(policy.firstByteTimeout)
			defer first.Stop()
			select {
			case <-attemptCtx.Done():
				return
			case <-first.C:
				if received.Load() == 0 {
					cancel(errors.New("primary asset source did not deliver data within the first-byte timeout"))
					return
				}
			}
			if policy.speedWindow <= 0 {
				return
			}
			previous := received.Load()
			ticker := time.NewTicker(policy.speedWindow)
			defer ticker.Stop()
			for {
				select {
				case <-attemptCtx.Done():
					return
				case <-ticker.C:
					current := received.Load()
					if current-previous < policy.minimumBytes {
						cancel(errors.New("primary asset source transfer speed is below the minimum"))
						return
					}
					previous = current
				}
			}
		}()
	}
	// Clone the client so all redirects (including HF's signed CDN URL) stay
	// HTTPS without changing a shared transport or a caller's test client.
	secureClient := *client
	secureClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many asset redirects")
		}
		if err := validateAssetURL(req.URL.String()); err != nil {
			return err
		}
		if client.CheckRedirect != nil {
			return client.CheckRedirect(req, via)
		}
		return nil
	}
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	response, err := secureClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("asset download returned HTTP %d", response.StatusCode)
	}
	file, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer func() {
		file.Close()
		if downloadErr != nil {
			os.Remove(destination)
		}
	}()
	hash := sha256.New()
	reader := countedAssetReader{Reader: response.Body, bytes: &received}
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(reader, size+1))
	if err != nil {
		if cause := context.Cause(attemptCtx); cause != nil {
			return cause
		}
		return err
	}
	if written != size || hex.EncodeToString(hash.Sum(nil)) != digest {
		return errors.New("asset size or SHA-256 does not match the build catalog")
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return file.Close()
}

func downloadMirroredAsset(ctx context.Context, client *http.Client, primary, fallback, filename, destination string,
	size int64, digest string, policy mirrorDownloadPolicy) error {
	if err := validateAssetMirrors(primary, fallback, filename); err != nil {
		return err
	}
	if fallback == "" || fallback == primary {
		return downloadAssetAttempt(ctx, client, primary, destination, size, digest, mirrorDownloadPolicy{})
	}
	primaryErr := downloadAssetAttempt(ctx, client, primary, destination, size, digest, policy)
	if primaryErr == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	appLog.Logger.Info().Str("filename", filename).Err(primaryErr).Msg("asset.download.fallback")
	if err := downloadAssetAttempt(ctx, client, fallback, destination, size, digest, mirrorDownloadPolicy{}); err != nil {
		return fmt.Errorf("asset download failed: primary: %v; fallback: %w", primaryErr, err)
	}
	return nil
}

// DownloadVerifiedAsset downloads a build-pinned public resource with mirror
// fallback and verifies its exact size and SHA-256 before returning success.
func DownloadVerifiedAsset(ctx context.Context, primary, fallback, filename, destination string, size int64, digest string) error {
	return downloadMirroredAsset(ctx, &http.Client{Timeout: 3 * time.Minute}, primary, fallback, filename, destination, size, digest, primaryMirrorPolicy)
}
