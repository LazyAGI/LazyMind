package doc

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"lazymind/core/common/orm"
	"lazymind/core/paperguide"
	"lazymind/core/pdfir"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func guidePrecisionFixture(t *testing.T, hash string) ([]byte, pdfir.Document) {
	return guidePrecisionFixtureWithBodyBox(t, hash, "90")
}

func guidePrecisionFixtureWithBodyBox(t *testing.T, hash, bodyRight string) ([]byte, pdfir.Document) {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, _ := z.Create("fixture_content_list.json")
	content := `[{"type":"text","text":"Experimental study","page_idx":0,"bbox":[100,100,900,200]},{"type":"text","text":"The authors report an experiment with samples and specify the conditions.","page_idx":0,"bbox":[100,300,900,500]}]`
	f.Write([]byte(strings.ReplaceAll(content, "900,500", bodyRight+"0,500")))
	f, _ = z.Create("layout.json")
	layout := `{"_backend":"hybrid","_version_name":"3.4.4","pdf_info":[{"page_idx":0,"page_size":[100,100],"preproc_blocks":[{"type":"text","bbox":[10,10,90,20],"lines":[{"spans":[{"type":"text","content":"Experimental study","bbox":[10,10,90,20]}]}]},{"type":"text","bbox":[10,30,90,50],"lines":[{"spans":[{"type":"text","content":"The authors report an experiment with samples and specify the conditions.","bbox":[10,30,90,50]}]}]}],"para_blocks":[{"type":"text","bbox":[10,10,90,20],"lines":[{"spans":[{"type":"text","content":"Experimental study","bbox":[10,10,90,20]}]}]},{"type":"text","bbox":[10,30,90,50],"lines":[{"spans":[{"type":"text","content":"The authors report an experiment with samples and specify the conditions.","bbox":[10,30,90,50]}]}]}]}]}`
	f.Write([]byte(strings.ReplaceAll(layout, "90,50", bodyRight+",50")))
	z.Close()
	d, e := pdfir.AdaptZIP(b.Bytes(), hash, "vlm")
	if e != nil {
		t.Fatal(e)
	}
	return b.Bytes(), d
}
func TestGuidePrecisionArtifactIsImmutableSibling(t *testing.T) {
	raw, base := guidePrecisionFixture(t, pdfir.Digest("source"))
	dir := t.TempDir()
	result := pdfIRResult{IRPath: filepath.Join(dir, "document-ir.json"), RawPath: filepath.Join(dir, "mineru-result.zip")}
	before, _ := json.Marshal(base)
	os.WriteFile(result.IRPath, before, 0600)
	os.WriteFile(result.RawPath, raw, 0600)
	legacy := filepath.Join(dir, "source-precision-v2-frozen.json")
	legacyBody := []byte(`{"frozen":"historical precision v2"}`)
	if e := os.WriteFile(legacy, legacyBody, 0600); e != nil {
		t.Fatal(e)
	}
	legacyInfo, _ := os.Stat(legacy)
	info, _ := os.Stat(result.IRPath)
	enriched, path, e := loadGuidePrecision(base, result, false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("read-only load wrote artifact")
	}
	written, path2, e := loadGuidePrecision(base, result, true)
	if e != nil || path == result.IRPath || path != path2 || !reflect.DeepEqual(enriched, written) {
		t.Fatalf("bad immutable write %s %v", path, e)
	}
	oldBody, _ := os.ReadFile(legacy)
	oldInfo, _ := os.Stat(legacy)
	if !strings.HasPrefix(filepath.Base(path), "source-precision-v3-") || !bytes.Equal(oldBody, legacyBody) || !oldInfo.ModTime().Equal(legacyInfo.ModTime()) {
		t.Fatal("new projection overwrote historical sibling")
	}
	after, _ := os.ReadFile(result.IRPath)
	afterInfo, _ := os.Stat(result.IRPath)
	zipAfter, _ := os.ReadFile(result.RawPath)
	if !bytes.Equal(before, after) || !info.ModTime().Equal(afterInfo.ModTime()) || !bytes.Equal(raw, zipAfter) {
		t.Fatal("modified original artifact")
	}
	stat, _ := os.Stat(path)
	_, _, e = loadGuidePrecision(base, result, true)
	later, _ := os.Stat(path)
	if e != nil || !stat.ModTime().Equal(later.ModTime()) {
		t.Fatal("rewrote immutable artifact")
	}
	os.WriteFile(path, []byte("corrupt"), 0600)
	if _, _, e = loadGuidePrecision(base, result, true); e == nil {
		t.Fatal("silently replaced corrupt artifact")
	}
	os.Remove(result.RawPath)
	if _, _, e = loadGuidePrecision(base, result, false); e == nil {
		t.Fatal("missing archive accepted")
	}
}

func TestGuidePrecisionLoaderNeverReparsesMissingArchive(t *testing.T) {
	db, _, _ := guideHTTPFixture(t)
	var job orm.AsyncJob
	db.Where("id = ?", "guide-ir").Take(&job)
	var artifact pdfIRResult
	json.Unmarshal(job.ResultJSON, &artifact)
	before, _ := os.ReadFile(artifact.IRPath)
	info, _ := os.Stat(artifact.IRPath)
	os.Remove(artifact.RawPath)
	scope := paperguide.Scope{OwnerID: "owner", DatasetID: "guide-ds", DocumentID: "guide-doc", Language: "en"}
	state, err := (paperGuideSource{}).Load(t.Context(), scope, false)
	if err != nil || state.Snapshot != nil || !state.PrecisionUnavailable || state.CanPrepare {
		t.Fatalf("precision silently available: %+v %v", state, err)
	}
	_, err = (paperGuideSource{}).Load(t.Context(), scope, true)
	if !errors.Is(err, paperguide.ErrPrecision) {
		t.Fatalf("missing archive didn't fail closed: %v", err)
	}
	var count int64
	db.Model(&orm.AsyncJob{}).Count(&count)
	if count != 1 {
		t.Fatal("created parser job")
	}
	after, _ := os.ReadFile(artifact.IRPath)
	later, _ := os.Stat(artifact.IRPath)
	if !bytes.Equal(before, after) || !info.ModTime().Equal(later.ModTime()) {
		t.Fatal("base artifact rewritten")
	}
}

func TestGuidePrecisionLoaderRejectsUnboundCanonicalPayload(t *testing.T) {
	raw, _ := guidePrecisionFixture(t, pdfir.Digest("source"))
	z, _ := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	var changed bytes.Buffer
	w := zip.NewWriter(&changed)
	for _, f := range z.File {
		r, _ := f.Open()
		data, _ := io.ReadAll(r)
		r.Close()
		if strings.HasSuffix(f.Name, "_content_list.json") {
			data = bytes.Replace(data, []byte("Experimental study"), []byte("Experimental study unproven continuation"), 1)
		}
		out, _ := w.Create(f.Name)
		out.Write(data)
	}
	w.Close()
	base, e := pdfir.AdaptZIP(changed.Bytes(), pdfir.Digest("source"), "vlm")
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	result := pdfIRResult{IRPath: filepath.Join(dir, "document-ir.json"), RawPath: filepath.Join(dir, "mineru-result.zip")}
	before, _ := json.Marshal(base)
	os.WriteFile(result.IRPath, before, 0600)
	os.WriteFile(result.RawPath, changed.Bytes(), 0600)
	for _, prepare := range []bool{false, true} {
		if _, _, e := loadGuidePrecision(base, result, prepare); e == nil || !strings.Contains(e.Error(), "complete producer render") {
			t.Fatalf("unproven payload reached precision cache: %v", e)
		}
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 2 {
		t.Fatal("failed preparation wrote precision artifact")
	}
	after, _ := os.ReadFile(result.IRPath)
	if !bytes.Equal(before, after) {
		t.Fatal("canonical source changed")
	}
}

func TestGuideSnapshotPathBindsOriginalFigureProofEvenIfArtifactRehashesIt(t *testing.T) {
	hash := strings.Repeat("a", 64)
	raw, base := guidePrecisionFixture(t, hash)
	projected, err := pdfir.ProjectSourcePrecision(base, raw)
	if err != nil {
		t.Fatal(err)
	}
	block := pdfir.Block{ID: "test-chart", Type: "chart", Page: 0, BBox: []float64{.1, .2, .3, .4}}
	projected.Blocks = append(projected.Blocks, block)
	id := paperguide.SourceIdentity{SourceSHA256: hash, DatasetID: "data", DocumentID: "paper", OwnerID: "owner"}
	proof := pdfir.OriginalFigureProof{BlockID: block.ID, Type: block.Type, Page: block.Page, Parts: [][]float64{block.BBox}}
	s, err := paperguide.BuildSourceSnapshotWithFigureProofs(projected, id, []pdfir.OriginalFigureProof{proof})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), s.SnapshotFilename())
	write := func(snapshot paperguide.GuideSourceSnapshot) {
		data, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(s)
	source := paperGuideSource{}
	if _, err = source.Read(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	proof.Parts = [][]float64{{.15, .2, .3, .4}}
	forged, err := paperguide.BuildSourceSnapshotWithFigureProofs(projected, id, []pdfir.OriginalFigureProof{proof})
	if err != nil {
		t.Fatal(err)
	}
	if err = paperguide.ValidateSnapshot(forged, id, s.IRDigest); err != nil {
		t.Fatal("fixture should be internally consistent", err)
	}
	write(forged)
	if _, err = source.Read(t.Context(), path); !errors.Is(err, paperguide.ErrEvidence) {
		t.Fatalf("rehashed proof replaced the path commitment: %v", err)
	}
}
