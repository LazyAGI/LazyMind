package academic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

func TestUnifiedImportPreservesOtherProvidersAndVersions(t *testing.T) {
	for _, provider := range []string{"openalex", "unpaywall", "sciverse", "web"} {
		t.Run(provider, func(t *testing.T) {
			db, batch, item, _ := importPipelineFixture(t)
			if err := db.Model(&orm.AcademicWork{}).Where("id=?", "work").Update("arxiv_id_base", "").Error; err != nil {
				t.Fatal(err)
			}
			downloads := 0
			svc, err := doc.NewOriginalService(doc.OriginalServiceDeps{DB: db, HTTPTransport: sourceTransport(func(r *http.Request) (*http.Response, error) {
				downloads++
				if r.URL.Host != "publisher.example" {
					t.Fatalf("unexpected source: %s", r.URL.Host)
				}
				return &http.Response{StatusCode: 200, ContentLength: -1, Header: http.Header{"Content-Type": {"application/pdf"}}, Body: io.NopCloser(strings.NewReader("%PDF-1.7\nidentical verified PDF bytes\n%%EOF")), Request: r}, nil
			}), SubmitProcessing: func(context.Context, doc.DatasetCatalogCaller, string, string) (doc.StartTaskResult, error) {
				return doc.StartTaskResult{Status: "STARTED", SubmitStatus: "ACCEPTED"}, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			for index, version := range []string{"submittedVersion", "publishedVersion", "publishedVersion"} {
				ref := PaperRef{PaperID: "work", Title: "Verified paper", Authors: []string{"Author"}, Year: 2025, DOI: "10.1234/paper", SourceProvider: provider, SourceVersion: version, PDFURL: "https://publisher.example/paper.pdf"}
				selected := savedImportCandidate{FulltextCandidate: FulltextCandidate{URL: ref.PDFURL, SourceProvider: provider, Version: version, SourceType: "open_access_pdf", License: "cc-by", ExpectedMIME: "application/pdf"}, VerifiedPaper: &ref}
				item.SelectedCandidateJSON = marshal(selected)
				item.DocumentID, item.DocumentTaskID = "", ""
				if index > 0 {
					item.ID = fmt.Sprintf("item-%d", index)
					if err = db.Create(&item).Error; err != nil {
						t.Fatal(err)
					}
				} else if err = db.Model(&item).Update("selected_candidate_json", item.SelectedCandidateJSON).Error; err != nil {
					t.Fatal(err)
				}
				if err = processImportItem(t.Context(), asyncjob.Job{ID: "job", AttemptCount: 1}, batch, item, sourceImportCaller(batch), svc); err != nil {
					t.Fatal(err)
				}
				var receipt orm.PaperImportItem
				if err = db.Where("id=?", item.ID).Take(&receipt).Error; err != nil {
					t.Fatal(err)
				}
				var link orm.AcademicWorkDocument
				if err = db.Where("document_id=?", receipt.DocumentID).Take(&link).Error; err != nil {
					t.Fatal(err)
				}
				if link.SourceProvider != provider || link.SourceVersion != version {
					t.Fatalf("source lost: %+v", link)
				}
				var state orm.AcademicDocumentState
				if err = db.Where("document_id=?", receipt.DocumentID).Take(&state).Error; err != nil {
					t.Fatal(err)
				}
				var metadata WorkInput
				if err = json.Unmarshal(state.MetadataJSON, &metadata); err != nil || metadata.Title != ref.Title || metadata.DOI != ref.DOI {
					t.Fatalf("snapshot lost: %+v %v", metadata, err)
				}
				if index == 2 && receipt.Status != "skipped" {
					t.Fatalf("same version duplicated: %+v", receipt)
				}
			}
			var documents int64
			if err = db.Model(&orm.Document{}).Count(&documents).Error; err != nil || documents != 2 || downloads != 2 {
				t.Fatalf("documents=%d downloads=%d error=%v", documents, downloads, err)
			}
		})
	}
}

func TestOpenAlexHostedImportInjectsCredentialOnlyAtDownload(t *testing.T) {
	db, batch, item, _ := importPipelineFixture(t)
	if err := db.AutoMigrate(&orm.UserModelProvider{}, &orm.UserModelProviderGroup{}); err != nil {
		t.Fatal(err)
	}
	base := orm.BaseModel{CreateUserID: "alice"}
	for _, row := range []any{&orm.UserModelProvider{ID: "oa", Name: "OpenAlex", Category: "datasource", BaseModel: base}, &orm.UserModelProviderGroup{ID: "oa-group", UserModelProviderID: "oa", APIKey: "private-fixture-key", IsVerified: true, BaseModel: base}} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	ref := PaperRef{PaperID: "work", Title: "Verified paper", SourceProvider: "openalex", PDFURL: "https://content.openalex.org/works/W123.pdf"}
	item.SelectedCandidateJSON = marshal(savedImportCandidate{FulltextCandidate: FulltextCandidate{URL: ref.PDFURL, SourceProvider: "openalex", SourceType: "openalex_hosted", ExpectedMIME: "application/pdf"}, VerifiedPaper: &ref})
	svc, err := doc.NewOriginalService(doc.OriginalServiceDeps{DB: db, HTTPTransport: sourceTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("api_key") != "private-fixture-key" {
			t.Fatal("hosted download lost credential")
		}
		return &http.Response{StatusCode: 200, ContentLength: -1, Header: http.Header{"Content-Type": {"application/pdf"}}, Body: io.NopCloser(strings.NewReader("%PDF-1.7\nverified PDF bytes\n%%EOF")), Request: r}, nil
	}), SubmitProcessing: func(context.Context, doc.DatasetCatalogCaller, string, string) (doc.StartTaskResult, error) {
		return doc.StartTaskResult{Status: "STARTED"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err = processImportItem(t.Context(), asyncjob.Job{ID: "job", AttemptCount: 1}, batch, item, sourceImportCaller(batch), svc); err != nil {
		t.Fatal(err)
	}
	var links []orm.AcademicWorkDocument
	if err = db.Find(&links).Error; err != nil || len(links) != 1 || strings.Contains(links[0].SourceLocator, "private-fixture-key") {
		t.Fatal("credential persisted", err)
	}
}

func TestOtherProviderSnapshotRejectsWrongPaperBeforeDownload(t *testing.T) {
	db, batch, item, _ := importPipelineFixture(t)
	ref := PaperRef{PaperID: "another-work", Title: "Unrelated paper", SourceProvider: "openalex", SourceVersion: "publishedVersion", PDFURL: "https://publisher.example/paper.pdf"}
	item.SelectedCandidateJSON = marshal(savedImportCandidate{FulltextCandidate: FulltextCandidate{URL: ref.PDFURL, SourceProvider: ref.SourceProvider, Version: ref.SourceVersion}, VerifiedPaper: &ref})
	downloads := 0
	svc, err := doc.NewOriginalService(doc.OriginalServiceDeps{DB: db, HTTPTransport: sourceTransport(func(r *http.Request) (*http.Response, error) { downloads++; return nil, context.Canceled })})
	if err != nil {
		t.Fatal(err)
	}
	if err = processImportItem(t.Context(), asyncjob.Job{ID: "job", AttemptCount: 1}, batch, item, sourceImportCaller(batch), svc); err == nil || downloads != 0 {
		t.Fatal("foreign snapshot reached download", err, downloads)
	}
	var count int64
	if err = db.Model(&orm.Document{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("foreign paper was saved", count, err)
	}
}

func TestSourceInspectionAndActiveImportsKeepProviderVersions(t *testing.T) {
	db, _, item, _ := importPipelineFixture(t)
	inspected, err := inspectRecord(t.Context(), db, sourceRecord{URL: "https://arxiv.org/pdf/2404.06654v2.pdf"})
	if err != nil || inspected.Candidate.Version != "v2" || inspected.Candidate.URL != "https://arxiv.org/pdf/2404.06654v2" {
		t.Fatalf("explicit source revision lost: %+v %v", inspected, err)
	}
	item.SelectedCandidateJSON = marshal(FulltextCandidate{SourceProvider: "openalex", Version: "publishedVersion", URL: "https://publisher.example/paper.pdf"})
	if err = db.Model(&item).Update("selected_candidate_json", item.SelectedCandidateJSON).Error; err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"openalex", "unpaywall"} {
		active, err := activePaperImport(t.Context(), "alice", "target", "work", "publishedVersion", provider)
		if err != nil || active != (provider == "openalex") {
			t.Fatal("different providers shared an active revision", active, err)
		}
	}
}

func TestCurrentImportsFilterBeforePagination(t *testing.T) {
	db := paperLibraryTestDB(t, orm.OpenTestDB(t).DB)
	now := time.Now().UTC()
	for index := 0; index < 25; index++ {
		id := fmt.Sprintf("batch-%02d", index)
		status, kind := "succeeded", "metadata_only"
		if index == 0 {
			status, kind = "failed", ""
		}
		if index == 1 {
			status, kind = "pending", ""
		}
		batch := orm.PaperImportBatch{ID: id, CreatedBy: "owner", TargetDatasetID: "papers", EntryType: "paper_link", Status: status, CreatedAt: now.Add(time.Duration(index) * time.Minute), SourceContextJSON: marshal(map[string]string{}), SourceDocumentIDsJSON: marshal([]string{}), PolicySnapshotJSON: marshal(map[string]string{})}
		item := orm.PaperImportItem{ID: id, BatchID: id, Status: status, ResultKind: kind, ReferenceIDsJSON: marshal([]string{}), PresenceSnapshotJSON: marshal(map[string]string{}), SelectedCandidateJSON: marshal(map[string]string{}), ErrorDetailsJSON: marshal(map[string]string{})}
		for _, row := range []any{&batch, &item} {
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	response := libraryRequest(t, "GET", "/imports?view=operations&filter=current&limit=1", "papers", "owner", nil)
	var page struct {
		Items        []ImportOperation `json:"items"`
		Next         string            `json:"next_cursor"`
		Notification struct {
			Active      int `json:"active"`
			NeedsAction int `json:"needs_action"`
		} `json:"notification"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || response.Code != 200 || len(page.Items) != 1 || page.Items[0].ID != "batch-01" || page.Next == "" || page.Notification.Active != 1 || page.Notification.NeedsAction != 1 {
		t.Fatalf("old current tasks hidden: %d %s %v", response.Code, response.Body.String(), err)
	}
	response = libraryRequest(t, "GET", "/imports?view=operations&filter=current&cursor="+page.Next, "papers", "owner", nil)
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].ID != "batch-00" {
		t.Fatal(response.Body.String(), err)
	}
}

func TestImportRetryHandlesUploadedAndReferenceProcessingFailures(t *testing.T) {
	for _, entry := range []string{"pdf_upload", "single_reference", "paper_expansion"} {
		t.Run(entry, func(t *testing.T) {
			db := paperLibraryTestDB(t, orm.OpenTestDB(t).DB)
			previousTransport := http.DefaultTransport
			http.DefaultTransport = sourceTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"items":[{"dataset_id":"papers","exists":false,"allowed":true}],"source_map":{}}`)), Request: r}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = previousTransport })
			if err := db.AutoMigrate(&orm.AsyncJob{}); err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&orm.Dataset{}).Where("id=?", "papers").Update("processing_level", "parsed").Error; err != nil {
				t.Fatal(err)
			}
			batch := orm.PaperImportBatch{ID: "retry", CreatedBy: "owner", TargetDatasetID: "papers", EntryType: entry, Status: "succeeded", SourceContextJSON: marshal(map[string]string{}), SourceDocumentIDsJSON: marshal([]string{}), PolicySnapshotJSON: marshal(map[string]string{})}
			item := orm.PaperImportItem{ID: "retry-item", BatchID: batch.ID, DocumentID: "pdf", DocumentTaskID: "parse", Status: "succeeded", Stage: "processing", ReferenceIDsJSON: marshal([]string{}), PresenceSnapshotJSON: marshal(map[string]string{}), SelectedCandidateJSON: marshal(map[string]string{}), ErrorDetailsJSON: marshal(map[string]string{})}
			for _, row := range []any{&batch, &item, &orm.Document{ID: "pdf", DatasetID: "papers", DisplayName: "paper.pdf", DocumentType: "PDF", Ext: marshal(map[string]string{}), BaseModel: orm.BaseModel{CreateUserID: "owner"}}, &orm.Task{ID: "parse", DatasetID: "papers", DocID: "pdf", Ext: marshal(map[string]string{"task_state": "FAILED"})}, &orm.DocumentProcessingState{DocumentID: "pdf", DatasetID: "papers", ParseStatus: "failed", ParseErrorCode: "PARSE_FAILED", ParseErrorMessage: "parser unavailable", UpdatedAt: time.Now().Add(time.Hour)}} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			retry := func(body string) *httptest.ResponseRecorder {
				r := mux.SetURLVars(httptest.NewRequest("POST", "/imports/retry:retry", strings.NewReader(body)), map[string]string{"batch_id": batch.ID})
				r.Header.Set("X-User-Id", "owner")
				w := httptest.NewRecorder()
				RetryImport(w, r)
				return w
			}
			first := retry(`{"idempotency_key":"once"}`)
			if first.Code != 200 {
				t.Fatal(first.Code, first.Body.String())
			}
			if again := retry(`{"idempotency_key":"once"}`); again.Code != 200 || again.Body.String() != first.Body.String() {
				t.Fatal("lost response duplicated retry", again.Code, again.Body.String())
			}
			if again := retry(`{"idempotency_key":"another"}`); again.Code != 409 {
				t.Fatal("active import accepted another retry", again.Code)
			}
			if err := db.Where("id=?", item.ID).Take(&item).Error; err != nil || item.Stage != "reparse" || item.DocumentID != "pdf" || item.Status != "pending" {
				t.Fatalf("binding or reparse lost: %+v %v", item, err)
			}
			var count int64
			db.Model(&orm.AsyncJob{}).Count(&count)
			if count != 1 {
				t.Fatal("duplicated worker", count)
			}
		})
	}
}

func TestExpansionPreviewPreservesHostedAndVerifiedReadingCandidates(t *testing.T) {
	db := paperFixture(t)
	ref := PaperRef{PaperID: "a", Title: "A verified paper", SourceProvider: "openalex", SourceVersion: "submittedVersion", ArxivID: "2404.06654", PDFURL: "https://publisher.example/primary.pdf"}
	primary := FulltextCandidate{URL: ref.PDFURL, SourceProvider: "openalex", Version: ref.SourceVersion, SourceType: "open_access_pdf", License: "cc-by", ExpectedMIME: "application/pdf"}
	hosted := FulltextCandidate{URL: "https://content.openalex.org/works/W123.pdf", SourceProvider: "openalex", Version: ref.SourceVersion, SourceType: "openalex_hosted", License: "cc-by", ExpectedMIME: "application/pdf"}
	record := orm.AcademicExpansion{ID: "import-source", UserID: "u", DatasetID: "papers", Version: 1, DiscoveryMode: "external", SeedSnapshotJSON: marshal([]paperSource{}), DiscoveryJSON: marshal([]paperDiscovery{{Papers: []PaperRef{ref}, Candidates: map[string][]FulltextCandidate{"a": {primary, hosted}}}}), ResultJSON: marshal(ReviewWorkflowInput{Papers: []WorkflowPaper{{Paper: ref}}})}
	quality := contentQuality{Downloaded: true, Parsed: true, Eligible: true, Identity: "matched", Material: "main", Text: "usable", Acquisition: contentAcquisition{Candidate: &hosted}}
	content := orm.AcademicPaperContent{ID: "verified-read", UserID: "u", PaperID: "a", ParserVersion: orm.AcademicPaperParserVersion, SourceKind: "external", SourceProvider: "openalex", SourceVersion: ref.SourceVersion, SourceHash: "verified-byte-hash", Status: "ready", ContentHash: "text-hash", PagesJSON: marshal([]ContentPage{{Page: 1, Text: "Verified text"}}), QualityJSON: marshal(quality)}
	for _, row := range []any{&record, &content} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	PreviewImport(w, paperRequest(`{"expansion_id":"import-source","work_ids":["a"],"target_dataset_id":"papers"}`))
	var response struct {
		Token string        `json:"preview_token"`
		Items []PreviewItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 || len(response.Items) != 1 {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	item := response.Items[0]
	if len(item.Candidates) != 2 || item.Candidates[0] != hosted || item.Paper.SourceProvider != "openalex" || item.Paper.PDFURL != hosted.URL {
		t.Fatalf("verified source was rebuilt or version coerced: %+v", item)
	}
	receipt, err := loadImportPreview(t.Context(), response.Token, "u", "papers")
	if err != nil || receipt.Hashes["a|"+hosted.URL] != content.SourceHash {
		t.Fatal("verified bytes not pinned", err)
	}
}

func TestDOIPreviewDistinguishesFulltextFailureFromAbsence(t *testing.T) {
	for _, status := range []int{503, 404, 200} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			paperFixture(t)
			previous := http.DefaultTransport
			http.DefaultTransport = sourceTransport(func(r *http.Request) (*http.Response, error) {
				code, body := 200, `{"message":{"title":["DOI paper"],"DOI":"10.1234/example","author":[{"given":"Jane","family":"Doe"}]}}`
				if r.URL.Host == "api.unpaywall.org" {
					code, body = status, `{"best_oa_location":{"url_for_pdf":"https://publisher.example/paper.pdf","version":"publishedVersion"}}`
				}
				return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = previous })
			w := httptest.NewRecorder()
			PreviewImport(w, paperRequest(`{"identifier":"10.1234/example","target_dataset_id":"papers"}`))
			var response struct {
				Items []PreviewItem `json:"items"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 || len(response.Items) != 1 {
				t.Fatal(w.Code, w.Body.String(), err)
			}
			item := response.Items[0]
			if item.Title != "DOI paper" || (item.FulltextError != nil) != (status == 503) {
				t.Fatalf("lookup failure lost: %+v", item)
			}
			if status == 200 && (len(item.Candidates) != 1 || item.Candidates[0].SourceProvider != "unpaywall" || item.Candidates[0].Version != "publishedVersion") {
				t.Fatalf("DOI fulltext provenance lost: %+v", item)
			}
		})
	}
}
