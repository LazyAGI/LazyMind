package pdfir_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"lazymind/core/paperguide"
	"lazymind/core/pdfir"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrecisionAuditedPhysicalPageTargets(t *testing.T) {
	root := os.Getenv("LAZYMIND_PRECISION_FIXTURES")
	if root == "" {
		t.Skip("offline source-derived excerpts not requested")
	}
	paths, err := filepath.Glob(filepath.Join(root, "*.json"))
	if err != nil || len(paths) != 5 {
		t.Fatal("missing source-derived fixtures")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var fixture struct {
				Hash    string          `json:"source_sha256"`
				Items   json.RawMessage `json:"content_items"`
				Layout  json.RawMessage `json:"layout"`
				Targets []struct {
					Page   int    `json:"physical_page"`
					Phrase string `json:"phrase"`
				} `json:"targets"`
			}
			if json.Unmarshal(data, &fixture) != nil {
				t.Fatal("invalid fixture")
			}
			var raw bytes.Buffer
			w := zip.NewWriter(&raw)
			f, _ := w.Create("source_content_list.json")
			f.Write(fixture.Items)
			f, _ = w.Create("layout.json")
			f.Write(fixture.Layout)
			w.Close()
			base, err := pdfir.AdaptZIP(raw.Bytes(), fixture.Hash, "vlm")
			if err != nil {
				t.Fatal(err)
			}
			d, err := pdfir.ProjectSourcePrecision(base, raw.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			s, err := paperguide.BuildSourceSnapshot(d, paperguide.SourceIdentity{SourceSHA256: fixture.Hash, OwnerID: "offline", DatasetID: "test", DocumentID: "source"})
			if err != nil {
				t.Fatal(err)
			}
			for _, target := range fixture.Targets {
				found := false
				for _, ev := range s.Evidence {
					if !strings.Contains(strings.Join(strings.Fields(ev.Text), " "), target.Phrase) {
						continue
					}
					found = true
					if ev.Locator.Page != target.Page-1 || ev.Locator.Kind != "page" || len(ev.Locator.BBox) != 0 || ev.SourceRegionID == "" {
						t.Fatalf("%q has false location: %+v", target.Phrase, ev.Locator)
					}
					loc, e := paperguide.ResolveTextLocator(s, ev.ID, target.Phrase)
					if e != nil || loc.Page != target.Page-1 || loc.Kind != "page" {
						t.Fatalf("quote lost page: %+v %v", loc, e)
					}
				}
				if !found {
					t.Fatalf("source target missing: %q", target.Phrase)
				}
			}
			// Every span is represented exactly once, and canonical parents still retain
			// their original whole text/starting page for reading and notes.
			if len(s.Assets) != 0 {
				t.Fatal("text regions became assets")
			}
			for i, b := range base.Blocks {
				if b.ID != s.Blocks[i].ID || b.Text != s.Blocks[i].Text || b.Page != s.Blocks[i].Page {
					t.Fatal("canonical parent changed")
				}
			}
		})
	}
}
