package academic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"lazymind/core/academic/arxivsource"
	"lazymind/core/doc"
	"lazymind/core/log"
)

type paperPDFDownloadDir struct{}

type cachedPDF struct {
	UserID       string    `json:"user_id"`
	PaperID      string    `json:"paper_id"`
	Provider     string    `json:"provider"`
	BaseID       string    `json:"base_id"`
	Version      string    `json:"version"`
	URL          string    `json:"url"`
	DownloadedAt time.Time `json:"downloaded_at"`
	Size         int64     `json:"size"`
	SHA256       string    `json:"sha256"`
}

func paperPDFCacheRoot() string { return filepath.Join(doc.UploadRoot(), "academic-pdf-cache") }

func (p cachedPDF) key() string {
	sum := sha256.Sum256(marshal([]string{p.UserID, p.Provider, p.BaseID, p.Version, p.URL}))
	return hex.EncodeToString(sum[:])
}

// Caller holds the canonical arXiv import lock until parsing or copying into
// permanent storage finishes. Cleanup acquires that same lock, never a nested one.
func acquireCachedPDFLocked(ctx context.Context, user string, ref PaperRef, acquire func(context.Context, FulltextCandidate) (string, int64, error)) (string, int64, error) {
	metadata, ok := arxivMetadataFromRef(ref)
	if !ok || user == "" {
		return "", 0, errors.New("verified versioned PDF source required")
	}
	wanted := cachedPDF{UserID: user, PaperID: ref.PaperID, Provider: "arxiv", BaseID: metadata.BaseID, Version: metadata.Version, URL: metadata.PDFURL}
	root := paperPDFCacheRoot()
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", 0, err
	}
	if info, err := os.Lstat(root); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", 0, errors.New("invalid paper PDF cache directory")
	}
	directory := filepath.Join(root, wanted.key())
	if entry, err := readCachedPDF(directory); err == nil && entry.UserID == user && entry.key() == wanted.key() {
		if hash, size, err := hashCachedPDF(filepath.Join(directory, "paper.pdf")); err == nil && hash == entry.SHA256 && size == entry.Size {
			log.Logger.Info().Str("paper_id", ref.PaperID).Str("version", ref.SourceVersion).Msg("academic PDF cache hit")
			return filepath.Join(directory, "paper.pdf"), size, nil
		}
	}
	if err := os.RemoveAll(directory); err != nil {
		return "", 0, err
	}
	staging := directory + ".partial-" + uuid.NewString()
	if err := os.Mkdir(staging, 0700); err != nil {
		return "", 0, err
	}
	defer os.RemoveAll(staging)
	wanted.DownloadedAt = time.Now().UTC()
	// Ownership precedes the download, so a crashed partial download stays
	// inside the managed directory and can be reaped on the next startup.
	if err := os.WriteFile(filepath.Join(staging, "source.json"), marshal(wanted), 0600); err != nil {
		return "", 0, err
	}
	downloadCtx := context.WithValue(ctx, paperPDFDownloadDir{}, staging)
	path, _, err := acquire(downloadCtx, FulltextCandidate{URL: metadata.PDFURL, SourceType: "official_pdf", SourceProvider: "arxiv", Version: metadata.Version, ExpectedMIME: "application/pdf"})
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(path)
	in, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(filepath.Join(staging, "paper.pdf"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	wanted.Size, err = io.Copy(io.MultiWriter(out, h), io.LimitReader(in, maxPaperBytes+1))
	closeErr := out.Close()
	if err != nil {
		return "", 0, err
	}
	if closeErr != nil {
		return "", 0, closeErr
	}
	if wanted.Size == 0 || wanted.Size > maxPaperBytes {
		return "", 0, errors.New("invalid cached PDF size")
	}
	wanted.SHA256 = hex.EncodeToString(h.Sum(nil))
	wanted.DownloadedAt = time.Now().UTC()
	if _, _, err = hashCachedPDF(filepath.Join(staging, "paper.pdf")); err != nil {
		return "", 0, err
	}
	if err = os.WriteFile(filepath.Join(staging, "source.json"), marshal(wanted), 0600); err != nil {
		return "", 0, err
	}
	if err = ctx.Err(); err != nil {
		return "", 0, err
	}
	if err = os.Remove(path); err != nil {
		return "", 0, err
	}
	if err = os.Rename(staging, directory); err != nil {
		return "", 0, err
	}
	log.Logger.Info().Str("paper_id", ref.PaperID).Str("version", ref.SourceVersion).Int64("bytes", wanted.Size).Msg("academic PDF cached after download")
	return filepath.Join(directory, "paper.pdf"), wanted.Size, nil
}

func hashCachedPDF(path string) (string, int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxPaperBytes {
		return "", 0, errors.New("invalid cached PDF file")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	header := make([]byte, 5)
	if _, err = io.ReadFull(f, header); err != nil || string(header) != "%PDF-" {
		return "", 0, errors.New("invalid cached PDF header")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return "", 0, err
	}
	h := sha256.New()
	size, err := io.Copy(h, io.LimitReader(f, maxPaperBytes+1))
	return hex.EncodeToString(h.Sum(nil)), size, err
}

func readCachedPDF(directory string) (cachedPDF, error) {
	var record cachedPDF
	info, err := os.Lstat(directory)
	if err != nil {
		return record, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return record, errors.New("invalid cache entry")
	}
	path := filepath.Join(directory, "source.json")
	info, err = os.Lstat(path)
	if err != nil {
		return record, err
	}
	if !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return record, errors.New("invalid cache source record")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return record, err
	}
	if err = json.Unmarshal(data, &record); err != nil {
		return record, err
	}
	id, e := arxivsource.Parse(record.BaseID + record.Version)
	if e != nil || id.Version == "" || record.Provider != "arxiv" || record.URL != id.PDFURL() || record.UserID == "" || record.DownloadedAt.IsZero() {
		return record, errors.New("invalid cache source identity")
	}
	if name := filepath.Base(directory); name != record.key() && !strings.HasPrefix(name, record.key()+".partial-") {
		return record, errors.New("cache identity mismatch")
	}
	return record, nil
}

func paperPDFCleanupBoundary(now time.Time) time.Time {
	zone, _ := time.LoadLocation("Asia/Shanghai")
	local := now.In(zone)
	boundary := time.Date(local.Year(), local.Month(), local.Day(), 3, 0, 0, 0, zone)
	if local.Before(boundary) {
		boundary = boundary.AddDate(0, 0, -1)
	}
	return boundary
}

func cleanupPaperPDFs(ctx context.Context, db *gorm.DB, now time.Time) (int, int64, error) {
	root := paperPDFCacheRoot()
	if info, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	} else if err != nil {
		return 0, 0, err
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return 0, 0, errors.New("invalid PDF cleanup root")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, 0, err
	}
	count := 0
	var size int64
	var failures error
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		directory := filepath.Join(root, entry.Name())
		record, e := readCachedPDF(directory)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			failures = errors.Join(failures, e)
			continue
		}
		partial := strings.HasPrefix(entry.Name(), record.key()+".partial-")
		if !partial && !record.DownloadedAt.Before(paperPDFCleanupBoundary(now)) {
			continue
		}
		attempt, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		guard, e := lockArxivImport(attempt, db, record.BaseID)
		if e != nil {
			cancel()
			if !errors.Is(e, context.DeadlineExceeded) && !errors.Is(e, context.Canceled) {
				failures = errors.Join(failures, e)
			}
			continue
		}
		// The entry may have been replaced while waiting for its resource lock.
		current, e := readCachedPDF(directory)
		if e == nil {
			e = guard.check()
		}
		if e == nil && (partial || current.DownloadedAt.Before(paperPDFCleanupBoundary(now))) {
			if e = os.RemoveAll(directory); e == nil {
				count++
				size += current.Size
			}
		}
		guard.release()
		cancel()
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			failures = errors.Join(failures, e)
		}
	}
	return count, size, failures
}

func StartPaperPDFCleanup(ctx context.Context, db *gorm.DB) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			now := time.Now()
			count, size, err := cleanupPaperPDFs(ctx, db, now)
			if err != nil {
				log.Logger.Warn().Err(err).Msg("academic PDF cleanup failed")
			}
			log.Logger.Info().Int("files", count).Int64("bytes", size).Msg("academic PDF cleanup finished")
			next := paperPDFCleanupBoundary(now).AddDate(0, 0, 1)
			timer := time.NewTimer(time.Until(next))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	return done
}
