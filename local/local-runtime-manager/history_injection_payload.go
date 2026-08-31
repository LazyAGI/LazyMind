package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/winfile"
)

const (
	historyInjectionPayloadMarkerName = ".package-sha256"
	historyInjectionPayloadPrefix     = "history-injection/"
	historyInjectionPayloadMaxBytes   = uint64(2 << 30)
	historyInjectionPayloadRenameWait = 2 * time.Minute
)

// prepareBundledHistoryInjection expands the signed Desktop release asset into
// the mutable user runtime before Core starts. Local development and older
// Desktop packages have no archive path in their runtime manifest and keep
// using the repository history-injection directory.
func prepareBundledHistoryInjection(ctx context.Context, paths RuntimePaths) error {
	archivePath := strings.TrimSpace(paths.HistoryInjectionArchive)
	if archivePath == "" {
		return nil
	}
	identity, err := historyInjectionPayloadSHA256(archivePath)
	if err != nil {
		return fmt.Errorf("inspect bundled history injection package: %w", err)
	}
	if expected := strings.ToLower(strings.TrimSpace(paths.HistoryInjectionSHA256)); expected != "" && identity != expected {
		return fmt.Errorf("bundled history injection package SHA-256 mismatch: got %s, want %s", identity, expected)
	}
	targetRoot := strings.TrimSpace(paths.HistoryInjectionRoot)
	if targetRoot == "" {
		return fmt.Errorf("bundled history injection target root is empty")
	}
	markerPath := filepath.Join(targetRoot, historyInjectionPayloadMarkerName)
	if marker, readErr := os.ReadFile(markerPath); readErr == nil && strings.TrimSpace(string(marker)) == identity && historyInjectionBundleCount(targetRoot) > 0 {
		return nil
	} else if readErr != nil && !os.IsNotExist(readErr) {
		return fmt.Errorf("read bundled history injection marker: %w", readErr)
	}

	parent := filepath.Dir(targetRoot)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create bundled history injection parent: %w", err)
	}
	stagingRoot, err := os.MkdirTemp(parent, ".history-injection-staging-")
	if err != nil {
		return fmt.Errorf("create bundled history injection staging directory: %w", err)
	}
	defer os.RemoveAll(stagingRoot)

	extractedRoot := filepath.Join(stagingRoot, "history-injection")
	bundleCount, err := extractHistoryInjectionPayload(archivePath, extractedRoot)
	if err != nil {
		return err
	}
	if bundleCount == 0 {
		return fmt.Errorf("bundled history injection package contains no bundle ZIP files")
	}
	if err := os.WriteFile(filepath.Join(extractedRoot, historyInjectionPayloadMarkerName), []byte(identity+"\n"), 0o644); err != nil {
		return fmt.Errorf("write bundled history injection marker: %w", err)
	}
	if err := replaceHistoryInjectionRoot(ctx, extractedRoot, targetRoot); err != nil {
		return fmt.Errorf("install bundled history injection package: %w", err)
	}
	return nil
}

func historyInjectionPayloadSHA256(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func extractHistoryInjectionPayload(archivePath, targetRoot string) (int, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return 0, fmt.Errorf("open bundled history injection package: %w", err)
	}
	defer reader.Close()

	var totalBytes uint64
	bundleCount := 0
	for _, entry := range reader.File {
		cleanName := path.Clean(strings.ReplaceAll(entry.Name, "\\", "/"))
		if cleanName == "." || strings.HasPrefix(cleanName, "/") || cleanName == ".." || strings.HasPrefix(cleanName, "../") {
			return 0, fmt.Errorf("bundled history injection package contains unsafe path %q", entry.Name)
		}
		if cleanName == strings.TrimSuffix(historyInjectionPayloadPrefix, "/") {
			continue
		}
		if !strings.HasPrefix(cleanName, historyInjectionPayloadPrefix) {
			// README and package checksum metadata live at the outer ZIP root and
			// are intentionally not copied into Core's discovery directory.
			continue
		}
		relativeName := strings.TrimPrefix(cleanName, historyInjectionPayloadPrefix)
		if relativeName == "" || relativeName == "." || relativeName == ".." || strings.HasPrefix(relativeName, "../") {
			return 0, fmt.Errorf("bundled history injection package contains unsafe path %q", entry.Name)
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return 0, fmt.Errorf("bundled history injection package contains symlink %q", entry.Name)
		}
		totalBytes += entry.UncompressedSize64
		if totalBytes > historyInjectionPayloadMaxBytes {
			return 0, fmt.Errorf("bundled history injection package exceeds %d extracted bytes", historyInjectionPayloadMaxBytes)
		}

		nativeRelativeName := filepath.FromSlash(relativeName)
		if filepath.IsAbs(nativeRelativeName) || filepath.VolumeName(nativeRelativeName) != "" {
			return 0, fmt.Errorf("bundled history injection package contains unsafe path %q", entry.Name)
		}
		destination := filepath.Join(targetRoot, nativeRelativeName)
		resolvedRelative, relErr := filepath.Rel(targetRoot, destination)
		if relErr != nil || resolvedRelative == ".." || strings.HasPrefix(resolvedRelative, ".."+string(filepath.Separator)) {
			return 0, fmt.Errorf("bundled history injection package contains unsafe path %q", entry.Name)
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(destination, 0o755); err != nil {
				return 0, fmt.Errorf("create bundled history injection directory: %w", err)
			}
			continue
		}
		if !entry.Mode().IsRegular() {
			return 0, fmt.Errorf("bundled history injection package contains unsupported file %q", entry.Name)
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return 0, fmt.Errorf("create bundled history injection file parent: %w", err)
		}
		if err := extractHistoryInjectionPayloadFile(entry, destination); err != nil {
			return 0, err
		}
		if strings.EqualFold(filepath.Ext(relativeName), ".zip") {
			bundleCount++
		}
	}
	return bundleCount, nil
}

func extractHistoryInjectionPayloadFile(entry *zip.File, destination string) error {
	source, err := entry.Open()
	if err != nil {
		return fmt.Errorf("open bundled history injection file %s: %w", entry.Name, err)
	}
	defer source.Close()
	target, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create bundled history injection file %s: %w", entry.Name, err)
	}
	_, copyErr := io.Copy(target, source)
	closeErr := target.Close()
	if copyErr != nil {
		return fmt.Errorf("extract bundled history injection file %s: %w", entry.Name, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close bundled history injection file %s: %w", entry.Name, closeErr)
	}
	return nil
}

func replaceHistoryInjectionRoot(ctx context.Context, sourceRoot, targetRoot string) error {
	backupRoot := fmt.Sprintf("%s.previous-%d", targetRoot, os.Getpid())
	_ = os.RemoveAll(backupRoot)
	hadTarget := false
	if _, err := os.Lstat(targetRoot); err == nil {
		hadTarget = true
		if err := retryHistoryInjectionRename(ctx, targetRoot, backupRoot); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := retryHistoryInjectionRename(ctx, sourceRoot, targetRoot); err != nil {
		if hadTarget {
			_ = retryHistoryInjectionRename(context.Background(), backupRoot, targetRoot)
		}
		return err
	}
	if hadTarget {
		_ = os.RemoveAll(backupRoot)
	}
	return nil
}

func retryHistoryInjectionRename(ctx context.Context, source, destination string) error {
	return winfile.RetryOperation(ctx, func() error {
		return os.Rename(source, destination)
	}, winfile.RetryOptions{MaxWait: historyInjectionPayloadRenameWait})
}

func historyInjectionBundleCount(root string) int {
	count := 0
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.EqualFold(filepath.Ext(path), ".zip") {
			count++
		}
		return nil
	})
	return count
}
