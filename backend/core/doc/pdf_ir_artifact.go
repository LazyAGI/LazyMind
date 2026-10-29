package doc

import (
	"encoding/json"
	"lazymind/core/pdfir"
	"os"
	"path/filepath"
)

// Bound serialized bytes, including escaping and additive projections, before
// publishing either a new job artifact or an upgrade of a usable cached IR.
func writePDFIRArtifact(path string, d pdfir.Document, limit int64) error {
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if int64(len(b)) > limit {
		return errPDFIRTooLarge
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pdf-ir-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(b)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
