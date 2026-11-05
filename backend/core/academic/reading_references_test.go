package academic

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

func TestPaperReferencesListsCluesThenRegistersOnlySelectedIdentity(t *testing.T) {
	db := paperFixture(t)
	sources, err := seedSources(db.DB, "papers", []WorkflowPaper{{Paper: PaperRef{PaperID: "a", SourceDocumentID: "a"}, IsSeed: true}})
	if err != nil {
		t.Fatal(err)
	}
	text := "References\n[1] " + houlsbyCitation + "\n[2] DeepSeek-AI. Another useful paper, 2025. arXiv:2501.01234."
	content := orm.AcademicPaperContent{ID: "reference-body", UserID: "u", PaperID: "a", ParserVersion: orm.AcademicPaperParserVersion, SourceKey: sources[0].key(), SourceKind: "kb", SourceDatasetID: "papers", SourceDocumentID: "a", Status: "ready", ContentHash: "bibliography-hash", PagesJSON: marshal([]ContentPage{{Page: 10, Text: text}}), QualityJSON: marshal(contentQuality{Eligible: true, Identity: "matched", Material: "main", Text: "usable"})}
	record := orm.AcademicExpansion{ID: "reference-reading", UserID: "u", DatasetID: "papers", Status: "preparing", DiscoveryMode: "external", PreparationRevision: 1, ResultJSON: marshal(ReviewWorkflowInput{}), SeedSnapshotJSON: marshal(sources), DiscoveryJSON: marshal([]paperDiscovery{})}
	for _, row := range []any{&content, &record} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	requests := 0
	transport := http.DefaultTransport
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "export.arxiv.org" {
			return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
		}
		requests++
		if r.URL.Query().Get("id_list") != "1902.00751" {
			t.Errorf("unexpected provider lookup: %s", r.URL)
		}
		body := strings.Replace(houlsbyAtom("1902.00751", "v1"), "</published>", "</published><updated>2019-06-10T00:00:00Z</updated>", 1)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/atom+xml"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = transport })
	var first struct {
		References []paperReferenceClue `json:"references"`
		Next       string               `json:"next_cursor"`
		Total      int                  `json:"total_references"`
	}
	w := httptest.NewRecorder()
	ReadPaperContent(w, paperRequest(`{"expansion_id":"reference-reading","paper_id":"a","view":"references","max_results":1}`))
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil || w.Code != 200 || len(first.References) != 1 || first.Next == "" || first.Total != 2 || requests != 0 {
		t.Fatalf("list clues: %d %s, requests=%d err=%v", w.Code, w.Body.String(), requests, err)
	}
	w = httptest.NewRecorder()
	ReadPaperContent(w, paperRequest(string(marshal(map[string]string{"cursor": first.Next}))))
	var last struct {
		References []paperReferenceClue `json:"references"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &last); err != nil || w.Code != 200 || len(last.References) != 1 || last.References[0].ReferenceKey != "2" || requests != 0 {
		t.Fatalf("reference cursor: %d %s", w.Code, w.Body.String())
	}
	for i := 0; i < 2; i++ {
		w = httptest.NewRecorder()
		ReadPaperContent(w, paperRequest(`{"expansion_id":"reference-reading","paper_id":"a","view":"references","reference_keys":["1"]}`))
		if err := json.Unmarshal(w.Body.Bytes(), &last); err != nil || w.Code != 200 || len(last.References) != 1 || last.References[0].Paper == nil {
			t.Fatalf("selected identity: %d %s", w.Code, w.Body.String())
		}
		p := last.References[0].Paper
		if p.Title != houlsbyTitle || p.PublishedAt != "2019-02-02T00:00:00Z" || p.SourceVersionDate != "2019-06-10T00:00:00Z" || p.SourceVersion != "v1" {
			t.Fatalf("verified metadata: %+v", p)
		}
		current := orm.AcademicExpansion{}
		if err := db.Where("id=?", record.ID).Take(&current).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := expansionSource(db.DB, current, p.PaperID); err != nil {
			t.Fatalf("resolved paper not registered: %v", err)
		}
	}
	if requests != 1 {
		t.Fatalf("resolved identity not reused: %d requests", requests)
	}
	var documents int64
	if err := db.Model(&orm.Document{}).Where("dataset_id=?", "papers").Count(&documents).Error; err != nil || documents != 2 {
		t.Fatalf("reading imported documents: count=%d err=%v", documents, err)
	}
	var receipts map[string]paperReadReceipt
	if err := db.Where("id=?", record.ID).Take(&record).Error; err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(record.ReadingJSON, &receipts)
	if len(receipts) != 0 {
		t.Fatal("bibliography reading counted as full text coverage")
	}
}

func TestPaperContentLimitPersistsAcrossCursorAndRecordsOnlyDeliveredBlocks(t *testing.T) {
	db := paperFixture(t)
	sources, err := seedSources(db.DB, "papers", []WorkflowPaper{{Paper: PaperRef{PaperID: "a", SourceDocumentID: "a"}, IsSeed: true}})
	if err != nil {
		t.Fatal(err)
	}
	pages := []ContentPage{{Page: 1, Text: "Abstract"}, {Page: 2, Text: "Contents"}, {Page: 3, Text: "Methods"}, {Page: 4, Text: "Baselines"}, {Page: 5, Text: "Discussion"}}
	content := orm.AcademicPaperContent{ID: "limited-body", UserID: "u", PaperID: "a", ParserVersion: orm.AcademicPaperParserVersion, SourceKey: sources[0].key(), SourceKind: "kb", SourceDatasetID: "papers", SourceDocumentID: "a", Status: "ready", ContentHash: "limited-hash", PagesJSON: marshal(pages), QualityJSON: marshal(contentQuality{Eligible: true, Identity: "matched", Material: "main", Text: "usable"})}
	record := orm.AcademicExpansion{ID: "limited-reading", UserID: "u", DatasetID: "papers", Status: "preparing", DiscoveryMode: "external", ResultJSON: marshal(ReviewWorkflowInput{}), SeedSnapshotJSON: marshal(sources), DiscoveryJSON: marshal([]paperDiscovery{})}
	for _, row := range []any{&content, &record} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	request := `{"expansion_id":"limited-reading","paper_id":"a","view":"content","max_results":2}`
	for index, expected := range [][]int{{0, 1}, {2, 3}, {4}} {
		w := httptest.NewRecorder()
		ReadPaperContent(w, paperRequest(request))
		var response struct {
			Pages     []ContentPage `json:"pages"`
			Positions []int         `json:"positions"`
			Next      string        `json:"next_cursor"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 || string(marshal(response.Positions)) != string(marshal(expected)) || len(response.Pages) != len(expected) {
			t.Fatalf("page %d: %d %s, err=%v", index, w.Code, w.Body.String(), err)
		}
		if err := db.Where("id=?", record.ID).Take(&record).Error; err != nil {
			t.Fatal(err)
		}
		var ledger map[string]paperReadReceipt
		if err := json.Unmarshal(record.ReadingJSON, &ledger); err != nil {
			t.Fatal(err)
		}
		if len(ledger["a"].Positions) != expected[len(expected)-1]+1 {
			t.Fatalf("coverage includes unread blocks: %+v", ledger["a"])
		}
		if (index < 2) != (response.Next != "") {
			t.Fatalf("incorrect continuation: %+v", response)
		}
		request = string(marshal(map[string]string{"cursor": response.Next}))
	}
	w := httptest.NewRecorder()
	ReadPaperContent(w, paperRequest(`{"expansion_id":"limited-reading","paper_id":"a","view":"content","max_results":6}`))
	if w.Code != 400 {
		t.Fatalf("unbounded read accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestPaperContentReadObservesProcessingBeforeDeliveringRequestedPositions(t *testing.T) {
	db := paperFixture(t)
	sources, err := seedSources(db.DB, "papers", []WorkflowPaper{{Paper: PaperRef{PaperID: "a", SourceDocumentID: "a"}, IsSeed: true}})
	if err != nil {
		t.Fatal(err)
	}
	content := orm.AcademicPaperContent{ID: "processing-body", UserID: "u", PaperID: "a", ParserVersion: orm.AcademicPaperParserVersion, SourceKey: sources[0].key(), SourceKind: "kb", SourceDatasetID: "papers", SourceDocumentID: "a", Status: "processing", PagesJSON: marshal([]ContentPage{}), QualityJSON: marshal(contentQuality{})}
	record := orm.AcademicExpansion{ID: "processing-reading", UserID: "u", DatasetID: "papers", Status: "preparing", DiscoveryMode: "external", ResultJSON: marshal(ReviewWorkflowInput{}), SeedSnapshotJSON: marshal(sources), DiscoveryJSON: marshal([]paperDiscovery{})}
	for _, row := range []any{&content, &record} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	request := `{"expansion_id":"processing-reading","paper_id":"a","positions":[0],"max_results":1}`
	read := func() (string, []int) {
		t.Helper()
		w := httptest.NewRecorder()
		ReadPaperContent(w, paperRequest(request))
		var response struct {
			Status    string        `json:"status"`
			Level     string        `json:"content_level"`
			Pages     []ContentPage `json:"pages"`
			Positions []int         `json:"positions"`
			Next      string        `json:"next_cursor"`
			Warnings  []string      `json:"warnings"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 {
			t.Fatalf("read: %d %s, err=%v", w.Code, w.Body.String(), err)
		}
		if response.Status == "processing" && (response.Level != "none" || len(response.Pages) != 0 || len(response.Positions) != 0 || response.Next != "" || len(response.Warnings) == 0) {
			t.Fatalf("processing response must not claim a read: %+v", response)
		}
		return response.Status, response.Positions
	}
	if status, _ := read(); status != "processing" {
		t.Fatalf("unexpected content status: %s", status)
	}
	if err := db.Where("id=?", record.ID).Take(&record).Error; err != nil {
		t.Fatal(err)
	}
	var ledger map[string]paperReadReceipt
	if len(record.ReadingJSON) > 0 {
		if err := json.Unmarshal(record.ReadingJSON, &ledger); err != nil {
			t.Fatal(err)
		}
	}
	if len(ledger) != 0 {
		t.Fatalf("processing content counted as read: %+v", ledger)
	}
	if err := db.Model(&content).Updates(map[string]any{"status": "ready", "content_hash": "parsed-body", "pages_json": marshal([]ContentPage{{Page: 1, Text: "Abstract"}, {Page: 2, Text: "Method"}}), "quality_json": marshal(contentQuality{Eligible: true, Identity: "matched", Material: "main", Text: "usable"})}).Error; err != nil {
		t.Fatal(err)
	}
	if status, positions := read(); status != "ready" || string(marshal(positions)) != "[0]" {
		t.Fatalf("parsed body was not delivered: %s %v", status, positions)
	}
	if err := db.Where("id=?", record.ID).Take(&record).Error; err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(record.ReadingJSON, &ledger); err != nil || string(marshal(ledger["a"].Positions)) != "[0]" || ledger["a"].Level != "related_sections" {
		t.Fatalf("receipt includes undelivered blocks: %+v, err=%v", ledger, err)
	}
	w := httptest.NewRecorder()
	ReadPaperContent(w, paperRequest(`{"expansion_id":"processing-reading","paper_id":"a","positions":[2]}`))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "INVALID_REQUEST") || !strings.Contains(w.Body.String(), "total_blocks=2") {
		t.Fatalf("ready content bounds must identify the invalid request: %d %s", w.Code, w.Body.String())
	}
}

func TestSplitAuthorYearReferencesPreservesPageContinuations(t *testing.T) {
	section := `J. Ainslie, J. Lee-Thorp, and S. Sanghai. Gqa: Training models.
 arXiv:2305.13245, 2023.
DeepSeek-AI. Deepseek-v2: A strong mixture-of-experts model.
 CoRR, abs/2405.04434, 2024. URL https://doi.org/10.48550/arXiv.2405.04
 434.
M. Chen, J. Tworek, H. Jun,

 38

 N. Joseph, and W. Zaremba. Evaluating large language models trained on code.
 CoRR, abs/2107.03374, 2021. URL https://arxiv.org/abs/2107.03374.
D. Dua, Y. Wang, and M. Gardner. DROP: A reading comprehension benchmark.
 In Proceedings of NAACL, pages 2368–
 2378. Association for Computational Linguistics, 2019. doi:10.18653/V1/N19-1246.
Anthropic. Claude code. https://code.claude.com/docs/en/overview, 2026.`
	items := splitReferences(section)
	if len(items) != 5 {
		t.Fatalf("got %d citations: %+v", len(items), items)
	}
	if items[1].Arxiv != "2405.04434" || items[1].DOI != "10.48550/arxiv.2405.04434" || !strings.Contains(items[2].Raw, "N. Joseph") || items[3].DOI != "10.18653/v1/n19-1246" {
		t.Fatalf("identity or continuation lost: %+v", items)
	}
	for _, item := range items {
		if conflictingReferenceIDs(item.Raw) {
			t.Fatalf("mixed identities in %s", item.Raw)
		}
	}
	_, err := referenceMetadata(t.Context(), &orm.AcademicReference{RawText: "A. Author. Paper. arXiv:2305.13245, 2023. B. Author. Other. arXiv:2405.04434, 2024."}, false)
	if err != errConflictingPaperIdentifiers {
		t.Fatalf("mixed citation resolved: %v", err)
	}
}

func TestBulletReferenceKeysSelectIndividualEntries(t *testing.T) {
	items := splitReferences("- A. Author. One useful paper, 2023. arXiv:2305.13245.\n- B. Author. Another useful paper, 2024. arXiv:2405.04434.")
	if len(items) != 2 || items[0].Key != "1" || items[1].Key != "2" {
		t.Fatalf("individual entries cannot be selected: %+v", items)
	}
}
