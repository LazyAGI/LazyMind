package doc

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"lazymind/core/common/orm"
	"lazymind/core/pdfir"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestPDFIRStatusUpgradesLegacyProjectionFromRetainedArchive(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.AsyncJob{}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	f, _ := z.Create("paper_content_list.json")
	f.Write([]byte(`[{"type":"table","page_idx":0,"bbox":[10,10,900,900],"table_caption":["Table 1: Results"],"table_body":"<table><tr><td>A</td></tr></table>"}]`))
	z.Close()
	d, e := pdfir.AdaptZIP(buf.Bytes(), "hash", "vlm")
	if e != nil {
		t.Fatal(e)
	}
	d.ProjectionVersion = 0
	for i := range d.Blocks {
		d.Blocks[i].Table = nil
	}
	legacy := d.Blocks[0]
	dir := t.TempDir()
	irpath, rawpath := filepath.Join(dir, "ir.json"), filepath.Join(dir, "mineru-result.zip")
	os.WriteFile(irpath, mustJSON(d), 0600)
	os.WriteFile(rawpath, buf.Bytes(), 0600)
	row := orm.Document{ID: "doc", DatasetID: "ds"}
	db.Create(&orm.AsyncJob{ID: "projection-job", JobType: pdfIRJobType, ResourceType: "document_pdf_ir", ResourceID: pdfIRResource("ds", "doc"), Status: "succeeded", PayloadJSON: mustJSON(pdfIRPayload{Revision: pdfir.Revision, SourceSHA256: "hash"}), ResultJSON: mustJSON(pdfIRResult{IRPath: irpath, RawPath: rawpath}), CreatedAt: time.Now()})
	result, e := pdfIRStatus(t.Context(), row, "hash")
	if e != nil || result.Status != "ready" || result.Document.ProjectionVersion != pdfir.ProjectionVersion || result.Document.Blocks[0].Table == nil {
		t.Fatalf("upgrade failed: %+v %v", result, e)
	}
	upgraded := result.Document.Blocks[0]
	upgraded.Table = nil
	if !reflect.DeepEqual(legacy, upgraded) {
		t.Fatal("source block changed")
	}
	stored, _ := os.ReadFile(irpath)
	var persisted pdfir.Document
	json.Unmarshal(stored, &persisted)
	if persisted.ProjectionVersion != pdfir.ProjectionVersion {
		t.Fatal("upgrade not persisted")
	}
	raw, _ := os.ReadFile(rawpath)
	if !bytes.Equal(raw, buf.Bytes()) {
		t.Fatal("raw archive changed")
	}
	var count int64
	db.Model(&orm.AsyncJob{}).Count(&count)
	if count != 1 {
		t.Fatal("upgrade created a parse job")
	}
	os.Remove(rawpath)
	second, e := pdfIRStatus(t.Context(), row, "hash")
	if e != nil || second.Document.ProjectionVersion != pdfir.ProjectionVersion {
		t.Fatal("upgraded IR still requires raw archive")
	}
}
func TestPDFIRProjectionUpgradeRejectsMismatchedOrCorruptArchive(t *testing.T) {
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	f, _ := z.Create("other_content_list.json")
	f.Write([]byte(`[{"type":"text","page_idx":0,"text":"different document"}]`))
	z.Close()
	for _, raw := range []string{"not zip", "", archive.String()} {
		t.Run("invalid archive", func(t *testing.T) {
			dir := t.TempDir()
			ip, rp := filepath.Join(dir, "ir.json"), filepath.Join(dir, "raw.zip")
			d := pdfir.Document{Version: 1, SourceSHA256: "hash", Blocks: []pdfir.Block{{ID: "original", Type: "text", Page: 0, Text: "unchanged"}}}
			before := mustJSON(d)
			os.WriteFile(ip, before, 0600)
			os.WriteFile(rp, []byte(raw), 0600)
			upgradePDFIRProjection(&d, pdfIRResult{IRPath: ip, RawPath: rp})
			after, _ := os.ReadFile(ip)
			if !bytes.Equal(before, after) || d.ProjectionVersion != 0 || d.Blocks[0].Text != "unchanged" {
				t.Fatal("corrupt archive replaced legacy IR")
			}
		})
	}
}
func TestPDFIRProjectionV2UpgradeKeepsExistingTableCaptionIDs(t *testing.T) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	f, _ := z.Create("paper_content_list.json")
	f.Write([]byte(`[{"type":"table","page_idx":0,"bbox":[100,100,900,300],"table_body":"<table><tr><td>42</td></tr></table>"},{"type":"image","page_idx":1,"bbox":[100,100,900,300]}]`))
	f, _ = z.Create("layout.json")
	f.Write([]byte(`{"pdf_info":[{"page_idx":0,"page_size":[1000,1000],"para_blocks":[{"type":"table_caption","bbox":[100,310,900,330],"lines":[{"spans":[{"content":"Table 1: Values"}]}]}]},{"page_idx":1,"page_size":[1000,1000],"para_blocks":[{"type":"image_caption","bbox":[100,310,900,330],"lines":[{"spans":[{"content":"Figure 1: Method"}]}]}]}]}`))
	z.Close()
	d, e := pdfir.AdaptZIP(buf.Bytes(), "hash", "vlm")
	if e != nil {
		t.Fatal(e)
	}
	old := []pdfir.Block{}
	for _, b := range d.Blocks {
		if b.Type != "image_caption" {
			b.Image = nil
			old = append(old, b)
		}
	}
	d.Blocks = old
	d.ProjectionVersion = 2
	dir := t.TempDir()
	ip, rp := filepath.Join(dir, "ir.json"), filepath.Join(dir, "raw.zip")
	os.WriteFile(ip, mustJSON(d), 0600)
	os.WriteFile(rp, buf.Bytes(), 0600)
	upgradePDFIRProjection(&d, pdfIRResult{IRPath: ip, RawPath: rp})
	if d.ProjectionVersion != 3 || len(d.Blocks) != len(old)+1 || d.Blocks[1].Image.Number != "1" {
		t.Fatal("v2 image upgrade failed")
	}
	for i, b := range old {
		if b.ID != d.Blocks[i].ID || !reflect.DeepEqual(b.Table, d.Blocks[i].Table) {
			t.Fatal("existing table/source identity changed")
		}
	}
}

func TestPDFIRProjectionUpgradePreservesLegacyArtifactWhenOverBudget(t *testing.T) {
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	f, _ := z.Create("paper_content_list.json")
	f.Write([]byte(`[{"type":"table","page_idx":0,"bbox":[10,10,900,900],"table_caption":["Table 1: Results"],"table_body":"<table><tr><td>expanded table projection</td></tr></table>"}]`))
	z.Close()
	d, err := pdfir.AdaptZIP(archive.Bytes(), "hash", "vlm")
	if err != nil {
		t.Fatal(err)
	}
	d.ProjectionVersion = 0
	d.Blocks[0].Table = nil
	before := mustJSON(d)
	dir := t.TempDir()
	ip, rp := filepath.Join(dir, "ir.json"), filepath.Join(dir, "raw.zip")
	if err := os.WriteFile(ip, before, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rp, archive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	upgradePDFIRProjectionWithLimit(&d, pdfIRResult{IRPath: ip, RawPath: rp}, int64(len(before)))
	after, err := os.ReadFile(ip)
	if err != nil || !bytes.Equal(before, after) || d.ProjectionVersion != 0 || d.Blocks[0].Table != nil {
		t.Fatalf("oversized projection replaced legacy artifact: %v", err)
	}
	if len(d.Warnings) == 0 {
		t.Fatal("skipped upgrade must explain the size limit")
	}
}
