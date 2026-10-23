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
)

func TestExternalDiscoveryReusesOnlyInitiatingCollection(t *testing.T) {
	db := paperFixture(t)
	for _, dataset := range []string{"papers", "other"} {
		if dataset == "other" {
			if err := db.Create(&orm.Dataset{ID: dataset, KbID: dataset, ScenarioKey: "academic_papers", Ext: marshal(map[string]string{}), BaseModel: orm.BaseModel{CreateUserID: "u"}}).Error; err != nil {
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
	SearchPapers(w, paperRequest(string(marshal(map[string]any{"expansion_id": record.ID, "query": "all:agents"}))))
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
		foreign := orm.AcademicPaperContent{ID: "foreign-" + paper.PaperID[:8], UserID: "u", PaperID: paper.PaperID, ParserVersion: orm.AcademicPaperParserVersion, SourceKey: "other", SourceKind: "kb", SourceVersion: "v1", SourceDatasetID: "other", SourceDocumentID: "doc-" + paper.ArxivID, Status: "ready", ContentHash: "body", PagesJSON: marshal([]ContentPage{{Page: 1, Text: "other collection's private body"}})}
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
	row := orm.AcademicPaperContent{ID: "scoped-body", UserID: "u", PaperID: "a", ParserVersion: orm.AcademicPaperParserVersion, SourceKey: seeds[0].key(), SourceKind: "kb", SourceDatasetID: "papers", SourceDocumentID: "a", Status: "ready", ContentHash: "hash", PagesJSON: marshal(pages)}
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
