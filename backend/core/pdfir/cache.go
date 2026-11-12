package pdfir

import (
	"os"
	"path/filepath"
)

// CachePath is shared with the ingestion reader. Content and model identity
// prevent reuse after a source or parser change; source ACLs are checked by callers.
func CachePath(source, hash, model string) string {
	if len(hash) != 64 || (model != "vlm" && model != "pipeline") {
		return ""
	}
	for _, c := range hash {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return ""
		}
	}
	return filepath.Join(filepath.Dir(source), ".lazymind-artifacts", "mineru-cache-v1", hash+"-"+model+".zip")
}
func ReadCachedArchive(source, hash, model string) []byte {
	path := CachePath(source, hash, model)
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	raw, err := bounded(f, MaxArchiveBytes)
	if err != nil {
		return nil
	}
	if _, err := AdaptZIP(raw, hash, model); err != nil {
		return nil
	}
	return raw
}
func SaveCachedArchive(source, hash, model string, raw []byte) {
	path := CachePath(source, hash, model)
	if path == "" || int64(len(raw)) > MaxArchiveBytes {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".archive-*")
	if err != nil {
		return
	}
	defer os.Remove(f.Name())
	_, err = f.Write(raw)
	closeErr := f.Close()
	if err == nil && closeErr == nil {
		_ = os.Rename(f.Name(), path)
	}
}
