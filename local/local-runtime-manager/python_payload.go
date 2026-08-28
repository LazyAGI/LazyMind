package main

import (
	"archive/zip"
	"context"
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
	pythonPayloadArchiveName = "python-runtime.zip"
	pythonPayloadMarkerName  = ".python-runtime-ready"
	pythonPayloadRenameWait  = 10 * time.Minute
)

var pythonPayloadRoots = []string{
	"runtimes/python",
	"deps/python",
}

type pythonPayloadProgress struct {
	Stage          string
	CompletedFiles int
	TotalFiles     int
	CompletedBytes uint64
	TotalBytes     uint64
	CompletedRoots int
	TotalRoots     int
	RetryAttempt   int
	RetryElapsed   time.Duration
}

type pythonPayloadProgressFunc func(pythonPayloadProgress)

// prepareBundledPythonRuntime expands the Windows installer payload on demand.
// macOS and portable builds continue to ship expanded Python trees, so a
// missing payload archive is intentionally a no-op.
func prepareBundledPythonRuntime(ctx context.Context, paths RuntimePaths, report pythonPayloadProgressFunc) error {
	archivePath := filepath.Join(paths.ResourcesRoot, pythonPayloadArchiveName)
	archiveIdentity, err := pythonPayloadIdentity(archivePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect bundled Python payload: %w", err)
	}

	markerPath := filepath.Join(paths.ResourcesRoot, pythonPayloadMarkerName)
	marker, markerErr := os.ReadFile(markerPath)
	if markerErr == nil && strings.TrimSpace(string(marker)) == archiveIdentity && pythonPayloadRootsExist(paths.ResourcesRoot) {
		_ = os.Remove(archivePath)
		return nil
	}
	if markerErr != nil && !os.IsNotExist(markerErr) {
		return fmt.Errorf("read bundled Python payload marker: %w", markerErr)
	}

	stagingRoot, err := os.MkdirTemp(paths.ResourcesRoot, ".python-runtime-staging-")
	if err != nil {
		return fmt.Errorf("create bundled Python staging directory: %w", err)
	}
	defer os.RemoveAll(stagingRoot)

	progress, err := extractPythonPayload(archivePath, stagingRoot, report)
	if err != nil {
		return err
	}
	progress.Stage = "installing"
	progress.TotalRoots = len(pythonPayloadRoots)
	if report != nil {
		report(progress)
	}
	for index, relativeRoot := range pythonPayloadRoots {
		source := filepath.Join(stagingRoot, filepath.FromSlash(relativeRoot))
		destination := filepath.Join(paths.ResourcesRoot, filepath.FromSlash(relativeRoot))
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return fmt.Errorf("create bundled Python parent directory: %w", err)
		}
		err := winfile.RetryOperation(ctx, func() error {
			return os.RemoveAll(destination)
		}, pythonPayloadRetryOptions(progress, report))
		if err != nil {
			return fmt.Errorf("replace bundled Python directory %s: %w", destination, err)
		}
		err = winfile.RetryOperation(ctx, func() error {
			return os.Rename(source, destination)
		}, pythonPayloadRetryOptions(progress, report))
		if err != nil {
			return fmt.Errorf("install bundled Python directory %s: %w", destination, err)
		}
		progress.Stage = "installing"
		progress.CompletedRoots = index + 1
		progress.RetryAttempt = 0
		progress.RetryElapsed = 0
		if report != nil {
			report(progress)
		}
	}
	if err := writeFileAtomically(ctx, markerPath, []byte(archiveIdentity+"\n"), 0o644, func(attempt int, elapsed time.Duration) {
		if report == nil {
			return
		}
		waiting := progress
		waiting.Stage = "waiting"
		waiting.RetryAttempt = attempt
		waiting.RetryElapsed = elapsed
		report(waiting)
	}); err != nil {
		return fmt.Errorf("write bundled Python payload marker: %w", err)
	}
	// The expanded trees replace the archive. Removing it keeps the installed
	// footprint equivalent to the old full installer instead of storing Python
	// twice. A transient antivirus lock is harmless; the marker avoids another
	// extraction and the next launch retries this removal.
	_ = os.Remove(archivePath)
	return nil
}

func pythonPayloadRetryOptions(progress pythonPayloadProgress, report pythonPayloadProgressFunc) winfile.RetryOptions {
	return winfile.RetryOptions{
		MaxWait: pythonPayloadRenameWait,
		OnRetry: func(attempt int, _ error, elapsed time.Duration) {
			if report == nil {
				return
			}
			waiting := progress
			waiting.Stage = "waiting"
			waiting.RetryAttempt = attempt
			waiting.RetryElapsed = elapsed
			report(waiting)
		},
	}
}

func pythonPayloadRootsExist(resourcesRoot string) bool {
	for _, relativeRoot := range pythonPayloadRoots {
		info, err := os.Stat(filepath.Join(resourcesRoot, filepath.FromSlash(relativeRoot)))
		if err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

func extractPythonPayload(archivePath, stagingRoot string, report pythonPayloadProgressFunc) (pythonPayloadProgress, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return pythonPayloadProgress{}, fmt.Errorf("open bundled Python payload: %w", err)
	}
	defer reader.Close()

	progress := pythonPayloadProgress{Stage: "extracting"}
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		progress.TotalFiles++
		progress.TotalBytes += entry.UncompressedSize64
	}
	if report != nil {
		report(progress)
	}

	foundRoot := make(map[string]bool, len(pythonPayloadRoots))
	for _, entry := range reader.File {
		cleanName := path.Clean(strings.ReplaceAll(entry.Name, "\\", "/"))
		if cleanName == "." || strings.HasPrefix(cleanName, "/") || cleanName == ".." || strings.HasPrefix(cleanName, "../") {
			return progress, fmt.Errorf("bundled Python payload contains unsafe path %q", entry.Name)
		}
		allowed := false
		for _, root := range pythonPayloadRoots {
			if cleanName == root || strings.HasPrefix(cleanName, root+"/") {
				allowed = true
				foundRoot[root] = true
				break
			}
		}
		if !allowed {
			return progress, fmt.Errorf("bundled Python payload contains unexpected path %q", entry.Name)
		}

		destination := filepath.Join(stagingRoot, filepath.FromSlash(cleanName))
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(destination, 0o755); err != nil {
				return progress, fmt.Errorf("create bundled Python directory: %w", err)
			}
			continue
		}
		if !entry.Mode().IsRegular() {
			return progress, fmt.Errorf("bundled Python payload contains unsupported file %q", entry.Name)
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return progress, fmt.Errorf("create bundled Python file parent: %w", err)
		}
		if err := extractPythonPayloadFile(entry, destination); err != nil {
			return progress, err
		}
		progress.CompletedFiles++
		progress.CompletedBytes += entry.UncompressedSize64
		if report != nil {
			report(progress)
		}
	}
	for _, root := range pythonPayloadRoots {
		if !foundRoot[root] {
			return progress, fmt.Errorf("bundled Python payload is missing %s", root)
		}
	}
	return progress, nil
}

func extractPythonPayloadFile(entry *zip.File, destination string) error {
	source, err := entry.Open()
	if err != nil {
		return fmt.Errorf("open bundled Python file %s: %w", entry.Name, err)
	}
	defer source.Close()

	mode := entry.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	target, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create bundled Python file %s: %w", entry.Name, err)
	}
	_, copyErr := io.Copy(target, source)
	closeErr := target.Close()
	if copyErr != nil {
		return fmt.Errorf("extract bundled Python file %s: %w", entry.Name, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close bundled Python file %s: %w", entry.Name, closeErr)
	}
	return nil
}

func pythonPayloadIdentity(filePath string) (string, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UTC().UnixNano()), nil
}

func writeFileAtomically(
	ctx context.Context,
	filePath string,
	content []byte,
	mode os.FileMode,
	onRetry func(attempt int, elapsed time.Duration),
) error {
	temp, err := os.CreateTemp(filepath.Dir(filePath), ".python-runtime-marker-")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, mode); err != nil {
		return err
	}
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return winfile.RetryOperation(ctx, func() error {
		return os.Rename(tempPath, filePath)
	}, winfile.RetryOptions{
		MaxWait: pythonPayloadRenameWait,
		OnRetry: func(attempt int, _ error, elapsed time.Duration) {
			if onRetry != nil {
				onRetry(attempt, elapsed)
			}
		},
	})
}
