package artifactfile

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// Keep room below MAX_PATH for the terminator and atomic-write temporary names.
const storagePathBudget = 240

// StoragePath folds long scopes into one directory inside the same managed root.
// Existing metadata keeps its recorded path and requires no migration.
func StoragePath(root, filename string, scopes ...string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	parts := append([]string{root}, scopes...)
	candidate := filepath.Join(append(parts, filename)...)
	if storagePathFits(candidate) {
		return candidate, nil
	}
	identity, err := json.Marshal(append(append([]string{}, scopes...), filename))
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(identity)
	candidate = filepath.Join(root, fmt.Sprintf("%x", digest[:16]), "file"+StorageExtension(filename))
	// The budget selects a compact layout, not a hard I/O limit. Go's Windows
	// os APIs automatically use extended-length paths when the configured root
	// itself is long. Keep the path inside managed storage for signed reads.
	return candidate, nil
}

func storagePathFits(path string) bool {
	// Supplementary Unicode characters occupy two UTF-16 code units on Windows.
	if len(utf16.Encode([]rune(path))) > storagePathBudget ||
		len(utf16.Encode([]rune(filepath.Join(filepath.Dir(path), ".artifact-1234567890")))) > storagePathBudget {
		return false
	}
	for _, component := range strings.FieldsFunc(filepath.ToSlash(path), func(r rune) bool { return r == '/' }) {
		if len(utf16.Encode([]rune(component))) > 255 || len(component) > 255 {
			return false
		}
	}
	return true
}
