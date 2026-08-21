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

func readFiles(entries []*zip.File) (map[string][]byte, error) {
	if len(entries) > MaxFiles {
		return nil, fmt.Errorf("skill package contains too many entries: %d > %d", len(entries), MaxFiles)
	}
	files := make(map[string][]byte, len(entries))
	var total uint64
	for _, entry := range entries {
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
