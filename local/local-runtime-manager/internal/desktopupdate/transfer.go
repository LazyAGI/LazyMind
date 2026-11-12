package desktopupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Progress struct {
	Status          string `json:"status"`
	DownloadedBytes int64  `json:"downloadedBytes"`
	TotalBytes      int64  `json:"totalBytes"`
}

type DownloadReceipt struct {
	Status         string `json:"status"`
	ReleaseID      string `json:"releaseId"`
	Version        string `json:"version"`
	PolicySequence uint64 `json:"policySequence"`
	PolicyDigest   string `json:"policyDigest"`
	PackagePath    string `json:"packagePath"`
	FeedPath       string `json:"feedPath"`
	ComponentsPath string `json:"componentsPath"`
}

func Discover(ctx context.Context, c Config, state LocalState, root string, client *http.Client, now time.Time) (Decision, error) {
	if err := ValidateConfig(c); err != nil {
		return Decision{}, err
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var envelope []byte
	err := withHTTP(ctx, c, client, c.PolicyURL, func(_ context.Context, response *http.Response) error {
		if response.ContentLength > MaxEnvelopeBytes {
			return failure("policy_too_large", nil)
		}
		var err error
		envelope, err = io.ReadAll(io.LimitReader(response.Body, MaxEnvelopeBytes+1))
		if err != nil {
			return failure("network_unavailable", err)
		}
		if len(envelope) > MaxEnvelopeBytes {
			return failure("policy_too_large", nil)
		}
		return nil
	})
	if err != nil {
		return Decision{}, err
	}
	// Policy time advances during retries; a policy that expired while fetching
	// cannot be accepted using the older timestamp captured at command startup.
	return Check(ctx, envelope, c, state, root, false, now.Add(time.Since(started)))
}
func Download(ctx context.Context, c Config, state LocalState, root, expectedReleaseID string, client *http.Client, now time.Time, progress func(Progress)) (DownloadReceipt, error) {
	decision, err := Discover(ctx, c, state, root, client, now)
	if err != nil {
		return DownloadReceipt{}, err
	}
	if (decision.Status != "direct" && decision.Status != "bridge") || decision.Next == nil || decision.Next.ID != expectedReleaseID {
		return DownloadReceipt{}, failure("offer_changed", nil)
	}
	target := decision.Next
	partition := sha256.Sum256([]byte(c.TrustDomain + "\x00" + c.Audience + "\x00" + c.Channel))
	dir := filepath.Join(root, "downloads", hex.EncodeToString(partition[:]))
	if err := ensureDirectory(dir); err != nil {
		return DownloadReceipt{}, failure("cache_write", err)
	}
	release, err := acquireDownloadLock(ctx, dir)
	if err != nil {
		return DownloadReceipt{}, err
	}
	defer release()
	// Only this holder can own partial files in this namespace. This also cleans
	// a helper terminated by Windows after its graceful cancellation timed out.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return DownloadReceipt{}, failure("cache_read", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".download-") && strings.HasSuffix(entry.Name(), ".part") && !entry.IsDir() {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
				return DownloadReceipt{}, failure("cache_write", err)
			}
		}
	}
	artifacts := []Artifact{target.Package, target.Feed, target.Components}
	var total, completed, reported int64
	var lastSent time.Time
	var lastDelivered int64
	for _, a := range artifacts {
		if a.Size > 9007199254740991-total {
			return DownloadReceipt{}, failure("artifact_size", nil)
		}
		total += a.Size
	}
	report := func(current int64) {
		value := completed + current
		if value < reported {
			value = reported
		}
		if value > total {
			value = total
		}
		reported = value
		if progress != nil {
			if !lastSent.IsZero() && !(lastDelivered == 0 && value > 0) && value != total && time.Since(lastSent) < 250*time.Millisecond {
				return
			}
			lastSent = time.Now()
			lastDelivered = value
			progress(Progress{Status: "downloading", DownloadedBytes: value, TotalBytes: total})
		}
	}
	report(0)
	packageExtension := ".zip"
	if target.Platform == "windows" {
		packageExtension = ".exe"
	}
	extensions := []string{packageExtension, ".yml", ".json"}
	paths := make([]string, len(artifacts))
	for index, a := range artifacts {
		if err := ctx.Err(); err != nil {
			return DownloadReceipt{}, err
		}
		destination := filepath.Join(dir, a.SHA256+extensions[index])
		paths[index] = destination
		valid, err := verifyCachedArtifact(ctx, destination, a)
		if err != nil {
			return DownloadReceipt{}, failure("cache_read", err)
		}
		if !valid {
			// Unlink only the deterministic cache entry, never a referenced target.
			if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
				return DownloadReceipt{}, failure("cache_write", err)
			}
			err := withHTTP(ctx, c, client, a.URL, func(attemptCtx context.Context, response *http.Response) error {
				return writeArtifact(attemptCtx, response, a, destination, report)
			})
			if err != nil {
				return DownloadReceipt{}, err
			}
		}
		completed += a.Size
		report(0)
	}
	if err := ctx.Err(); err != nil {
		return DownloadReceipt{}, err
	}
	return DownloadReceipt{Status: "downloaded", ReleaseID: target.ID, Version: target.Version, PolicySequence: decision.PolicySequence, PolicyDigest: decision.PolicyDigest, PackagePath: paths[0], FeedPath: paths[1], ComponentsPath: paths[2]}, nil
}

func verifyCachedArtifact(ctx context.Context, name string, a Artifact) (bool, error) {
	info, err := os.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Size() != a.Size {
		return false, nil
	}
	f, err := os.Open(name)
	if err != nil {
		return false, err
	}
	defer f.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(contextReader{ctx: ctx, reader: f}, a.Size+1))
	if err != nil {
		return false, err
	}
	return n == a.Size && hex.EncodeToString(hash.Sum(nil)) == a.SHA256, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func writeArtifact(ctx context.Context, response *http.Response, a Artifact, destination string, progress func(int64)) error {
	if response.ContentLength >= 0 && response.ContentLength != a.Size {
		return failure("artifact_size", nil)
	}
	f, err := os.CreateTemp(filepath.Dir(destination), ".download-*.part")
	if err != nil {
		return failure("cache_write", err)
	}
	temporary := f.Name()
	defer os.Remove(temporary)
	defer f.Close()
	hash := sha256.New()
	reader := io.LimitReader(response.Body, a.Size+1)
	buffer := make([]byte, 128*1024)
	var received int64
	for {
		n, readErr := reader.Read(buffer)
		if n > 0 {
			received += int64(n)
			if received > a.Size {
				return failure("artifact_size", nil)
			}
			if _, err := f.Write(buffer[:n]); err != nil {
				return failure("cache_write", err)
			}
			_, _ = hash.Write(buffer[:n])
			progress(received)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return failure("network_unavailable", readErr)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if received != a.Size {
		return failure("artifact_size", nil)
	}
	if hex.EncodeToString(hash.Sum(nil)) != a.SHA256 {
		return failure("artifact_hash", nil)
	}
	if err := f.Sync(); err != nil {
		return failure("cache_write", err)
	}
	if err := f.Close(); err != nil {
		return failure("cache_write", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := replaceFile(temporary, destination); err != nil {
		return failure("cache_write", err)
	}
	return nil
}

func acquireDownloadLock(ctx context.Context, dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, "download.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, failure("cache_lock", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		if err := waitCtx.Err(); err != nil {
			_ = f.Close()
			return nil, failure("download_busy", err)
		}
		locked, err := tryLock(f)
		if err != nil {
			_ = f.Close()
			return nil, failure("cache_lock", err)
		}
		if locked {
			return func() { unlock(f); _ = f.Close() }, nil
		}
		if err := waitForRetry(waitCtx, 20*time.Millisecond); err != nil {
			_ = f.Close()
			return nil, failure("download_busy", err)
		}
	}
}

func VerifyArtifact(ctx context.Context, filename string, artifact Artifact) error {
	ok, err := verifyCachedArtifact(ctx, filename, artifact)
	if err != nil {
		return failure("artifact_hash", err)
	}
	if !ok {
		return failure("artifact_hash", nil)
	}
	return nil
}
