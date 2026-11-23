package pdfir_test

import (
	"encoding/json"
	"lazymind/core/paperguide"
	"lazymind/core/pdfir"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Explicit offline opt-in: reads retained archives and authorized IR only.
// No database, parser, model, HTTP or original-artifact write is involved.
func TestPrecisionRetainedCorpus(t *testing.T) {
	root := os.Getenv("LAZYMIND_PRECISION_CORPUS_ROOT")
	if root == "" {
		t.Skip("offline retained corpus not requested")
	}
	raw, err := os.ReadFile(filepath.Join(root, ".superpowers/sdd/实施与验收/corpus-ready.json"))
	if err != nil {
		t.Fatal(err)
	}
	var corpus map[string]struct {
		Document string `json:"document_id"`
		IR       string `json:"ir_path"`
		Hash     string `json:"source_sha256"`
	}
	if json.Unmarshal(raw, &corpus) != nil {
		t.Fatal("invalid corpus")
	}
	archives := map[string][]string{}
	err = filepath.WalkDir(filepath.Join(root, "data/core/uploads"), func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() && d.Name() == "mineru-result.zip" {
			for key, x := range corpus {
				if strings.Contains(path, string(filepath.Separator)+x.Document+string(filepath.Separator)) {
					archives[key] = append(archives[key], path)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	type fragmentTarget struct {
		Page     int    `json:"page"`
		HTMLHash string `json:"html_sha256"`
		Rows     int    `json:"rows"`
		First    string `json:"first_cell"`
		Last     string `json:"last_cell"`
	}
	var targets []struct {
		Paper     string           `json:"paper"`
		ParentID  string           `json:"parent_id"`
		Fragments []fragmentTarget `json:"fragments"`
	}
	if path := os.Getenv("LAZYMIND_TABLE_PROVENANCE_TARGETS"); path != "" {
		data, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		if e = json.Unmarshal(data, &targets); e != nil {
			t.Fatal(e)
		}
		if len(targets) != 13 {
			t.Fatal("expected thirteen independently audited tables")
		}
	}
	for key, x := range corpus {
		t.Run(key, func(t *testing.T) {
			data, e := os.ReadFile(x.IR)
			if e != nil {
				t.Fatal(e)
			}
			var base pdfir.Document
			if json.Unmarshal(data, &base) != nil {
				t.Fatal("bad IR")
			}
			if base.SourceSHA256 != x.Hash || len(archives[key]) == 0 {
				t.Fatal("source mismatch")
			}
			raw, e := os.ReadFile(archives[key][0])
			if e != nil {
				t.Fatal(e)
			}
			started := time.Now()
			enriched, e := pdfir.ProjectSourcePrecision(base, raw)

			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(base.Blocks, enriched.Blocks) {
				t.Fatal("canonical blocks changed")
			}
			for _, target := range targets {
				if target.Paper != key {
					continue
				}
				actual := []pdfir.SourceRegion{}
				for _, r := range enriched.Precision.Regions {
					if r.ParentBlockID == target.ParentID && r.Kind == "table_body_fragment" {
						actual = append(actual, r)
					}
				}
				if len(actual) != len(target.Fragments) {
					t.Fatalf("fragment count %s: %d", target.ParentID, len(actual))
				}
				for i, want := range target.Fragments {
					got := actual[i]
					rows := got.Table.Rows
					if got.Page != want.Page || pdfir.Digest(got.Table.HTML) != want.HTMLHash || len(rows) != want.Rows || rows[0].Cells[0].Text != want.First || rows[len(rows)-1].Cells[len(rows[len(rows)-1].Cells)-1].Text != want.Last {
						t.Fatalf("physical source mismatch parent=%s page=%d", target.ParentID, want.Page)
					}
				}
				t.Logf("verified table %s physical fragments=%d", target.ParentID, len(actual))
			}
			s, e := paperguide.BuildSourceSnapshot(enriched, paperguide.SourceIdentity{SourceSHA256: x.Hash, OwnerID: "offline", DatasetID: "offline", DocumentID: x.Document})
			if e != nil {
				t.Fatal(e)
			}
			if e = paperguide.ValidateSnapshot(s, s.Identity, s.IRDigest); e != nil {
				t.Fatal(e)
			}
			pageOnly, precise := 0, 0
			for _, e := range s.Evidence {
				if e.Locator.Kind == "page" {
					pageOnly++
				} else {
					precise++
				}
			}
			t.Logf("containment_restrictions=%d page_only_evidence=%d precise_evidence=%d assets=%d", len(*enriched.Precision.PageOnlyBlockIDs), pageOnly, precise, len(s.Assets))
			t.Logf("base=%d regions=%d evidence=%d sections=%d projection+catalog+validation=%s", len(base.Blocks), len(enriched.Precision.Regions), len(s.Evidence), len(s.Sections), time.Since(started))
		})
	}
}
