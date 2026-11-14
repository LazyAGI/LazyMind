package pdfir_test

import (
	"encoding/json"
	"lazymind/core/paperguide"
	"lazymind/core/pdfir"
	"os"
	"path/filepath"
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
			if key == "pantry" || key == "greenspace-review" || key == "dataset_resource" || key == "review_synthesis" || key == "react" {
				if e == nil || !strings.Contains(e.Error(), "table requires one complete body") {
					t.Fatalf("expected unresolved multi-page asset provenance, got %v", e)
				}
				t.Logf("explicit unavailable: %v", e)
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			s, e := paperguide.BuildSourceSnapshot(enriched, paperguide.SourceIdentity{SourceSHA256: x.Hash, OwnerID: "offline", DatasetID: "offline", DocumentID: x.Document})
			if e != nil {
				t.Fatal(e)
			}
			if e = paperguide.ValidateSnapshot(s, s.Identity, s.IRDigest); e != nil {
				t.Fatal(e)
			}
			t.Logf("base=%d regions=%d evidence=%d sections=%d projection+catalog+validation=%s", len(base.Blocks), len(enriched.Precision.Regions), len(s.Evidence), len(s.Sections), time.Since(started))
		})
	}
}
