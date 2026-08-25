package skillpackage

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

const (
	MaxFiles      = 512
	MaxFileBytes  = 32 << 20
	MaxTotalBytes = 128 << 20
)

func ReadZip(zipPath string) (map[string][]byte, error) {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return readFiles(reader.File)
}

func CleanPath(name string) (string, error) {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, `\`) || strings.Contains(name, "//") || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("unsafe path %q", name)
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned != name || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return "", fmt.Errorf("unsafe path %q", name)
	}
	for _, part := range strings.Split(cleaned, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("unsafe path %q", name)
		}
	}
	return cleaned, nil
}

func TreeHash(files map[string][]byte) string {
	paths := make([]string, 0, len(files))
	for filePath := range files {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, filePath := range paths {
		contentHash := sha256.Sum256(files[filePath])
		_, _ = io.WriteString(hash, filePath)
		_, _ = hash.Write([]byte{0})
		_, _ = io.WriteString(hash, hex.EncodeToString(contentHash[:]))
		_, _ = hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// WriteZip writes a deterministic Skill archive and returns its temporary path.
// The caller owns the returned file and must remove it when it is no longer needed.
func WriteZip(files map[string][]byte, directory string) (string, error) {
	if len(files) > MaxFiles {
		return "", fmt.Errorf("skill package contains too many entries: %d > %d", len(files), MaxFiles)
	}
	paths := make([]string, 0, len(files))
	var total int64
	for filePath, data := range files {
		cleaned, err := CleanPath(filePath)
		if err != nil {
			return "", err
		}
		if cleaned != filePath {
			return "", fmt.Errorf("unsafe path %q", filePath)
		}
		if len(data) > MaxFileBytes {
			return "", fmt.Errorf("skill package file %q exceeds %d bytes", filePath, MaxFileBytes)
		}
		total += int64(len(data))
		if total > MaxTotalBytes {
			return "", fmt.Errorf("skill package exceeds %d uncompressed bytes", MaxTotalBytes)
		}
		paths = append(paths, filePath)
	}
	sort.Strings(paths)

	file, err := os.CreateTemp(directory, ".skill-package-*.zip")
	if err != nil {
		return "", err
	}
	archivePath := file.Name()
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(archivePath)
		}
	}()

	writer := zip.NewWriter(file)
	for _, filePath := range paths {
		header := &zip.FileHeader{Name: filePath, Method: zip.Deflate}
		header.SetMode(0o644)
		header.Modified = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			_ = writer.Close()
			return "", err
		}
		if _, err := entry.Write(files[filePath]); err != nil {
			_ = writer.Close()
			return "", err
		}
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	keep = true
	return archivePath, nil
}

func readFiles(entries []*zip.File) (map[string][]byte, error) {
	if len(entries) > MaxFiles {
		return nil, fmt.Errorf("skill package contains too many entries: %d > %d", len(entries), MaxFiles)
	}
	files := make(map[string][]byte, len(entries))
	var total uint64
	for _, entry := range entries {
		if isIgnoredMetadata(entry.Name) {
			continue
		}
		entryName := strings.TrimSuffix(entry.Name, "/")
		name, err := CleanPath(entryName)
		if err != nil {
			return nil, err
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("skill package cannot contain symlink %q", name)
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		if _, exists := files[name]; exists {
			return nil, fmt.Errorf("skill package contains duplicate path %q", name)
		}
		if entry.UncompressedSize64 > MaxFileBytes {
			return nil, fmt.Errorf("skill package file %q exceeds %d bytes", name, MaxFileBytes)
		}
		total += entry.UncompressedSize64
		if total > MaxTotalBytes {
			return nil, fmt.Errorf("skill package exceeds %d uncompressed bytes", MaxTotalBytes)
		}
		rc, err := entry.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(rc, MaxFileBytes+1))
		closeErr := rc.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(data) > MaxFileBytes {
			return nil, fmt.Errorf("skill package file %q exceeds %d bytes", name, MaxFileBytes)
		}
		files[name] = data
	}
	return normalizeRoot(files), nil
}

func normalizeRoot(files map[string][]byte) map[string][]byte {
	if _, ok := files["SKILL.md"]; ok {
		return files
	}
	root := ""
	for filePath := range files {
		parts := strings.SplitN(filePath, "/", 2)
		if len(parts) != 2 || parts[1] == "" {
			return files
		}
		if root == "" {
			root = parts[0]
		} else if root != parts[0] {
			return files
		}
	}
	if root == "" {
		return files
	}
	normalized := make(map[string][]byte, len(files))
	prefix := root + "/"
	for filePath, data := range files {
		normalized[strings.TrimPrefix(filePath, prefix)] = data
	}
	if _, ok := normalized["SKILL.md"]; ok {
		return normalized
	}
	return files
}

func isIgnoredMetadata(name string) bool {
	name = strings.TrimSuffix(name, "/")
	if name == "" {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		lower := strings.ToLower(part)
		if lower == "__macosx" || lower == ".ds_store" || lower == "thumbs.db" || lower == "desktop.ini" || strings.HasPrefix(part, "._") {
			return true
		}
	}
	return false
}
