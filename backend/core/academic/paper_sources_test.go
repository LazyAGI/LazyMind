package academic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestExternalDiscoveryReusesOnlyInitiatingCollection(t *testing.T) {
	db := paperFixture(t)
	for _, dataset := range []string{"papers", "other"} {
		if dataset == "other" {
			if err := db.Create(&orm.Dataset{ID: dataset, KbID: dataset, Scene: "academic_papers", Ext: marshal(map[string]string{}), BaseModel: orm.BaseModel{CreateUserID: "u"}}).Error; err != nil {
				t.Fatal(err)
			}
		}
		id := map[string]string{"papers": "2404.06654", "other": "2210.03629"}[dataset]
		work, _, err := findOrCreateWork(t.Context(), db.DB, WorkInput{Title: id, ArxivID: id})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range []any{
			&orm.Document{ID: "doc-" + id, DatasetID: dataset, DisplayName: id + ".pdf"},
			&orm.AcademicDocumentState{DocumentID: "doc-" + id, DatasetID: dataset, PaperID: work.ID, Kind: "paper", Revision: 1},
			&orm.AcademicWorkDocument{AcademicWorkID: work.ID, DatasetID: dataset, DocumentID: "doc-" + id, SourceProvider: "arxiv", SourceVersion: "v1", ContentSHA256: "hash-" + id},
		} {
			if err = db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	w := httptest.NewRecorder()
	LaunchPaperWorkflow(w, paperRequest(`{"kind":"expansion","dataset_id":"papers","document_ids":["a","b"],"request_key":"external-discovery"}`))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var launch struct {
		ExpansionID string `json:"expansion_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &launch)
	var record orm.AcademicExpansion
	if err := db.Where("id=?", launch.ExpansionID).Take(&record).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := expansionSource(db.DB, record, "unsearched"); err == nil {
		t.Fatal("unsearched candidate accepted")
	}
	previous := http.DefaultTransport
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "export.arxiv.org" {
			return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header), Request: r}, nil
		}
		if r.URL.Query().Get("search_query") == "" {
			t.Errorf("not an external search: %s", r.URL)
			return nil, errors.New("unexpected request")
		}
		feed := `<feed xmlns="http://www.w3.org/2005/Atom">`
		for _, id := range []string{"2404.06654", "2210.03629"} {
			feed += `<entry><id>https://arxiv.org/abs/` + id + `v1</id><title>Real external candidate</title><summary>Original abstract</summary><published>2024-01-01T00:00:00Z</published><author><name>A. Author</name></author></entry>`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(feed + `</feed>`)), Header: make(http.Header), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	w = httptest.NewRecorder()
	SearchPapers(w, paperRequest(string(marshal(map[string]any{"expansion_id": record.ID, "query": "all:agents", "sources": []string{"arxiv"}}))))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := db.Where("id=?", record.ID).Take(&record).Error; err != nil {
		t.Fatal(err)
	}
	var searches []paperDiscovery
	if err := json.Unmarshal(record.DiscoveryJSON, &searches); err != nil || len(searches) != 1 || len(searches[0].Papers) != 2 {
		t.Fatal("discovery evidence missing", err)
	}
	for i, paper := range searches[0].Papers {
		source, err := expansionSource(db.DB, record, paper.PaperID)
		if err != nil {
			t.Fatal(err)
		}
		if source.IsSeed || source.Paper.SourceVersion != "v1" || (i == 0 && source.DocumentID == "") || (i == 1 && source.DocumentID != "") {
			t.Fatalf("invalid discovery source: %+v", source)
		}
		foreign := orm.AcademicPaperContent{QualityJSON: marshal(contentQuality{Eligible: true, Identity: "matched", Material: "main", Text: "usable"}), ID: "foreign-" + paper.PaperID[:8], UserID: "u", PaperID: paper.PaperID, ParserVersion: orm.AcademicPaperParserVersion, SourceKey: "other", SourceKind: "kb", SourceVersion: "v1", SourceDatasetID: "other", SourceDocumentID: "doc-" + paper.ArxivID, Status: "ready", ContentHash: "body", PagesJSON: marshal([]ContentPage{{Page: 1, Text: "other collection's private body"}})}
		if err = db.Create(&foreign).Error; err != nil {
			t.Fatal(err)
		}
		if _, err = findSourceContent(db.DB, "u", source); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatal("cross-collection cache reused", err)
		}
		foreign.ID = "external-" + paper.PaperID[:8]
		foreign.SourceKey = "external"
		foreign.SourceKind = "external"
		foreign.SourceProvider = "arxiv"
		foreign.SourceDatasetID = ""
		foreign.SourceDocumentID = ""
		if err = db.Create(&foreign).Error; err != nil {
			t.Fatal(err)
		}
		if got, err := findSourceContent(db.DB, "u", source); err != nil || got.ID != foreign.ID {
			t.Fatal("publicly acquired cache was not reused", got.ID, err)
		}
		source.Paper.SourceVersion = "v2"
		if _, err = findSourceContent(db.DB, "u", source); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatal("wrong revision reused")
		}
	}
	results, err := presenceForWorks(context.Background(), paperRequest(""), "u", []string{searches[0].Papers[1].PaperID}, "papers", true)
	if err != nil || len(results) != 1 || len(results[0].CurrentDataset) != 0 || len(results[0].OtherDatasets) != 0 {
		t.Fatal("presence escaped collection", results, err)
	}
}

func TestContentReadRequiresTaskAndBindsCursor(t *testing.T) {
	db := paperFixture(t)
	papers := []WorkflowPaper{{Paper: PaperRef{PaperID: "a"}, IsSeed: true}}
	seeds, err := seedSources(db.DB, "papers", papers)
	if err != nil {
		t.Fatal(err)
	}
	record := orm.AcademicExpansion{ID: "content-task", UserID: "u", DatasetID: "papers", ConversationID: "chat", RequestOwner: "u", RequestKey: "read", SeedSnapshotJSON: marshal(seeds), DiscoveryJSON: marshal([]paperDiscovery{}), ResultJSON: marshal(ReviewWorkflowInput{Papers: papers})}
	if err = db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	pages := []ContentPage{}
	for i := 1; i <= 7; i++ {
		pages = append(pages, ContentPage{Page: i, Text: "complete text"})
	}
	row := orm.AcademicPaperContent{QualityJSON: marshal(contentQuality{Eligible: true, Identity: "matched", Material: "main", Text: "usable"}), ID: "scoped-body", UserID: "u", PaperID: "a", ParserVersion: orm.AcademicPaperParserVersion, SourceKey: seeds[0].key(), SourceKind: "kb", SourceDatasetID: "papers", SourceDocumentID: "a", Status: "ready", ContentHash: "hash", PagesJSON: marshal(pages)}
	if err = db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	ReadPaperContent(w, paperRequest(`{"paper_id":"a"}`))
	if w.Code != 400 {
		t.Fatal("missing task accepted", w.Code)
	}
	w = httptest.NewRecorder()
	ReadPaperContent(w, paperRequest(`{"expansion_id":"content-task","paper_id":"a"}`))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var read struct {
		NextCursor string        `json:"next_cursor"`
		Pages      []ContentPage `json:"pages"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &read)
	if len(read.Pages) != 5 || read.NextCursor == "" {
		t.Fatal("first page incomplete")
	}
	w = httptest.NewRecorder()
	ReadPaperContent(w, paperRequest(string(marshal(map[string]string{"cursor": read.NextCursor, "expansion_id": "another-task"}))))
	if w.Code != 400 {
		t.Fatal("cursor rebound to another task", w.Code)
	}
	w = httptest.NewRecorder()
	ReadPaperContent(w, paperRequest(string(marshal(map[string]string{"cursor": read.NextCursor}))))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestOpenAlexSearchIdentityAbstractCacheAndSourceFailures(t *testing.T) {
	db := paperFixture(t)
	record := orm.AcademicExpansion{ID: "openalex-exp", UserID: "u", DatasetID: "papers", RequestOwner: "u", RequestKey: "openalex", Status: "preparing", DiscoveryMode: "external", PreparationRevision: 1, ResultJSON: marshal(ReviewWorkflowInput{Papers: []WorkflowPaper{}}), DiscoveryJSON: marshal([]paperDiscovery{})}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	calls := 0
	previous := http.DefaultTransport
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.openalex.org" {
			return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header), Request: r}, nil
		}
		calls++
		status := 200
		body := `{"results":[]}`
		switch r.URL.Query().Get("search") {
		case "papers":
			body = `{"results":[{"id":"https://openalex.org/W123","title":"Same title","doi":"https://doi.org/10.1000/one","publication_year":2025,"abstract_inverted_index":{"original":[0],"abstract":[1]},"content_urls":{"pdf":"https://content.openalex.org/works/W123.pdf?api_key=must-not-persist"}},{"id":"https://openalex.org/W124","title":"Same title","doi":"https://doi.org/10.1000/two","publication_year":2025}],"meta":{"next_cursor":"next-page"}}`
		case "auth":
			status = 401
		case "rate":
			status = 429
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	search := func(query string) map[string]any {
		w := httptest.NewRecorder()
		SearchPapers(w, paperRequest(string(marshal(map[string]any{"expansion_id": record.ID, "query": query, "sources": []string{"openalex", "sciverse", "web"}, "max_results": 5}))))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var response map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	response := search("papers")
	if len(response["papers"].([]any)) != 2 {
		t.Fatal("same title with different strong IDs was merged")
	}
	papers := response["papers"].([]any)
	first := papers[0].(map[string]any)
	if first["abstract"] != "original abstract" || papers[1].(map[string]any)["abstract"] != nil {
		t.Fatal("original or unknown abstract was lost")
	}
	before := calls
	if search("papers")["sources"].([]any)[0].(map[string]any)["reused"] != true || calls != before {
		t.Fatal("same preparation search was repeated")
	}
	if err := db.Where("id=?", record.ID).Take(&record).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(record.DiscoveryJSON), "must-not-persist") {
		t.Fatal("private hosted content credential persisted")
	}
	sources := response["sources"].([]any)
	if sources[1].(map[string]any)["status"] != "missing_configuration" || sources[2].(map[string]any)["status"] != "missing_configuration" {
		t.Fatal("missing configuration confused with empty results")
	}
	if search("rate")["sources"].([]any)[0].(map[string]any)["status"] != "rate_limited" {
		t.Fatal("rate limit was lost")
	}
	before = calls
	if search("while-cooling")["sources"].([]any)[0].(map[string]any)["reused"] != true || calls != before {
		t.Fatal("rate-limited source was requested again during cooldown")
	}
	var sourceRequest paperSearchRequest
	if err := configureSearchSource(t.Context(), "u", "openalex", &sourceRequest); err != nil {
		t.Fatal(err)
	}
	// Advance to the next independent provider scenario without a wall-clock sleep.
	if err := store.State().Del(t.Context(), "academic/search/cooldown/"+sourceRequest.scope); err != nil {
		t.Fatal(err)
	}
	if search("empty")["sources"].([]any)[0].(map[string]any)["status"] != "empty" {
		t.Fatal("empty results were lost")
	}
	if search("auth")["sources"].([]any)[0].(map[string]any)["status"] != "authentication_failed" {
		t.Fatal("authentication failure was lost")
	}
	before = calls
	search("another-query")
	if calls != before {
		t.Fatal("unusable authenticated source was requested repeatedly")
	}
}

func TestWebSearchLeadsAreSuccessfulResultsWithoutInventingPapers(t *testing.T) {
	db := paperFixture(t)
	base := orm.BaseModel{CreateUserID: "u", CreateUserName: "u"}
	for _, row := range []any{
		&orm.UserModelProvider{ID: "web-provider", Name: "Tavily", Category: "search", BaseModel: base},
		&orm.UserModelProviderGroup{ID: "web-group", UserModelProviderID: "web-provider", Name: "test", APIKey: "fixture", IsVerified: true, BaseModel: base},
		&orm.UserSelectedProvider{UserID: "u", Category: "search", UserModelProviderGroupID: "web-group"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"items": []map[string]string{{"title": "CRAG tutorial", "url": "https://example.org/tutorial", "snippet": "A useful lead, not a verified paper"}}})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "fixture")
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	record := orm.AcademicExpansion{ID: "web-lead", UserID: "u", DatasetID: "papers", RequestOwner: "u", RequestKey: "web-lead", Status: "preparing", DiscoveryMode: "external", PreparationRevision: 1, ResultJSON: marshal(ReviewWorkflowInput{Papers: []WorkflowPaper{}}), DiscoveryJSON: marshal([]paperDiscovery{})}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	SearchPapers(w, paperRequest(`{"expansion_id":"web-lead","query":"CRAG","sources":["web"],"max_results":5}`))
	var response struct {
		Papers  []PaperRef      `json:"papers"`
		Leads   []discoveryLead `json:"leads"`
		Sources []searchOutcome `json:"sources"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(response.Papers) != 0 || len(response.Leads) != 1 || len(response.Sources) != 1 || response.Sources[0].Status != "ok" || response.Sources[0].Count != 1 {
		t.Fatal(w.Body.String())
	}
}

func TestDiscoveryNormalizesArxivDOIAliasesAndRetainsConflictingOwners(t *testing.T) {
	db := paperFixture(t)
	original, _, err := findOrCreateWork(t.Context(), db.DB, WorkInput{Title: "Canonical paper", ArxivID: "2402.09353"})
	if err != nil {
		t.Fatal(err)
	}
	alias, _, err := findOrCreateWork(t.Context(), db.DB, WorkInput{Title: "Canonical paper", DOI: "10.48550/arxiv.2402.09353", Provider: "sciverse", ProviderWorkID: "provider-paper"})
	if err != nil || alias.ID != original.ID || alias.ArxivIDBase != "2402.09353" {
		t.Fatal("arXiv DOI created a second identity", alias, err)
	}
	separate, _, err := findOrCreateWork(t.Context(), db.DB, WorkInput{Title: "Conflicting identity", DOI: "10.1234/separate"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = findOrCreateWork(t.Context(), db.DB, WorkInput{Title: "Provider bridge", DOI: "10.1234/separate", ArxivID: "2402.09353"})
	if !errors.Is(err, errConflictingPaperIdentifiers) {
		t.Fatal("identifier bridge must be an explicit conflict", err)
	}
	var retained orm.AcademicWork
	if err := db.Where("id=?", separate.ID).Take(&retained).Error; err != nil || retained.ArxivIDBase != "" {
		t.Fatal("conflicting work was changed", err)
	}
}
