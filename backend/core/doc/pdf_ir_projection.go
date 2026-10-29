package doc

import (
	"errors"
	"io"
	"lazymind/core/pdfir"
	"os"
	"reflect"
)

// Upgrade only the additive projection of a completed, source-matched artifact.
// The retained archive belongs to this job; no provider call or new job is needed.
// A missing/corrupt archive must not make an otherwise usable legacy IR fail.
func upgradePDFIRProjection(d *pdfir.Document, result pdfIRResult) {
	upgradePDFIRProjectionWithLimit(d, result, pdfIRMaxArtifactBytes)
}

func upgradePDFIRProjectionWithLimit(d *pdfir.Document, result pdfIRResult, limit int64) {
	if d.ProjectionVersion >= pdfir.ProjectionVersion {
		return
	}
	warn := func() {
		d.Warnings = append(d.Warnings, "Table projection could not be upgraded from the retained archive")
	}
	f, err := os.Open(result.RawPath)
	if err != nil {
		warn()
		return
	}
	raw, err := io.ReadAll(io.LimitReader(f, pdfir.MaxArchiveBytes+1))
	f.Close()
	if err != nil || int64(len(raw)) > pdfir.MaxArchiveBytes {
		warn()
		return
	}
	upgraded, err := pdfir.AdaptZIP(raw, d.SourceSHA256, d.Parser.Model)
	if err != nil || len(upgraded.Blocks) < len(d.Blocks) {
		warn()
		return
	}
	// Check provenance against every original block before replacing the cache.
	for i, b := range d.Blocks {
		candidate := upgraded.Blocks[i]
		candidate.Table = nil
		candidate.Image = nil
		b.Table = nil
		b.Image = nil
		if !reflect.DeepEqual(b, candidate) {
			warn()
			return
		}
	}
	if err := writePDFIRArtifact(result.IRPath, upgraded, limit); err != nil {
		if errors.Is(err, errPDFIRTooLarge) {
			d.Warnings = append(d.Warnings, "Updated PDF reading structure exceeds the size limit; the existing structure is retained. Use a smaller PDF for the updated projection.")
		} else {
			warn()
		}
		return
	}
	*d = upgraded
}
