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
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, _ := z.Create("fixture_content_list.json")
	f.Write([]byte(`[{"type":"text","text":"Experimental study","page_idx":0,"bbox":[100,100,900,200]},{"type":"text","text":"The authors report an experiment with samples and specify the conditions.","page_idx":0,"bbox":[100,300,900,500]}]`))
	f, _ = z.Create("layout.json")
	f.Write([]byte(`{"_backend":"hybrid","_version_name":"3.4.4","pdf_info":[{"page_idx":0,"page_size":[100,100],"preproc_blocks":[{"type":"text","bbox":[10,10,90,20],"lines":[{"spans":[{"type":"text","content":"Experimental study","bbox":[10,10,90,20]}]}]},{"type":"text","bbox":[10,30,90,50],"lines":[{"spans":[{"type":"text","content":"The authors report an experiment with samples and specify the conditions.","bbox":[10,30,90,50]}]}]}],"para_blocks":[{"type":"text","bbox":[10,10,90,20],"lines":[{"spans":[{"type":"text","content":"Experimental study","bbox":[10,10,90,20]}]}]},{"type":"text","bbox":[10,30,90,50],"lines":[{"spans":[{"type":"text","content":"The authors report an experiment with samples and specify the conditions.","bbox":[10,30,90,50]}]}]}]}]}`))
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
