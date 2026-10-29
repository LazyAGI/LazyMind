package doc

import (
	"bytes"
	"encoding/json"
	"errors"
	"lazymind/core/pdfir"
	"os"
	"path/filepath"
	"testing"
)

func TestPDFIRArtifactSerializedBudgetAndPreservation(t *testing.T) {
	// Escaped text expands on serialization, so the limit must apply to JSON bytes.
	d := pdfir.Document{SourceSHA256: "hash", Blocks: []pdfir.Block{{Text: "<>&"}}}
	encoded, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ir.json")
	if err := writePDFIRArtifact(path, d, int64(len(encoded))); err != nil {
		t.Fatalf("exact-limit artifact rejected: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, encoded) {
		t.Fatalf("artifact differs from bounded bytes: %v", err)
	}
	d.Blocks[0].Text += "x"
	if err := writePDFIRArtifact(path, d, int64(len(encoded))); !errors.Is(err, errPDFIRTooLarge) {
		t.Fatalf("one-byte overflow accepted: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("oversize write replaced usable artifact: %v", err)
	}
	absent := filepath.Join(filepath.Dir(path), "absent.json")
	if err := writePDFIRArtifact(absent, d, int64(len(encoded))); !errors.Is(err, errPDFIRTooLarge) {
		t.Fatalf("new oversized artifact accepted: %v", err)
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatalf("oversized new artifact was published: %v", err)
	}
}
