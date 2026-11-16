package doc

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"lazymind/core/common/orm"
	"lazymind/core/pdfir"
	"os"
	"path/filepath"
	"strings"
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

func TestSharedArchiveCannotBypassSerializedIRBudget(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source.pdf")
	os.WriteFile(source, []byte("%PDF-shared-large"), 0600)
	hash, _ := pdfir.SourceHash(source)
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	f, _ := zw.Create("paper_content_list.json")
	// Raw '<' is legal JSON but expands sixfold when serving a Go JSON object.
	f.Write([]byte(`[{"type":"text","text":"` + strings.Repeat("<", 6<<20) + `","page_idx":0,"bbox":[0,0,100,100]}]`))
	zw.Close()
	path := pdfir.CachePath(source, hash, "vlm")
	os.MkdirAll(filepath.Dir(path), 0700)
	os.WriteFile(path, archive.Bytes(), 0600)
	state := pdfIRStatusFromJobs(orm.Document{Ext: mustJSON(documentExt{StoredPath: source})}, hash, nil)
	if state.Document != nil || state.Status != "failed" || state.ErrorMessage != pdfIRTooLargeMessage {
		t.Fatalf("oversized shared IR was exposed: status=%s doc=%v error=%s", state.Status, state.Document != nil, state.ErrorMessage)
	}
}
