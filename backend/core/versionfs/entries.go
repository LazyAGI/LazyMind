package versionfs

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

func MergeEntries(base map[string]Entry, overlays []Overlay) map[string]Entry {
	entries := CloneEntries(base)
	for _, overlay := range overlays {
		switch overlay.Op {
		case OverlayDelete:
			for path := range entries {
				if path == overlay.Path || IsDescendantPath(overlay.Path, path) {
					delete(entries, path)
				}
			}
		default:
			entries[overlay.Path] = Entry{
				Path:      overlay.Path,
				EntryType: overlay.EntryType,
				BlobHash:  overlay.BlobHash,
				Size:      overlay.Size,
				Mime:      overlay.Mime,
				FileType:  overlay.FileType,
				Binary:    overlay.Binary,
				Mode:      overlay.Mode,
				FromDraft: true,
			}
		}
	}
	return entries
}

func HashTree(entries map[string]Entry) string {
	lines := make([]string, 0, len(entries))
	for _, entry := range SortedEntries(entries) {
		lines = append(lines, entry.Path+"\x00"+entry.EntryType+"\x00"+entry.BlobHash)
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

func SortedEntries(entries map[string]Entry) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func CloneEntries(entries map[string]Entry) map[string]Entry {
	out := make(map[string]Entry, len(entries))
	for path, entry := range entries {
		out[path] = entry
	}
	return out
}

func UnionEntryPaths(a, b map[string]Entry) []string {
	seen := map[string]bool{}
	for path := range a {
		seen[path] = true
	}
	for path := range b {
		seen[path] = true
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func EntrySignature(entry Entry) string {
	return strings.Join([]string{entry.EntryType, entry.BlobHash, entry.FileType}, "\x00")
}

func IsDescendantPath(parent, candidate string) bool {
	return strings.HasPrefix(candidate, parent+"/")
}

func IsStrictDescendantPath(parent, candidate string) bool {
	return candidate != parent && IsDescendantPath(parent, candidate)
}

func IsAncestorPath(parent, candidate string) bool {
	return candidate == parent || IsDescendantPath(parent, candidate)
}

func UniquePaths(paths []string) []string {
	seen := map[string]bool{}
	for _, path := range paths {
		if strings.TrimSpace(path) != "" {
			seen[path] = true
		}
	}
	out := make([]string, 0, len(seen))
	for path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}
