package doc

import (
	"encoding/json"
	"io"
	"lazymind/core/pdfir"
	"os"
	"path/filepath"
	"reflect"
)

// Upgrade only the additive projection of a completed, source-matched artifact.
// The retained archive belongs to this job; no provider call or new job is needed.
// A missing/corrupt archive must not make an otherwise usable legacy IR fail.
func upgradePDFIRProjection(d *pdfir.Document, result pdfIRResult) {
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
	file, err := os.CreateTemp(filepath.Dir(result.IRPath), ".pdf-ir-projection-*.json")
	if err != nil {
		warn()
		return
	}
	temp := file.Name()
	defer os.Remove(temp)
	err = json.NewEncoder(file).Encode(upgraded)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		warn()
		return
	}
	if os.Rename(temp, result.IRPath) != nil {
		warn()
		return
	}
	*d = upgraded
}
