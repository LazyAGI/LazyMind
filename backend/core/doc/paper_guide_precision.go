package doc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"lazymind/core/pdfir"
	"os"
	"path/filepath"
)

// loadGuidePrecision is deliberately separate from the reader's legacy table
// upgrade path. Neither the base IR nor its retained ZIP is ever rewritten.
// prepare=false is read-only, including when the immutable sibling is absent.
func loadGuidePrecision(base pdfir.Document, result pdfIRResult, prepare bool) (pdfir.Document, string, error) {
	return loadGuidePrecisionWithFigures(base, result, prepare, nil)
}

func loadGuidePrecisionWithFigures(base pdfir.Document, result pdfIRResult, prepare bool, figures *[]pdfir.OriginalFigureProof) (pdfir.Document, string, error) {
	raw := result.archive
	var err error
	if raw == nil {
		f, err := os.Open(result.RawPath)
		if err != nil {
			return pdfir.Document{}, "", err
		}
		raw, err = io.ReadAll(io.LimitReader(f, pdfir.MaxArchiveBytes+1))
		f.Close()
		if err != nil {
			return pdfir.Document{}, "", err
		}
	}
	var enriched pdfir.Document
	if figures == nil {
		enriched, err = pdfir.ProjectSourcePrecision(base, raw)
	} else {
		enriched, *figures, err = pdfir.ProjectGuideSourcePrecision(base, raw)
	}
	if err != nil {
		return pdfir.Document{}, "", err
	}
	p := enriched.Precision
	key := pdfir.Digest(fmt.Sprintf("%s:%s:%s:%d", p.SourceSHA256, p.BaseIRDigest, p.ArchiveDigest, p.Version))
	path := filepath.Join(filepath.Dir(result.IRPath), fmt.Sprintf("source-precision-v%d-%s.json", p.Version, key))
	expected, err := json.Marshal(enriched)
	if err != nil {
		return pdfir.Document{}, "", err
	}
	if int64(len(expected)) > pdfIRMaxArtifactBytes {
		return pdfir.Document{}, "", errPDFIRTooLarge
	}
	verify := func() error {
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		defer f.Close()
		data, e := io.ReadAll(io.LimitReader(f, pdfIRMaxArtifactBytes+1))
		if e != nil {
			return e
		}
		if !bytes.Equal(data, expected) {
			return errors.New("immutable source precision artifact mismatch")
		}
		return nil
	}
	if err = verify(); err == nil {
		return enriched, path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return pdfir.Document{}, "", err
	}
	if !prepare {
		return enriched, path, nil
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".source-precision-*.tmp")
	if err != nil {
		return pdfir.Document{}, "", err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(expected); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return pdfir.Document{}, "", err
	}
	if closeErr != nil {
		return pdfir.Document{}, "", closeErr
	}
	// Publish atomically without replacing another process's immutable artifact.
	if err = os.Link(temp, path); err != nil && !errors.Is(err, os.ErrExist) {
		return pdfir.Document{}, "", err
	}
	if err = verify(); err != nil {
		return pdfir.Document{}, "", err
	}
	return enriched, path, nil
}
