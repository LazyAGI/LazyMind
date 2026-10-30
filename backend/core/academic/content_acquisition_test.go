package academic

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

func TestExpansionReusesImportedPDFContent(t *testing.T) {
	for _, version := range []string{"submittedVersion", ""} {
		t.Run("source_version="+version, func(t *testing.T) {
			db := paperFixture(t)
			t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
			path := filepath.Join(doc.UploadRoot(), "paper.pdf")
			if err := os.WriteFile(path, []byte("%PDF-local paper"), 0600); err != nil {
				t.Fatal(err)
			}
			paper := PaperRef{PaperID: "a", Title: "Imported candidate", PDFURL: "https://example.org/paper.pdf", SourceProvider: "openalex", SourceVersion: version}
			if err := db.Model(&orm.Document{}).Where("id=?", "a").Update("ext", marshal(map[string]string{"stored_path": path})).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.AcademicWorkDocument{AcademicWorkID: "a", DatasetID: "papers", DocumentID: "a", SourceProvider: paper.SourceProvider, SourceVersion: version, SourceLocator: paper.PDFURL}).Error; err != nil {
				t.Fatal(err)
			}
			seeds, err := seedSources(db.DB, "papers", []WorkflowPaper{{Paper: paper}})
			if err != nil {
				t.Fatal(err)
			}
			content := orm.AcademicPaperContent{ID: "imported-body", UserID: "u", PaperID: "a", ParserVersion: orm.AcademicPaperParserVersion,
				SourceKind: "kb", SourceDatasetID: "papers", SourceDocumentID: "a", SourceHash: seeds[0].Hash,
				SourceProvider: paper.SourceProvider, SourceVersion: version, SourceKey: seeds[0].key(), Status: "ready", ContentHash: "body",
				PagesJSON:   marshal([]ContentPage{{Page: 1, Text: "The original imported paper body."}}),
				QualityJSON: marshal(contentQuality{Eligible: true, Downloaded: true, Parsed: true, Identity: "matched", Material: "main", Text: "usable"})}
			record := orm.AcademicExpansion{ID: "imported-expansion", UserID: "u", DatasetID: "papers", Version: 1, Status: "completed",
				SeedSnapshotJSON: marshal([]paperSource{}), DiscoveryJSON: marshal([]paperDiscovery{{Papers: []PaperRef{paper}}}),
				ResultJSON: marshal(ReviewWorkflowInput{Papers: []WorkflowPaper{{Paper: paper}}})}
			for _, row := range []any{&content, &record} {
				if err = db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 2; i++ {
				w := httptest.NewRecorder()
				ReadPaperContent(w, paperRequest(string(marshal(map[string]string{"expansion_id": record.ID, "paper_id": "a"}))))
				var result struct {
					Status string
					Pages  []ContentPage
				}
				if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || result.Status != "ready" || len(result.Pages) != 1 {
					t.Fatalf("existing imported body was not readable: %d %s %v", w.Code, w.Body.String(), err)
				}
			}
			var jobs int64
			if err = db.Model(&orm.AsyncJob{}).Where("job_type=?", "academic.paper_content").Count(&jobs).Error; err != nil || jobs != 0 {
				t.Fatalf("existing PDF was queued again: %d %v", jobs, err)
			}
			if err = db.Model(&orm.AcademicDocumentState{}).Where("document_id=?", "a").Update("revision", 2).Error; err != nil {
				t.Fatal(err)
			}
			source, err := expansionSource(db.DB, record, "a")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = findSourceContent(db.DB, "u", source); err == nil {
				t.Fatal("content from an older document identity revision was reused")
			}
		})
	}
}

func TestCompletedExpansionQueuesAllRetainedPapersOnce(t *testing.T) {
	db := paperFixture(t)
	conversation, err := newAcademicConversation(db.DB, "u", "papers", "research", "expansion")
	if err != nil {
		t.Fatal(err)
	}
	papers := []WorkflowPaper{
		{Paper: PaperRef{PaperID: "a", Title: "Already read", SourceProvider: "openalex", SourceVersion: "publishedVersion"}},
		{Paper: PaperRef{PaperID: "b", Title: "Never read", PDFURL: "https://example.org/b.pdf"}},
	}
	record := orm.AcademicExpansion{ID: "completed-expansion", UserID: "u", DatasetID: "papers", Goal: "expansion", Status: "completed",
		Version: 1, ConversationID: conversation, SeedSnapshotJSON: marshal([]paperSource{}),
		DiscoveryJSON: marshal([]paperDiscovery{{Papers: []PaperRef{papers[0].Paper, papers[1].Paper}}}),
		ResultJSON:    marshal(ReviewWorkflowInput{UserInstruction: "research", Papers: papers})}
	source := paperSource{Paper: papers[0].Paper, DatasetID: "papers"}
	ready := orm.AcademicPaperContent{ID: "already-ready", UserID: "u", PaperID: "a", SourceKey: source.key(),
		SourceKind: "external", SourceProvider: source.Paper.SourceProvider, SourceVersion: source.Paper.SourceVersion,
		ParserVersion: orm.AcademicPaperParserVersion, Status: "ready", ContentHash: "hash", PagesJSON: marshal([]ContentPage{{Page: 1, Text: "body"}})}
	session := orm.WorkflowSession{ID: "completed-session", ConversationID: conversation, WorkflowID: "paper_expansion", CreateUserID: "u", Status: "completed"}
	for _, row := range []any{&record, &ready, &session} {
		if err = db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	job := asyncjob.Job{ResourceID: record.ID, CreateUserID: "u", PayloadJSON: marshal(expansionProjectionPayload{SessionID: session.ID})}
	for i := 0; i < 2; i++ {
		if _, err = projectAcademicResult(t.Context(), job, nil); err != nil {
			t.Fatal(err)
		}
	}
	var content orm.AcademicPaperContent
	if err = db.Where("paper_id=?", "b").Take(&content).Error; err != nil || content.Status != "processing" {
		t.Fatal("an unread retained paper was not queued", content.Status, err)
	}
	var jobs int64
	if err = db.Model(&orm.AsyncJob{}).Where("job_type=? AND resource_id=?", "academic.paper_content", content.ID).Count(&jobs).Error; err != nil || jobs != 1 {
		t.Fatalf("projection did not deduplicate acquisition: %d %v", jobs, err)
	}
	var handoffs int64
	db.Model(&orm.AcademicHandoff{}).Count(&handoffs)
	if handoffs != 0 {
		t.Fatal("ordinary expansion started a review")
	}
}

func TestFulltextFallsBackToDOIAndReusesDownloadedOriginal(t *testing.T) {
	db := paperFixture(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "content-fixture")
	title := "Reliable tool learning with language models"
	parsed := false
	parser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !parsed {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"pages": []ContentPage{{Page: 1, Text: title + "\nAbstract\n" + strings.Repeat("We evaluate tool learning with controlled experiments and report methods and limitations. ", 5)}}, "parser_version": orm.AcademicPaperParserVersion})
	}))
	defer parser.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", parser.URL)
	previous := http.DefaultTransport
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "api.openalex.org" {
			body := marshal(scholarlyWork{ID: "https://openalex.org/W123", DOI: "https://doi.org/10.1234/tools", Title: title,
				Best:      &scholarlyLocation{PDFURL: "https://primary.example/paper.pdf", Version: "publishedVersion"},
				Locations: []scholarlyLocation{{PDFURL: "https://backup.example/paper.pdf", Version: "publishedVersion"}}})
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
		}
		if r.URL.Host == strings.TrimPrefix(parser.URL, "http://") {
			return previous.RoundTrip(r)
		}
		return nil, errors.New("unexpected service request: " + r.URL.Host)
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	downloads := 0
	originals, err := doc.NewOriginalService(doc.OriginalServiceDeps{DB: db.DB, HTTPTransport: importErrorTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "primary.example" {
			return &http.Response{StatusCode: 503, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("unavailable")), Request: r}, nil
		}
		if r.URL.Host != "backup.example" {
			return nil, errors.New("unexpected PDF host: " + r.URL.Host)
		}
		downloads++
		body := "%PDF-1.7\nverified paper bytes\n%%EOF"
		return &http.Response{StatusCode: 200, ContentLength: int64(len(body)), Header: http.Header{"Content-Type": {"application/pdf"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	paper := PaperRef{PaperID: "a", Title: title, DOI: "10.1234/tools", SourceProvider: "openalex", SourceVersion: "publishedVersion", PDFURL: "https://primary.example/paper.pdf"}
	content := orm.AcademicPaperContent{ID: "fallback-content", UserID: "u", PaperID: "a", ParserVersion: orm.AcademicPaperParserVersion, SourceKey: "fallback-source", SourceKind: "external", Status: "processing", PagesJSON: marshal([]ContentPage{}), QualityJSON: marshal(map[string]any{})}
	if err = db.Create(&content).Error; err != nil {
		t.Fatal(err)
	}
	source := paperSource{Paper: paper, DatasetID: "papers"}
	if _, _, _, _, err = readExternalCopies(t.Context(), "u", source, content, originals); err == nil || errors.Is(err, errFulltextUnavailable) {
		t.Fatal("parser failure was hidden as missing PDF", err)
	}
	if err = db.Where("id=?", content.ID).Take(&content).Error; err != nil {
		t.Fatal(err)
	}
	acquisition := paperContentAcquisition(content.QualityJSON)
	if !acquisition.Downloaded || acquisition.FileRef == "" || acquisition.Candidate.URL != "https://backup.example/paper.pdf" {
		t.Fatalf("downloaded alternate was not retained for retry: %+v", acquisition)
	}
	acquisition.StartedAt = time.Now().Add(-time.Hour)
	if err = saveContentAcquisition(t.Context(), content, acquisition); err != nil {
		t.Fatal(err)
	}
	db.Where("id=?", content.ID).Take(&content)
	parsed = true
	pages, _, hash, version, err := readExternalCopies(t.Context(), "u", source, content, originals)
	if err != nil || len(pages) != 1 || hash == "" || version != "publishedVersion" || downloads != 1 {
		t.Fatalf("retry did not reuse the downloaded PDF: pages=%d version=%s downloads=%d error=%v", len(pages), version, downloads, err)
	}
}

func TestPreprintTemplateDOIDoesNotConflictWithPublishedIdentity(t *testing.T) {
	paper := PaperRef{Title: "Tool Learning with Large Language Models: A Survey", DOI: "10.1007/s11704-024-40678-2"}
	body := "Front. Comput. Sci.\nhttps://doi.org/10.1007/sxxxxx-yyy-zzzz-1\n" + paper.Title + "\nAbstract\n" + strings.Repeat("This survey studies tool learning methods and their evaluations with large language models. ", 4)
	if q := checkPaperQuality(paper, []ContentPage{{Page: 1, Text: body}}, "external", "hash"); !q.Eligible {
		t.Fatalf("unassigned template DOI rejected the matching preprint: %+v", q)
	}
	conflicting := strings.ReplaceAll(body, "sxxxxx-yyy-zzzz-1", "s11704-024-40000-0")
	if q := checkPaperQuality(paper, []ContentPage{{Page: 1, Text: conflicting}}, "external", "hash"); q.Eligible || q.Identity != "mismatch" {
		t.Fatalf("a real conflicting DOI was accepted: %+v", q)
	}
}

func TestRetryContentRequeuesCompletedQualityCheck(t *testing.T) {
	db := paperFixture(t)
	paper := PaperRef{PaperID: "a", Title: "A matching paper"}
	seeds, err := seedSources(db.DB, "papers", []WorkflowPaper{{Paper: paper}})
	if err != nil {
		t.Fatal(err)
	}
	acquisition := contentAcquisition{StartedAt: time.Now(), Downloaded: true, FileRef: "cached-original"}
	content := orm.AcademicPaperContent{ID: "checked-content", UserID: "u", PaperID: "a", SourceKind: "kb",
		SourceDatasetID: "papers", SourceDocumentID: "a", SourceKey: seeds[0].key(), ParserVersion: orm.AcademicPaperParserVersion,
		Status: "needs_review", PagesJSON: marshal([]ContentPage{{Page: 1, Text: "body"}}),
		QualityJSON: marshal(contentQuality{Downloaded: true, Parsed: true, Acquisition: acquisition})}
	record := orm.AcademicExpansion{ID: "retry-expansion", UserID: "u", DatasetID: "papers", Version: 1,
		SeedSnapshotJSON: marshal(seeds), DiscoveryJSON: marshal([]paperDiscovery{}), ResultJSON: marshal(ReviewWorkflowInput{Papers: []WorkflowPaper{{Paper: paper}}})}
	for _, row := range []any{&content, &record} {
		if err = db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	job, err := asyncjob.Enqueue(t.Context(), db.DB, asyncjob.EnqueueRequest{JobType: "academic.paper_content", ResourceType: "academic_content",
		ResourceID: content.ID, CreateUserID: "u", IdempotencyKey: content.ID, Payload: map[string]any{"source": seeds[0]}, MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Model(job).Update("status", "succeeded").Error; err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	RetryPaperContent(w, paperRequest(string(marshal(map[string]string{"expansion_id": record.ID, "paper_id": "a"}))))
	var pending int64
	db.Model(&orm.AsyncJob{}).Where("job_type=? AND resource_id=? AND status=?", "academic.paper_content", content.ID, "pending").Count(&pending)
	if w.Code != 200 || pending != 1 {
		t.Fatalf("quality retry did not create runnable work: pending=%d response=%d %s", pending, w.Code, w.Body.String())
	}
	db.Where("id=?", content.ID).Take(&content)
	if !paperContentAcquisition(content.QualityJSON).Downloaded || paperContentAcquisition(content.QualityJSON).FileRef != acquisition.FileRef {
		t.Fatal("retry discarded the downloaded original")
	}
}
