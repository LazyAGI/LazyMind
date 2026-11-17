package pdfir

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	dir := filepath.Dir(source)
	root := strings.TrimSpace(os.Getenv("LAZYMIND_UPLOAD_ROOT"))
	if root == "" {
		root = "/var/lib/lazymind/uploads"
	}
	if rel, err := filepath.Rel(root, dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		dir = root
	}
	return filepath.Join(dir, ".lazymind-artifacts", "mineru-cache-v1", hash+"-"+model+".zip")
}
func ReadCachedArchive(source, hash, model string) []byte {
	raw, _ := readArchive(CachePath(source, hash, model), hash, model)
	return raw
}

func readArchive(path, hash, model string) ([]byte, Document) {
	f, err := os.Open(path)
	if err != nil {
		return nil, Document{}
	}
	defer f.Close()
	raw, err := bounded(f, MaxArchiveBytes)
	if err != nil {
		return nil, Document{}
	}
	d, err := AdaptSharedZIP(raw, hash, model)
	if err != nil {
		return nil, Document{}
	}
	return raw, d
}

// ReadSharedArchive selects the current VLM result first, then a compatible
// pipeline result. Legacy files beside the source keep their original provenance.
func ReadSharedArchive(source, hash string) ([]byte, Document) {
	raw, d, _ := LookupSharedArchive(source, hash)
	return raw, d
}

// LookupSharedArchive reports an incomplete cache when no usable result exists.
func LookupSharedArchive(source, hash string) ([]byte, Document, bool) {
	incomplete := false
	for _, model := range []string{"vlm", "pipeline"} {
		path := CachePath(source, hash, model)
		if path == "" {
			return nil, Document{}, false
		}
		candidates := []string{path}
		if source != "" {
			legacy := filepath.Join(filepath.Dir(source), ".lazymind-artifacts", "mineru-cache-v1", filepath.Base(path))
			if path != legacy {
				candidates = append(candidates, legacy)
			}
		}
		for _, candidate := range candidates {
			raw, d := readArchive(candidate, hash, model)
			if raw != nil {
				if candidate != path && os.MkdirAll(filepath.Dir(path), 0700) == nil {
					// Promote legacy data without overwriting a concurrent
					// publication. The original legacy file stays in place.
					_ = os.Link(candidate, path)
				}
				return raw, d, false
			}
			if _, err := os.Stat(candidate); err == nil {
				incomplete = true
			}
		}
	}
	return nil, Document{}, incomplete
}

// AdaptSharedZIP requires a usable document. Legacy pipeline results must
// contain page geometry before being exposed as reading structure.
func AdaptSharedZIP(raw []byte, hash, model string) (Document, error) {
	d, err := AdaptZIP(raw, hash, model)
	if err != nil {
		return d, err
	}
	if len(d.Blocks) == 0 {
		return d, errors.New("MinerU result contains no structured blocks")
	}
	for _, b := range d.Blocks {
		if strings.TrimSpace(b.Type) == "" || model == "pipeline" && len(b.BBox) != 4 {
			return d, errors.New("MinerU result contains incomplete structure")
		}
	}
	return d, nil
}

func SaveCachedArchive(source, hash, model string, raw []byte) error {
	path := CachePath(source, hash, model)
	if path == "" {
		return errors.New("invalid MinerU cache identity")
	}
	if _, err := AdaptSharedZIP(raw, hash, model); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".archive-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(raw)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

// LockCachedArchive coordinates paid uploads with Python's Unix FileLock.
// The sidecar is persistent: removing it would let waiters lock different inodes.
// Callers must recheck the cache after locking and publish before unlocking.
func LockCachedArchive(ctx context.Context, source, hash, model string) (func(), error) {
	path := CachePath(source, hash, model)
	if path == "" {
		return nil, errors.New("invalid MinerU cache identity")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		err = tryArchiveLock(f)
		if err == nil {
			return func() { _ = releaseArchiveLock(f); _ = f.Close() }, nil
		}
		if !archiveLockBusy(err) {
			f.Close()
			return nil, err
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
