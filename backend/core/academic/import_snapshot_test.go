package academic

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"lazymind/core/common/orm"
)

func TestExpansionImportPinsArxivRevisionAcrossDiscoverySources(t *testing.T) {
	db := paperFixture(t)
	ref := PaperRef{PaperID: "a", Title: "SARA: Singular-Value Based Adaptive Low-Rank Adaption", ArxivID: "2408.03290", SourceProvider: "arxiv", SourceVersion: "v1", PDFURL: "https://arxiv.org/pdf/2408.03290v1"}
	indexed := FulltextCandidate{URL: "https://arxiv.org/pdf/2408.03290", SourceProvider: "openalex", Version: "submittedVersion", SourceType: "open_access_pdf"}
	newer := FulltextCandidate{URL: "https://arxiv.org/pdf/2408.03290v2", SourceProvider: "arxiv", Version: "v2", SourceType: "official_pdf"}
	hosted := FulltextCandidate{URL: "https://content.openalex.org/works/W4403445349.pdf", SourceProvider: "openalex", SourceType: "openalex_hosted"}
	input := ReviewWorkflowInput{Papers: []WorkflowPaper{{Paper: ref}}}
	record := orm.AcademicExpansion{ID: "sara", UserID: "u", DatasetID: "papers", Version: 1, DiscoveryMode: "external", SeedSnapshotJSON: marshal([]paperSource{}), DiscoveryJSON: marshal([]paperDiscovery{{Papers: []PaperRef{ref}}, {Papers: []PaperRef{ref}, Candidates: map[string][]FulltextCandidate{"a": {indexed, newer, hosted}}}}), ResultJSON: marshal(input)}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	PreviewImport(w, paperRequest(`{"expansion_id":"sara","expansion_version":1,"work_ids":["a"],"target_dataset_id":"papers","resolve_external":false}`))
	var response struct {
		Token string        `json:"preview_token"`
		Items []PreviewItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 || len(response.Items) != 1 {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	item := response.Items[0]
	if item.Disposition != "downloadable" || item.Paper.SourceProvider != "arxiv" || item.Paper.SourceVersion != "v1" || len(item.Candidates) != 1 || item.Candidates[0].URL != ref.PDFURL {
		t.Fatalf("preview changed the selected revision: %+v", item)
	}
	// The receipt still imports the selected revision if another round completes.
	if err := db.Model(&record).Update("version", 2).Error; err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	CreateImport(w, paperRequest(string(marshal(createImportRequest{PreviewToken: response.Token, TargetDatasetID: "papers", IdempotencyKey: "sara-import", Items: []selectedImportItem{{WorkID: "a", Candidate: item.Candidates[0]}}}))))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var saved orm.PaperImportItem
	if err := db.Take(&saved).Error; err != nil {
		t.Fatal(err)
	}
	var selected savedImportCandidate
	if err := json.Unmarshal(saved.SelectedCandidateJSON, &selected); err != nil || selected.Version != "v1" || selected.URL != ref.PDFURL || selected.VerifiedPaper == nil || selected.VerifiedPaper.SourceVersion != "v1" {
		t.Fatal("queued import lost its snapshot", selected, err)
	}
	var batch orm.PaperImportBatch
	if err := db.Take(&batch).Error; err != nil {
		t.Fatal(err)
	}
	var origin struct {
		Version int64 `json:"expansion_version"`
	}
	if err := json.Unmarshal(batch.SourceContextJSON, &origin); err != nil || origin.Version != 1 {
		t.Fatal("queued import used the newer round", origin, err)
	}
	w = httptest.NewRecorder()
	CreateImport(w, paperRequest(string(marshal(createImportRequest{PreviewToken: response.Token, TargetDatasetID: "papers", IdempotencyKey: "wrong-revision", Items: []selectedImportItem{{WorkID: "a", Candidate: newer}}}))))
	if w.Code != 409 {
		t.Fatal("unpreviewed revision accepted", w.Code, w.Body.String())
	}
}

func TestExpansionPreviewUsesSelectedCompletedRound(t *testing.T) {
	db := paperFixture(t)
	ref := testVerifiedPaper()
	old := ReviewWorkflowInput{Papers: []WorkflowPaper{{Paper: ref}}}
	current := ReviewWorkflowInput{Papers: []WorkflowPaper{{Paper: PaperRef{PaperID: "b", Title: "B"}}}}
	record := orm.AcademicExpansion{ID: "rounds", UserID: "u", DatasetID: "papers", Version: 2, DiscoveryMode: "external", SeedSnapshotJSON: marshal([]paperSource{}), DiscoveryJSON: marshal([]paperDiscovery{{Papers: []PaperRef{ref}}}), ResultJSON: marshal(current)}
	round := orm.AcademicExpansionRound{ID: "round-one", ExpansionID: record.ID, Version: 1, Status: "completed", InputJSON: marshal(old), ResultJSON: marshal(old)}
	for _, row := range []any{&record, &round} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		version int
		paper   string
		status  int
	}{{1, "a", 200}, {1, "b", 422}, {2, "a", 422}, {3, "a", 409}, {0, "a", 409}} {
		w := httptest.NewRecorder()
		PreviewImport(w, paperRequest(string(marshal(map[string]any{"expansion_id": record.ID, "expansion_version": tc.version, "work_ids": []string{tc.paper}, "target_dataset_id": "papers", "resolve_external": false}))))
		if w.Code != tc.status {
			t.Fatalf("round %d paper %s: %d %s", tc.version, tc.paper, w.Code, w.Body.String())
		}
		if tc.status == 200 {
			var response struct {
				Token string `json:"preview_token"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			receipt, err := loadImportPreview(t.Context(), response.Token, "u", "papers")
			if err != nil || receipt.ExpansionVersion != 1 || receipt.Items[0].Paper.SourceVersion != ref.SourceVersion {
				t.Fatal("preview did not bind the selected round", receipt, err)
			}
		}
	}
}

func TestExpansionImportRejectsUnverifiedAlternateVersions(t *testing.T) {
	db := paperFixture(t)
	ref := PaperRef{PaperID: "a", Title: "Paper", SourceProvider: "openalex", SourceVersion: "submittedVersion", PDFURL: "https://publisher.example/submitted.pdf"}
	for _, candidate := range []FulltextCandidate{
		{URL: "https://publisher.example/published.pdf", SourceProvider: "openalex", Version: "publishedVersion"},
		{URL: "https://content.openalex.org/works/W123.pdf", SourceProvider: "openalex"},
		{URL: "https://other.example/paper.pdf", SourceProvider: "web", Version: "submittedVersion"},
	} {
		source := paperSource{Paper: ref, Candidates: []FulltextCandidate{candidate}}
		quality := contentQuality{Eligible: true, Downloaded: true, Parsed: true, Identity: "matched", Material: "main", Text: "usable", Acquisition: contentAcquisition{Candidate: &candidate}}
		content := orm.AcademicPaperContent{ID: "read", UserID: "u", PaperID: "a", SourceKind: "external", SourceProvider: ref.SourceProvider, SourceVersion: ref.SourceVersion, ParserVersion: orm.AcademicPaperParserVersion, Status: "ready", ContentHash: "parsed", SourceHash: "other-version-bytes", PagesJSON: marshal([]ContentPage{{Page: 1, Text: "body"}}), QualityJSON: marshal(quality)}
		if err := db.Save(&content).Error; err != nil {
			t.Fatal(err)
		}
		candidates, hash, err := expansionImportCandidates(t.Context(), "u", source)
		if err != nil || len(candidates) != 1 || candidates[0].URL != ref.PDFURL || hash != "" {
			t.Fatal("alternate revision trusted as the frozen paper", candidates, hash, err)
		}
	}
}
