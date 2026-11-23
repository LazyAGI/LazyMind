package academic

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/pdfir"
)

func TestSelectedReviewUsesConfirmedMaterialsWithoutExpansion(t *testing.T) {
	db := paperFixture(t)
	if err := db.Model(&orm.Dataset{}).Where("id=?", "papers").Update("processing_level", "stored").Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		sources, err := seedSources(db.DB, "papers", []WorkflowPaper{{Paper: PaperRef{PaperID: id, SourceDocumentID: id}, IsSeed: true}})
		if err != nil {
			t.Fatal(err)
		}
		content := orm.AcademicPaperContent{ID: "body-" + id, UserID: "u", PaperID: id, ParserVersion: orm.AcademicPaperParserVersion,
			SourceKey: sources[0].key(), SourceKind: "kb", SourceDatasetID: "papers", SourceDocumentID: id, Status: "ready", ContentHash: "hash-" + id,
			PagesJSON: marshal([]ContentPage{{Page: 1, Text: "Original body of " + id}}), QualityJSON: marshal(contentQuality{Eligible: true, Identity: "matched", Material: "main", Text: "usable"})}
		if err := db.Create(&content).Error; err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	ResolvePaperSelection(w, paperRequest(`{"kind":"review","skip_expansion":true,"dataset_id":"papers","document_ids":["a","b","a","missing"]}`))
	var preview struct {
		Papers    []WorkflowPaper     `json:"papers"`
		Materials []reviewMaterial    `json:"materials"`
		Invalid   []map[string]string `json:"invalid"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(preview.Papers) != 2 || len(preview.Invalid) != 1 || len(preview.Materials) != 2 || !preview.Materials[0].Eligible || !preview.Materials[1].Eligible {
		t.Fatal(w.Code, w.Body.String())
	}
	body := `{"kind":"review","skip_expansion":true,"review_scope":"direct_references","dataset_id":"papers","document_ids":["a","b","missing"],"paper_ids":["a","b"],"user_instruction":"Compare tool learning","request_key":"direct-review"}`
	for i := 0; i < 2; i++ {
		w = httptest.NewRecorder()
		LaunchPaperWorkflow(w, paperRequest(body))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	var count int64
	db.Model(&orm.AcademicExpansion{}).Count(&count)
	if count != 0 {
		t.Fatalf("unexpected expansion: %d", count)
	}
	db.Model(&orm.AcademicHandoff{}).Count(&count)
	if count != 1 {
		t.Fatalf("duplicate handoff: %d", count)
	}
	var handoff orm.AcademicHandoff
	if err := db.First(&handoff).Error; err != nil {
		t.Fatal(err)
	}
	var input ReviewWorkflowInput
	if err := json.Unmarshal(handoff.InputJSON, &input); err != nil {
		t.Fatal(err)
	}
	if input.ReviewScope != "direct_references" || len(input.Papers) != 2 || input.UserInstruction != "Compare tool learning" {
		t.Fatalf("lost review context: %+v", input)
	}
	w = httptest.NewRecorder()
	ReadPaperContent(w, paperRequest(string(marshal(map[string]string{"handoff_id": handoff.ID, "paper_id": "a"}))))
	var read struct {
		Status string        `json:"status"`
		Pages  []ContentPage `json:"pages"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &read); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || read.Status != "ready" || len(read.Pages) != 1 || read.Pages[0].Text != "Original body of a" {
		t.Fatal(w.Code, w.Body.String())
	}
	// A changed source cannot silently shrink the user's confirmed selection.
	if err := db.Model(&orm.AcademicPaperContent{}).Where("paper_id=?", "b").Update("status", "failed").Error; err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	LaunchPaperWorkflow(w, paperRequest(`{"kind":"review","skip_expansion":true,"dataset_id":"papers","document_ids":["a","b"],"paper_ids":["a","b"],"request_key":"changed-source"}`))
	if w.Code != 422 {
		t.Fatal(w.Code, w.Body.String())
	}
}

// A library may have a retained reading structure without any retrieval chunks.
func TestSelectedReviewUsesParsedPDFWithoutRetrieval(t *testing.T) {
	db := paperFixture(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	for _, id := range []string{"a", "b"} {
		title := "Selected review paper " + id
		pdf := filepath.Join(root, id+".pdf")
		raw := "%PDF-1.7 " + id
		hash := pdfir.Digest(raw)
		if err := os.WriteFile(pdf, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&orm.Document{}).Where("id=?", id).Update("ext", marshal(map[string]string{"stored_path": pdf})).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&orm.AcademicWork{}).Where("id=?", id).Update("canonical_title", title).Error; err != nil {
			t.Fatal(err)
		}
		ir := pdfir.Document{Version: 1, SourceSHA256: hash, Pages: []pdfir.Page{{Index: 0}, {Index: 1}}, Blocks: []pdfir.Block{
			{Page: 0, Text: title}, {Page: 1, Text: strings.Repeat("We compared the measured performance in three experiments. ", 4)},
		}}
		path := filepath.Join(root, id+".json")
		if err := os.WriteFile(path, marshal(ir), 0600); err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.AsyncJob{ID: "parsed-" + id, JobType: "document_pdf_ir", ResourceType: "document_pdf_ir", ResourceID: pdfir.Digest("papers\x00" + id), Status: "succeeded", PayloadJSON: marshal(map[string]string{"source_sha256": hash, "revision": pdfir.Revision}), ResultJSON: marshal(map[string]string{"ir_path": path})}).Error; err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	ResolvePaperSelection(w, paperRequest(`{"kind":"review","skip_expansion":true,"dataset_id":"papers","document_ids":["a","b"]}`))
	var preview struct {
		Materials []reviewMaterial `json:"materials"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil || len(preview.Materials) != 2 {
		t.Fatal(w.Body.String(), err)
	}
	for _, material := range preview.Materials {
		if material.Status != "preparing" || material.Eligible {
			t.Fatalf("unexpected preview: %+v", material)
		}
	}
	var jobs []orm.AsyncJob
	if err := db.Where("job_type=?", "academic.paper_content").Find(&jobs).Error; err != nil || len(jobs) != 2 {
		t.Fatal(jobs, err)
	}
	for _, job := range jobs {
		if _, err := runPaperContent(t.Context(), asyncjob.Job{ResourceID: job.ResourceID, CreateUserID: "u", PayloadJSON: job.PayloadJSON}, nil); err != nil {
			t.Fatal(err)
		}
		var content orm.AcademicPaperContent
		if err := db.First(&content, "id=?", job.ResourceID).Error; err != nil || !usablePaperContent(content) {
			t.Fatal(content.Error, err)
		}
		var pages []ContentPage
		if err := json.Unmarshal(content.PagesJSON, &pages); err != nil || len(pages) != 2 || pages[0].Page != 1 || pages[1].Page != 2 {
			t.Fatal(pages, err)
		}
	}
	w = httptest.NewRecorder()
	LaunchPaperWorkflow(w, paperRequest(`{"kind":"review","skip_expansion":true,"dataset_id":"papers","document_ids":["a","b"],"paper_ids":["a","b"],"request_key":"parsed-review"}`))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestSelectedReviewRejectsDirectionAndForeignPapers(t *testing.T) {
	paperFixture(t)
	for _, body := range []string{
		`{"kind":"review","direction":"agents","document_ids":["a"],"request_key":"mixed"}`,
		`{"kind":"review","direction":"agents","skip_expansion":true,"request_key":"direction"}`,
		`{"kind":"review","expansion_id":"old","review_scope":"direct_references","request_key":"history"}`,
	} {
		w := httptest.NewRecorder()
		LaunchPaperWorkflow(w, paperRequest(body))
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	LaunchPaperWorkflow(w, paperRequest(`{"kind":"review","skip_expansion":true,"dataset_id":"papers","document_ids":["a"],"paper_ids":["a","b"],"request_key":"outside-selection"}`))
	if w.Code != 422 {
		t.Fatal(w.Code, w.Body.String())
	}
}
