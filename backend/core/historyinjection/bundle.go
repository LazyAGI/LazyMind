package historyinjection

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxExtractedBundleBytes int64 = 1 << 30

type BundleSource struct {
	Path     string
	Manifest Manifest
	isZip    bool
}

func Discover(root string) ([]BundleSource, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" {
		return nil, nil
	}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var values []BundleSource
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			// A bundle is rooted by its direct manifest.json. Once found, do not
			// recurse into payload: workflow workspaces legitimately contain their
			// own unrelated manifest.json files.
			manifestPath := filepath.Join(path, "manifest.json")
			info, err := os.Stat(manifestPath)
			if err == nil && !info.IsDir() {
				manifest, err := readManifest(manifestPath)
				if err != nil {
					return err
				}
				values = append(values, BundleSource{Path: path, Manifest: manifest})
				return filepath.SkipDir
			}
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			return nil
		}
		lower := strings.ToLower(entry.Name())
		switch {
		case lower == "manifest.json": // Supports Discover("/path/manifest.json").
			manifest, err := readManifest(path)
			if err != nil {
				return err
			}
			values = append(values, BundleSource{Path: filepath.Dir(path), Manifest: manifest})
		case strings.HasSuffix(lower, ".zip"):
			manifest, err := readZipManifest(path)
			if err != nil {
				return err
			}
			values = append(values, BundleSource{Path: path, Manifest: manifest, isZip: true})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Prefer distributable ZIPs when a checkout contains both the expanded
	// source directory and its ZIP. Bundle IDs make discovery deterministic.
	sort.Slice(values, func(i, j int) bool {
		if values[i].isZip != values[j].isZip {
			return values[i].isZip
		}
		return values[i].Path < values[j].Path
	})
	seen := map[string]bool{}
	unique := make([]BundleSource, 0, len(values))
	for _, value := range values {
		if seen[value.Manifest.BundleID] {
			continue
		}
		seen[value.Manifest.BundleID] = true
		unique = append(unique, value)
	}
	return unique, nil
}

func readManifest(path string) (Manifest, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse history injection manifest %s: %w", path, err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("validate history injection manifest %s: %w", path, err)
	}
	return manifest, nil
}

func readZipManifest(path string) (Manifest, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("open history injection ZIP %s: %w", path, err)
	}
	defer reader.Close()
	for _, file := range reader.File {
		if filepath.ToSlash(file.Name) != "manifest.json" {
			continue
		}
		stream, err := file.Open()
		if err != nil {
			return Manifest{}, err
		}
		body, err := io.ReadAll(io.LimitReader(stream, 4<<20))
		_ = stream.Close()
		if err != nil {
			return Manifest{}, err
		}
		var manifest Manifest
		if err := json.Unmarshal(body, &manifest); err != nil {
			return Manifest{}, fmt.Errorf("parse history injection manifest in %s: %w", path, err)
		}
		if err := manifest.Validate(); err != nil {
			return Manifest{}, fmt.Errorf("validate history injection manifest in %s: %w", path, err)
		}
		return manifest, nil
	}
	return Manifest{}, fmt.Errorf("history injection ZIP %s has no root manifest.json", path)
}

func (source BundleSource) materialize() (string, func(), error) {
	if !source.isZip {
		return source.Path, func() {}, nil
	}
	temporary, err := os.MkdirTemp("", "lazymind-history-injection-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(temporary) }
	if err := extractZip(source.Path, temporary); err != nil {
		cleanup()
		return "", nil, err
	}
	return temporary, cleanup, nil
}

func extractZip(path, destination string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer reader.Close()
	var total int64
	for _, file := range reader.File {
		name := filepath.ToSlash(file.Name)
		if !safeRelativePath(name) {
			return fmt.Errorf("history injection ZIP contains unsafe path %q", file.Name)
		}
		if file.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("history injection ZIP contains unsupported symlink %q", file.Name)
		}
		total += int64(file.UncompressedSize64)
		if total > maxExtractedBundleBytes {
			return fmt.Errorf("history injection ZIP exceeds %d extracted bytes", maxExtractedBundleBytes)
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		input, err := file.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, file.Mode().Perm())
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		_ = input.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func fileDigest(path string) (string, int64, error) {
	stream, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer stream.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, stream)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}
