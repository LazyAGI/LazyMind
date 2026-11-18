package paperguide

import (
	"fmt"
	"lazymind/core/pdfir"
	"testing"
)

func tableFragmentDocument(t *testing.T) (pdfir.Document, SourceIdentity) {
	t.Helper()
	d, id := fixture(t)
	blocks := []pdfir.Block{}
	for _, b := range d.Blocks {
		if b.Type != "table" {
			blocks = append(blocks, b)
		}
	}
	d.Blocks = blocks
	rows := []pdfir.TableRow{{Cells: []pdfir.TableCell{{Text: "home 11", RowSpan: 1, ColSpan: 1}}}, {Cells: []pdfir.TableCell{{Text: "later 22", RowSpan: 1, ColSpan: 1}}}}
	d.Blocks = append(d.Blocks, pdfir.Block{ID: "continuation", Type: "table", Page: 1, Table: &pdfir.Table{Rows: []pdfir.TableRow{}}}, pdfir.Block{ID: "physical-parent", Type: "table", Page: 0, BBox: []float64{.1, .1, .9, .8}, Table: &pdfir.Table{Rows: rows}})
	d.Precision = &pdfir.SourcePrecision{Version: pdfir.SourcePrecisionVersion, SourceSHA256: d.SourceSHA256, BaseIRDigest: pdfir.Digest("base"), ArchiveDigest: pdfir.Digest("zip")}
	for i, text := range []string{"home 11", "later 22"} {
		body := "<table><tr><td>" + text + "</td></tr></table>"
		rs, _, e := pdfir.ParseSourceTable(body)
		if e != nil {
			t.Fatal(e)
		}
		payload := &pdfir.SourceTableFragment{HTML: body, Rows: rs, BBox: []float64{.1, .1, .9, .8}, PageSize: []float64{100, 100}}
		if i == 1 {
			payload.ContinuationBlockID = "continuation"
		}
		d.Precision.Regions = append(d.Precision.Regions, pdfir.SourceRegion{ID: fmt.Sprintf("fragment%d", i), Kind: "table_body_fragment", ParentBlockID: "physical-parent", Page: i, Text: text, Table: payload})
	}
	return d, id
}
func TestHistoricalPrecisionV1CatalogV3Reconstruction(t *testing.T) {
	d, id := fixture(t)
	d.Precision = &pdfir.SourcePrecision{Version: 1, SourceSHA256: d.SourceSHA256, BaseIRDigest: pdfir.Digest("base"), ArchiveDigest: pdfir.Digest("archive"), Regions: []pdfir.SourceRegion{}}
	old, e := buildSourceSnapshot(d, id, "paper-guide-source-v3")
	if e != nil {
		t.Fatal(e)
	}
	if e = ValidateSnapshot(old, id, old.IRDigest); e != nil {
		t.Fatal(e)
	}
	if _, e = BuildSourceSnapshot(d, id); e == nil {
		t.Fatal("old precision activated as current")
	}
	for _, e := range old.Evidence {
		if len(e.FragmentEvidenceIDs) > 0 || e.ParentAssetID != "" {
			t.Fatal("historical ID reinterpreted")
		}
	}
	d.Precision = nil
	current, e := BuildSourceSnapshot(d, id)
	if e != nil {
		t.Fatal(e)
	}
	if current.IRDigest == old.IRDigest || current.Evidence[0].ID == old.Evidence[0].ID {
		t.Fatal("old source reused")
	}
}

func TestSnapshotRejectsForgedTableFragmentAssociation(t *testing.T) {
	for _, name := range []string{"wrong merged value", "missing start page", "fake continuation", "unproven prefix", "missing table regions"} {
		t.Run(name, func(t *testing.T) {
			d, id := tableFragmentDocument(t)
			switch name {
			case "wrong merged value":
				d.Blocks[len(d.Blocks)-1].Table.Rows[0].Cells[0].Text = "unaccounted canonical value"
			case "missing start page":
				d.Precision.Regions[0].Page = 1
			case "fake continuation":
				d.Precision.Regions[0].Table.ContinuationBlockID = "unknown"
			case "unproven prefix":
				d.Precision.Regions[0].Table.OmittedPrefixRows = 1
			case "missing table regions":
				d.Precision.Regions = nil
			}
			if _, e := BuildSourceSnapshot(d, id); e == nil {
				t.Fatal("forged table provenance accepted")
			}
		})
	}
}

func TestIndependentVerifiedEmptyTableKeepsMissingMaterialAsset(t *testing.T) {
	d, id := tableFragmentDocument(t)
	d.Blocks = append(d.Blocks, pdfir.Block{ID: "empty-independent", Type: "table", Page: 0, Table: &pdfir.Table{Rows: []pdfir.TableRow{}}})
	d.Precision.Regions = append(d.Precision.Regions, pdfir.SourceRegion{ID: "empty-region", Kind: "table_body_fragment", ParentBlockID: "empty-independent", Page: 0, Table: &pdfir.SourceTableFragment{HTML: "<table></table>", Rows: []pdfir.TableRow{}, BBox: []float64{.1, .1, .9, .8}, PageSize: []float64{100, 100}}})
	s, e := BuildSourceSnapshot(d, id)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, a := range s.Assets {
		if a.BlockID == "empty-independent" {
			found = true
			if len(a.Warnings) == 0 {
				t.Fatal("missing content warning lost")
			}
		}
	}
	if !found {
		t.Fatal("empty table silently removed")
	}
}
