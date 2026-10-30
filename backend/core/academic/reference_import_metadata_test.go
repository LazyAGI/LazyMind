package academic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/academic/arxivsource"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/paperlibrary"
)

const houlsbyTitle = "Parameter-Efficient Transfer Learning for NLP"
const houlsbyCitation = "Neil Houlsby, Andrei Giurgiu, Stanislaw Jastrzebski, Bruna Morrone, Quentin de Laroussilhe, Andrea Gesmundo, Mona Attariyan, and Sylvain Gelly. Parameter-Efficient Transfer Learning for NLP. arXiv:1902.00751 [cs, stat], June 2019. URL http://arxiv.org/abs/1902. 00751."

var houlsbyAuthors = []string{"Neil Houlsby", "Andrei Giurgiu", "Stanislaw Jastrzebski", "Bruna Morrone", "Quentin de Laroussilhe", "Andrea Gesmundo", "Mona Attariyan", "Sylvain Gelly"}

func houlsbyPaper() PaperRef {
	return PaperRef{PaperID: "work", Title: houlsbyTitle, Authors: houlsbyAuthors, Year: 2019, ArxivID: "1902.00751", SourceProvider: "arxiv", SourceVersion: "v1", PDFURL: "https://arxiv.org/pdf/1902.00751v1"}
}
func houlsbyAtom(base, version string) string {
	authors := ""
	for _, author := range houlsbyAuthors {
		authors += "<author><name>" + author + "</name></author>"
	}
	return `<feed xmlns="http://www.w3.org/2005/Atom"><entry><id>https://arxiv.org/abs/` + base + version + `</id><title>` + houlsbyTitle + `</title><summary>Verified abstract for reference import experiment.</summary><published>2019-02-02T00:00:00Z</published>` + authors + `</entry></feed>`
}
func seedReferencePDFCache(t *testing.T, db *gorm.DB, ref PaperRef) {
	t.Helper()
	guard, err := lockArxivImport(t.Context(), db, ref.ArxivID)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.release()
	_, _, err = acquireCachedPDFLocked(guard.ctx, "alice", ref, func(_ context.Context, _ FulltextCandidate) (string, int64, error) {
		p := filepath.Join(t.TempDir(), "download.pdf")
		b := []byte("%PDF-1.7\nreference experiment PDF bytes\n%%EOF")
		return p, int64(len(b)), os.WriteFile(p, b, 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
}
func prepareReferenceLibrary(t *testing.T, db *gorm.DB, dataset string) {
	t.Helper()
	if err := db.AutoMigrate(&orm.PaperLibraryEntry{}, &orm.PaperLibraryFolder{}, &orm.PaperLibraryAttachment{}, &orm.PaperLibraryNote{}, &orm.AcademicUserPaperState{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.Dataset{}).Where("id=?", dataset).Update("scene", "academic_papers").Error; err != nil {
		t.Fatal(err)
	}
}
func checkHoulsbyMetadata(t *testing.T, m paperlibrary.Metadata) {
	t.Helper()
	if m.Title != houlsbyTitle || m.Year != 2019 || !reflect.DeepEqual(m.Authors, houlsbyAuthors) {
		t.Fatalf("visible metadata incorrect: title=%q year=%d authors=%v", m.Title, m.Year, m.Authors)
	}
}

func TestReferenceMetadataCitationParsing(t *testing.T) {
	for _, tc := range []struct {
		name, raw, title string
		year             int
	}{
		{"screenshot", houlsbyCitation, houlsbyTitle, 2019},
		{"arxiv_before_2020_year", "I. Beltagy, M. Peters, and A. Cohan. Longformer: The Long-Document Transformer. arXiv:2004.05150, 2020.", "Longformer: The Long-Document Transformer", 2020},
		{"arxiv_before_2021_year", "P. Lewis. Retrieval-Augmented Generation for Knowledge-Intensive NLP Tasks. arXiv:2005.11401, 2020.", "Retrieval-Augmented Generation for Knowledge-Intensive NLP Tasks", 2020},
		{"arxiv_only_has_no_publication_year", "N. Houlsby. " + houlsbyTitle + ". arXiv:1902.00751.", houlsbyTitle, 0},
		{"doi_before_year", "A. Author. A sufficiently long paper title. doi:10.1145/2019.1234, 2024.", "A sufficiently long paper title", 2024},
		{"url_before_year", "A. Author. A sufficiently long paper title. URL https://example.org/2020/paper, 2024.", "A sufficiently long paper title", 2024},
		{"year_first", "N. Houlsby. 2019. " + houlsbyTitle + ". In ICML.", houlsbyTitle, 2019},
		{"year_parenthesized", "Houlsby, N. (2019). " + houlsbyTitle + ".", houlsbyTitle, 2019},
		{"year_suffix", "A. Author. 2024a. A sufficiently long paper title.", "A sufficiently long paper title", 2024},
		{"ieee_quoted", "N. Houlsby et al., \"" + houlsbyTitle + ",\" in Proceedings of ICML, 2019.", houlsbyTitle, 2019},
		{"doi_only_no_date", "A. Author. A sufficiently long paper title. doi:10.1145/2019.1234.", "A sufficiently long paper title", 0},
		{"url_only_no_date", "A. Author. A sufficiently long paper title. URL https://example.org/2020/paper.", "A sufficiently long paper title", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if y := extractYear(tc.raw); y != tc.year {
				t.Errorf("year=%d want=%d", y, tc.year)
			}
			if title := citationTitle(tc.raw); title != tc.title {
				t.Errorf("title=%q want=%q", title, tc.title)
			}
		})
	}
}

func TestReferenceMetadataExplicitArxivReferenceResolution(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		base      string
		wantError bool
	}{
		{"success", 200, "1902.00751", false},
		{"provider_unavailable", 503, "1902.00751", true},
		{"provider_wrong_identity", 200, "2404.06654", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, _, _, _ := importPipelineFixture(t)
			old := http.DefaultTransport
			lookups := 0
			http.DefaultTransport = sourceTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Hostname() != "export.arxiv.org" {
					return old.RoundTrip(r)
				}
				lookups++
				t.Logf("arxiv query: %s", r.URL.RawQuery)
				return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(houlsbyAtom(tc.base, "v1"))), Request: r}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = old })
			ref := orm.AcademicReference{ID: "screenshot", RawText: houlsbyCitation, ArxivIDBase: "1902.00751", PublicationYear: extractYear(houlsbyCitation), AuthorsJSON: marshal([]string{}), BBoxJSON: marshal([]string{}), SegmentIDsJSON: marshal([]string{})}
			if e := db.Create(&ref).Error; e != nil {
				t.Fatal(e)
			}
			work, _, _, err := resolveReferenceWithExternal(t.Context(), db, &ref, true)
			if tc.wantError {
				if err == nil {
					t.Fatalf("provider failure or wrong identity accepted: lookups=%d title=%q", lookups, work.CanonicalTitle)
				}
				var updated orm.AcademicReference
				db.Where("id=?", ref.ID).Take(&updated)
				if updated.ResolvedWorkID != "" {
					t.Fatal("failed lookup created resolved reference")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			checkHoulsbyMetadata(t, paperlibrary.WorkMetadata(work))
			if lookups != 1 {
				t.Fatalf("expected direct identifier lookup, got %d", lookups)
			}
		})
	}
}

func TestReferenceMetadataVerifiedSnapshotVisibleAfterImport(t *testing.T) {
	for _, mode := range []string{"fresh", "existing_pdf", "resume_committed", "manual_library_metadata"} {
		t.Run(mode, func(t *testing.T) {
			db, b, item, svc := importPipelineFixture(t)
			prepareReferenceLibrary(t, db, b.TargetDatasetID)
			ref := houlsbyPaper()
			b.EntryType = "single_reference"
			if e := db.Save(&b).Error; e != nil {
				t.Fatal(e)
			}
			if e := db.Model(&orm.AcademicWork{}).Where("id=?", "work").Updates(map[string]any{"canonical_title": houlsbyCitation, "publication_year": 1902, "authors_json": marshal([]string{}), "arxiv_id_base": ref.ArxivID}).Error; e != nil {
				t.Fatal(e)
			}
			item.SelectedCandidateJSON = marshal(savedImportCandidate{FulltextCandidate: FulltextCandidate{URL: ref.PDFURL, SourceProvider: "arxiv", Version: "v1", SourceType: "official_pdf", ExpectedMIME: "application/pdf"}, VerifiedPaper: &ref})
			if e := db.Save(&item).Error; e != nil {
				t.Fatal(e)
			}
			if mode == "existing_pdf" || mode == "resume_committed" {
				d := orm.Document{ID: "existing", DatasetID: b.TargetDatasetID, DisplayName: "already stored.pdf", BaseModel: orm.BaseModel{CreateUserID: "alice"}}
				task := orm.Task{ID: "existing-task", DatasetID: b.TargetDatasetID, DocID: d.ID}
				link := orm.AcademicWorkDocument{AcademicWorkID: "work", DatasetID: b.TargetDatasetID, DocumentID: d.ID, SourceProvider: "arxiv", SourceVersion: "v1"}
				for _, v := range []any{&d, &task, &link} {
					if e := db.Create(v).Error; e != nil {
						t.Fatal(e)
					}
				}
				if mode == "resume_committed" {
					var w orm.AcademicWork
					db.Where("id=?", "work").Take(&w)
					if _, e := registerRecognizedLibraryDocument(t.Context(), db, d, w, true, "", item.ID, "references"); e != nil {
						t.Fatal(e)
					}
					item.DocumentID, item.DocumentTaskID = d.ID, task.ID
					if e := db.Save(&item).Error; e != nil {
						t.Fatal(e)
					}
				}
			}
			if mode == "manual_library_metadata" {
				e := orm.PaperLibraryEntry{ID: "work-work", DatasetID: b.TargetDatasetID, AcademicWorkID: "work", MetadataJSON: marshal(map[string]any{"title": "My edited title", "authors": []string{"My author"}, "year": 2022}), TagsJSON: marshal([]string{}), Revision: 4}
				if err := db.Create(&e).Error; err != nil {
					t.Fatal(err)
				}
			}
			seedReferencePDFCache(t, db, ref)
			if err := processImportItem(t.Context(), asyncjob.Job{ID: "job", AttemptCount: 1}, b, item, sourceImportCaller(b), svc); err != nil {
				t.Fatal(err)
			}
			view, err := paperlibrary.Load(t.Context(), db, b.TargetDatasetID, "alice", nil, false)
			if err != nil || len(view.Data.Papers) != 1 {
				t.Fatalf("view=%+v err=%v", view, err)
			}
			p := view.Data.Papers[0]
			if mode == "manual_library_metadata" {
				if p.Metadata.Title != "My edited title" || p.Metadata.Year != 2022 || !reflect.DeepEqual(p.Metadata.Authors, []string{"My author"}) {
					t.Fatalf("manual metadata lost: %+v", p.Metadata)
				}
			} else {
				checkHoulsbyMetadata(t, p.Metadata)
				if p.Incomplete {
					t.Fatal("complete paper still marked incomplete")
				}
			}
			var documents int64
			if err := db.Model(&orm.Document{}).Count(&documents).Error; err != nil || documents != 1 {
				t.Fatalf("documents=%d err=%v", documents, err)
			}
		})
	}
}

func TestReferenceMetadataPartialSnapshotDoesNotEraseAuthors(t *testing.T) {
	db, b, item, svc := importPipelineFixture(t)
	prepareReferenceLibrary(t, db, b.TargetDatasetID)
	ref := houlsbyPaper()
	ref.Authors = nil
	if e := db.Model(&orm.AcademicWork{}).Where("id=?", "work").Updates(map[string]any{"canonical_title": houlsbyTitle, "publication_year": 2019, "authors_json": marshal(houlsbyAuthors), "arxiv_id_base": ref.ArxivID}).Error; e != nil {
		t.Fatal(e)
	}
	item.SelectedCandidateJSON = marshal(savedImportCandidate{FulltextCandidate: FulltextCandidate{URL: ref.PDFURL, SourceProvider: "arxiv", Version: "v1", SourceType: "official_pdf"}, VerifiedPaper: &ref})
	if e := db.Save(&item).Error; e != nil {
		t.Fatal(e)
	}
	seedReferencePDFCache(t, db, ref)
	if err := processImportItem(t.Context(), asyncjob.Job{ID: "job", AttemptCount: 1}, b, item, sourceImportCaller(b), svc); err != nil {
		t.Fatal(err)
	}
	view, err := paperlibrary.Load(t.Context(), db, b.TargetDatasetID, "alice", nil, false)
	if err != nil || len(view.Data.Papers) != 1 {
		t.Fatal(err)
	}
	checkHoulsbyMetadata(t, view.Data.Papers[0].Metadata)
}

func TestReferenceMetadataPreviewImportLibraryRoundTrip(t *testing.T) {
	db := paperFixture(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	old := http.DefaultTransport
	http.DefaultTransport = sourceTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() == "export.arxiv.org" {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(houlsbyAtom("1902.00751", "v1"))), Request: r}, nil
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"items":[{"dataset_id":"papers","exists":false,"allowed":true}],"source_map":{},"code":200,"data":[]}`)), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")
	items := splitReferences("[1] " + houlsbyCitation)
	if len(items) != 1 {
		t.Fatalf("extraction: %+v", items)
	}
	parsed := items[0]
	ref := orm.AcademicReference{ID: "screenshot", SourceDocumentID: "a", RawText: parsed.Raw, ArxivIDBase: parsed.Arxiv, DOINormalized: parsed.DOI, PublicationYear: parsed.Year, AuthorsJSON: marshal([]string{}), BBoxJSON: marshal([]string{}), SegmentIDsJSON: marshal([]string{})}
	if err := db.Create(&ref).Error; err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	PreviewImport(w, paperRequest(`{"reference_ids":["screenshot"],"target_dataset_id":"papers","resolve_external":true}`))
	var preview struct {
		Token string        `json:"preview_token"`
		Items []PreviewItem `json:"items"`
	}
	if json.Unmarshal(w.Body.Bytes(), &preview) != nil || w.Code != 200 || len(preview.Items) != 1 {
		t.Fatalf("preview %d %s", w.Code, w.Body.String())
	}
	selected := preview.Items[0]
	checkHoulsbyMetadata(t, paperlibrary.Metadata{Title: selected.Paper.Title, Authors: selected.Paper.Authors, Year: selected.Paper.Year})
	request := createImportRequest{EntryType: "single_reference", PreviewToken: preview.Token, TargetDatasetID: "papers", IdempotencyKey: "experiment", Items: []selectedImportItem{{WorkID: selected.WorkID, ReferenceIDs: []string{"screenshot"}, Candidate: selected.Candidates[0]}}}
	w = httptest.NewRecorder()
	CreateImport(w, paperRequest(string(marshal(request))))
	if w.Code != 200 {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var saved orm.PaperImportItem
	if err := db.Where("academic_work_id=?", selected.WorkID).Take(&saved).Error; err != nil {
		t.Fatal(err)
	}
	var b orm.PaperImportBatch
	if err := db.Where("id=?", saved.BatchID).Take(&b).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.Dataset{}).Where("id=?", "papers").Update("processing_level", "stored").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.AsyncJob{}).Where("id=?", b.AsyncJobID).Updates(map[string]any{"status": "running", "attempt_count": 1}).Error; err != nil {
		t.Fatal(err)
	}
	b.Status = "running"
	if err := db.Save(&b).Error; err != nil {
		t.Fatal(err)
	}
	seedReferencePDFCache(t, db.DB, selected.Paper)
	svc, err := doc.NewOriginalService(doc.OriginalServiceDeps{DB: db.DB, SubmitProcessing: func(context.Context, doc.DatasetCatalogCaller, string, string) (doc.StartTaskResult, error) {
		return doc.StartTaskResult{Status: "STARTED", SubmitStatus: "ACCEPTED"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err = processImportItem(t.Context(), asyncjob.Job{ID: b.AsyncJobID, AttemptCount: 1}, b, saved, sourceImportCaller(b), svc); err != nil {
		t.Fatal(err)
	}
	view, err := paperlibrary.Load(t.Context(), db.DB, "papers", "u", nil, false)
	if err != nil || len(view.Data.Papers) != 1 {
		t.Fatalf("view=%+v err=%v", view, err)
	}
	checkHoulsbyMetadata(t, view.Data.Papers[0].Metadata)
}

func TestReferenceMetadataLiveArxivLookup(t *testing.T) {
	if os.Getenv("LAZYMIND_TEST_REFERENCE_LIVE") != "1" {
		t.Skip("set LAZYMIND_TEST_REFERENCE_LIVE=1 for real arXiv integration")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
	defer cancel()
	m, err := arxivsource.NewClient().Lookup(ctx, "1902.00751")
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != houlsbyTitle || m.Year != 2019 || len(m.Authors) != 8 {
		t.Fatalf("unexpected live metadata: %+v", m)
	}
	t.Logf("live metadata title=%q authors=%d year=%d version=%s", m.Title, len(m.Authors), m.Year, m.Version)
}

func TestReferenceMetadataOpenAlexAuthorsArePreserved(t *testing.T) {
	db, _, _, _ := importPipelineFixture(t)
	old := http.DefaultTransport
	http.DefaultTransport = sourceTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"results":[]}`
		if r.URL.Hostname() == "api.openalex.org" {
			body = `{"results":[{"id":"https://openalex.org/W123","title":"A sufficiently long paper title","publication_year":2024,"doi":"https://doi.org/10.1234/paper","ids":{},"authorships":[{"author":{"display_name":"Alice Example"}},{"author":{"display_name":"Bob Example"}}],"best_oa_location":{"pdf_url":"https://publisher.example/paper.pdf","version":"publishedVersion"}}]}`
			t.Logf("OpenAlex requested fields: %s", r.URL.Query().Get("select"))
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	ref := orm.AcademicReference{ID: "oa-authors", RawText: "A. Example. 2024. A sufficiently long paper title.", AuthorsJSON: marshal([]string{}), BBoxJSON: marshal([]string{}), SegmentIDsJSON: marshal([]string{})}
	if err := db.Create(&ref).Error; err != nil {
		t.Fatal(err)
	}
	work, _, _, err := resolveReferenceWithExternal(t.Context(), db, &ref, true)
	if err != nil {
		t.Fatal(err)
	}
	got := paperlibrary.WorkMetadata(work)
	if !reflect.DeepEqual(got.Authors, []string{"Alice Example", "Bob Example"}) {
		t.Fatalf("OpenAlex dropped authors: %+v", got)
	}
}

func TestReferenceMetadataReferenceExplicitVersionSurvivesPreview(t *testing.T) {
	referenceVersionPreview(t, false)
}
func TestReferenceMetadataResolvedReferenceExplicitVersionSurvivesPreview(t *testing.T) {
	referenceVersionPreview(t, true)
}
func referenceVersionPreview(t *testing.T, resolved bool) {
	t.Helper()
	db := paperFixture(t)
	old := http.DefaultTransport
	queries := []string{}
	http.DefaultTransport = sourceTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"items":[{"dataset_id":"papers","exists":false,"allowed":true}],"source_map":{}}`
		if r.URL.Hostname() == "export.arxiv.org" {
			id := r.URL.Query().Get("id_list")
			queries = append(queries, id)
			version := "v2"
			if strings.HasSuffix(id, "v1") {
				version = "v1"
			}
			body = houlsbyAtom("1902.00751", version)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	ref := orm.AcademicReference{ID: "cited-v1", SourceDocumentID: "a", RawText: strings.ReplaceAll(houlsbyCitation, "1902.00751", "1902.00751v1"), ArxivIDBase: "1902.00751", PublicationYear: 2019, AuthorsJSON: marshal([]string{}), BBoxJSON: marshal([]string{}), SegmentIDsJSON: marshal([]string{})}
	if resolved {
		work, _, err := findOrCreateWork(t.Context(), db.DB, WorkInput{Title: houlsbyCitation, Year: 1902, ArxivID: "1902.00751"})
		if err != nil {
			t.Fatal(err)
		}
		ref.ResolvedWorkID = work.ID
	}
	if err := db.Create(&ref).Error; err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	PreviewImport(w, paperRequest(`{"reference_ids":["cited-v1"],"target_dataset_id":"papers","resolve_external":true}`))
	var preview struct {
		Items []PreviewItem `json:"items"`
	}
	if json.Unmarshal(w.Body.Bytes(), &preview) != nil || w.Code != 200 || len(preview.Items) != 1 {
		t.Fatalf("preview %d %s", w.Code, w.Body.String())
	}
	if preview.Items[0].Paper.SourceVersion != "v1" {
		t.Fatalf("cited v1 changed to %q; lookups=%v", preview.Items[0].Paper.SourceVersion, queries)
	}
}

func TestReferenceMetadataLiveReferencePDFAndLibraryRoundTrip(t *testing.T) {
	if os.Getenv("LAZYMIND_TEST_REFERENCE_LIVE") != "1" {
		t.Skip("set LAZYMIND_TEST_REFERENCE_LIVE=1 for real arXiv integration")
	}
	network := http.DefaultTransport
	db, b, item, svc := importPipelineFixture(t)
	prepareReferenceLibrary(t, db, b.TargetDatasetID)
	scan := http.DefaultTransport
	http.DefaultTransport = sourceTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() == "scan.test" {
			return scan.RoundTrip(r)
		}
		return network.RoundTrip(r)
	})
	b.EntryType = "single_reference"
	if err := db.Save(&b).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.AcademicWork{}).Where("id=?", "work").Updates(map[string]any{"canonical_title": houlsbyCitation, "publication_year": 1902, "authors_json": marshal([]string{}), "arxiv_id_base": "1902.00751"}).Error; err != nil {
		t.Fatal(err)
	}
	item.SelectedCandidateJSON = marshal(FulltextCandidate{URL: "https://arxiv.org/pdf/1902.00751", SourceProvider: "arxiv", SourceType: "official_pdf", ExpectedMIME: "application/pdf"})
	if err := db.Save(&item).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 110*time.Second)
	defer cancel()
	if err := processImportItem(ctx, asyncjob.Job{ID: "job", AttemptCount: 1}, b, item, sourceImportCaller(b), svc); err != nil {
		t.Fatal(err)
	}
	view, err := paperlibrary.Load(ctx, db, b.TargetDatasetID, "alice", nil, false)
	if err != nil || len(view.Data.Papers) != 1 || len(view.Data.Attachments) != 1 {
		t.Fatalf("view=%+v err=%v", view, err)
	}
	p := view.Data.Papers[0]
	if p.Metadata.Title != houlsbyTitle || p.Metadata.Year != 2019 || len(p.Metadata.Authors) != 8 || p.Incomplete {
		t.Fatalf("live metadata incorrect: %+v", p)
	}
	a := view.Data.Attachments[0]
	if len(a.ContentHash) != 64 || a.SourceVersion == "" {
		t.Fatalf("PDF has no verified hash/version: %+v", a)
	}
	t.Logf("real official metadata and real PDF imported: title=%q year=%d authors=%d version=%s sha256=%s", p.Metadata.Title, p.Metadata.Year, len(p.Metadata.Authors), a.SourceVersion, a.ContentHash)
}

func TestReferenceMetadataQuotedPhraseWithinTitle(t *testing.T) {
	title := `On the "Attention Is All You Need" paradigm`
	raw := `A. Author. ` + title + `. 2024.`
	if got := citationTitle(raw); got != title {
		t.Fatalf("title=%q want=%q", got, title)
	}
}

func TestReferenceMetadataPreviewReusesLookupsAndRejectsConflictingVersions(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "duplicate_references", true: "conflicting_versions"}[conflict], func(t *testing.T) {
			db := paperFixture(t)
			old := http.DefaultTransport
			requests := 0
			http.DefaultTransport = sourceTransport(func(r *http.Request) (*http.Response, error) {
				body := `{"items":[],"source_map":{}}`
				if r.URL.Hostname() == "export.arxiv.org" {
					requests++
					version := "v1"
					if strings.HasSuffix(r.URL.Query().Get("id_list"), "v2") {
						version = "v2"
					}
					body = houlsbyAtom("1902.00751", version)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = old })
			for index, id := range []string{"one", "two"} {
				raw := houlsbyCitation
				if conflict {
					raw = strings.ReplaceAll(raw, "1902.00751", "1902.00751v"+[]string{"1", "2"}[index])
				}
				ref := orm.AcademicReference{ID: id, SourceDocumentID: "a", RawText: raw, ArxivIDBase: "1902.00751", AuthorsJSON: marshal([]string{}), BBoxJSON: marshal([]string{}), SegmentIDsJSON: marshal([]string{})}
				if err := db.Create(&ref).Error; err != nil {
					t.Fatal(err)
				}
			}
			w := httptest.NewRecorder()
			PreviewImport(w, paperRequest(`{"reference_ids":["one","two"],"target_dataset_id":"papers","resolve_external":true}`))
			if conflict {
				if w.Code != 422 || !strings.Contains(w.Body.String(), "REFERENCE_VERSION_CONFLICT") {
					t.Fatalf("conflicting versions accepted: %d %s", w.Code, w.Body.String())
				}
			} else {
				var response struct {
					Items []PreviewItem `json:"items"`
				}
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Items) != 1 || len(response.Items[0].ReferenceIDs) != 2 || requests != 1 {
					t.Fatalf("lookup or identity duplicated: requests=%d %d %s", requests, w.Code, w.Body.String())
				}
			}
		})
	}
}

func TestReferenceMetadataWrappedYearsAreNotReferenceNumbers(t *testing.T) {
	for _, bracketed := range []bool{true, false} {
		first, second := "[1] ", "[2] "
		if !bracketed {
			first, second = "1. ", "2. "
		}
		raw := first + "Xiang Lisa Li and Percy Liang. Prefix-Tuning: Optimizing Continuous Prompts for Generation.\n2021. arXiv:2101.00190.\n" + second + houlsbyCitation
		refs := splitReferences(raw)
		if len(refs) != 2 || refs[0].Year != 2021 || refs[0].Arxiv != "2101.00190" || refs[1].Year != 2019 || refs[1].Arxiv != "1902.00751" {
			t.Fatalf("wrapped date split: %+v", refs)
		}
	}
}
