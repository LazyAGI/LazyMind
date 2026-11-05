package academic

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sourceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, e := orm.Connect(orm.DriverSQLite, filepath.Join(t.TempDir(), "test.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	if e = db.AutoMigrate(&orm.AcademicWork{}, &orm.AcademicWorkDocument{}, &orm.Conversation{}, &orm.ChatHistory{}, &orm.MultiAnswersChatHistory{}, &orm.SubAgentTask{}, &orm.PaperImportBatch{}, &orm.PaperImportItem{}, &orm.AsyncJob{}, &orm.Document{}, &orm.UploadedFile{}); e != nil {
		t.Fatal(e)
	}
	sql, e := db.DB.DB()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { sql.Close() })
	return db.DB
}
func TestOfficialArxivSources(t *testing.T) {
	for _, url := range []string{"https://arxiv.org/abs/2210.03629", "https://arxiv.org/pdf/2210.03629v2.pdf", "https://export.arxiv.org/html/2210.03629v3", "http://www.arxiv.org/abs/2210.03629?context=cs"} {
		if id := officialArxivID(url); id != "2210.03629" {
			t.Errorf("%s: %s", url, id)
		}
	}
	if id := officialArxivID("https://arxiv.org/abs/hep-th/9901001v2"); id != "hep-th/9901001" {
		t.Fatal(id)
	}
	for _, url := range []string{"https://arxiv.org.evil.test/abs/2210.03629", "https://evil.test/arxiv.org/pdf/2210.03629", "https://arxiv.org@evil.test/pdf/2210.03629", "https://arxiv.org:8443/pdf/2210.03629", "https://arxiv.org/foo/2210.03629", "https://arxiv.org/abs/2213.03629", "https://arxiv.org/abs/2210.03629garbage", "https://arxiv.org/abs/2210.03629v0", "file:///abs/2210.03629", "https://arxiv.org/abs/2210.03"} {
		if officialArxivID(url) != "" {
			t.Errorf("accepted %s", url)
		}
	}
}

type forbiddenNetwork struct{ t *testing.T }

func (f forbiddenNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	f.t.Error("qualification attempted network access")
	return nil, errors.New("offline")
}
func TestInspectionIsAuthorizedReadOnlyAndOffline(t *testing.T) {
	db := sourceTestDB(t)
	ctx := context.Background()
	previous := http.DefaultTransport
	http.DefaultTransport = forbiddenNetwork{t}
	t.Cleanup(func() { http.DefaultTransport = previous })
	conv := orm.Conversation{ID: "chat", BaseModel: orm.BaseModel{CreateUserID: "alice"}}
	if e := db.Create(&conv).Error; e != nil {
		t.Fatal(e)
	}
	h := orm.ChatHistory{ID: "answer", ConversationID: "chat", RetrievalResult: json.RawMessage(`{"sources":{"1":{"url":"https://arxiv.org/abs/2210.03629","title":"ReAct"},"2":{"url":"https://arxiv.org/pdf/2210.03629v2.pdf"},"3":{"url":"https://example.com/paper.pdf","title":"ReAct"},"4":{"url":"https://doi.org/10.1234/example"},"5":{"url":"https://doi.org/10.48550/arXiv.2210.03629v3"}}}`)}
	if e := db.Create(&h).Error; e != nil {
		t.Fatal(e)
	}
	c := SourceContext{ConversationID: "chat", HistoryID: "answer"}
	rows, e := inspectSources(ctx, db, "alice", c, nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != 5 || !rows[0].Eligible || rows[0].PaperKey != rows[1].PaperKey || rows[2].Eligible || !rows[3].Eligible || !rows[3].NeedsPDFLookup || rows[3].PaperKey != "doi:10.1234/example" || rows[3].Candidate.URL != "" {
		t.Fatalf("%+v", rows)
	}
	if doi := rows[4]; !doi.Eligible || doi.PaperKey != rows[0].PaperKey || doi.Candidate.URL != "https://arxiv.org/pdf/2210.03629v3" {
		t.Fatalf("arXiv DOI should match the direct arXiv link and preserve its revision: %+v", doi)
	}
	if _, e = inspectSources(ctx, db, "mallory", c, nil); e == nil {
		t.Fatal("cross-user inspection allowed")
	}
	if _, e = inspectSources(ctx, db, "alice", c, []string{"forged"}); e == nil {
		t.Fatal("forged source allowed")
	}
	for _, model := range []any{&orm.AcademicWork{}, &orm.AcademicWorkDocument{}, &orm.PaperImportBatch{}, &orm.PaperImportItem{}, &orm.Document{}, &orm.AsyncJob{}} {
		var n int64
		if e = db.Model(model).Count(&n).Error; e != nil || n != 0 {
			t.Fatalf("inspection mutated %T: %d %v", model, n, e)
		}
	}
}
func TestUntrustedFulltextCannotQualify(t *testing.T) {
	db := sourceTestDB(t)
	ctx := context.Background()
	input := WorkInput{DOI: "10.1234/test", Title: "Paper", Provenance: map[string]any{"pdf_url": "https://example.org/paper.pdf", "trusted_fulltext": true}}
	if _, _, e := findOrCreateWork(ctx, db, input); e != nil {
		t.Fatal(e)
	}
	source := sourceRecord{URL: "https://doi.org/10.1234/test"}
	row, e := inspectRecord(ctx, db, source)
	if e != nil {
		t.Fatal(e)
	}
	if !row.NeedsPDFLookup || row.Candidate.URL != "" {
		t.Fatal("client supplied trust marker accepted")
	}
	input.TrustedFulltext = true
	if _, _, e = findOrCreateWork(ctx, db, input); e != nil {
		t.Fatal(e)
	}
	row, e = inspectRecord(ctx, db, source)
	if e != nil || !row.Eligible {
		t.Fatalf("trusted existing metadata rejected: %+v %v", row, e)
	}
	row, e = inspectRecord(ctx, db, sourceRecord{URL: "https://example.org/paper.pdf"})
	if e != nil || !row.Eligible || row.PaperKey != "doi:10.1234/test" {
		t.Fatalf("trusted original URL rejected: %+v %v", row, e)
	}
}

func TestSourcePDFLookupMakesAgentCitationImportable(t *testing.T) {
	db := sourceTestDB(t)
	const doi = "10.1234/cited"
	const pdf = "https://publisher.example/cited.pdf"
	var metadataCalls, pdfCalls atomic.Int32
	ctx := context.WithValue(t.Context(), paperPDFClientKey{}, pdfLookupTestClient(func(r *http.Request) (int, string) {
		switch r.URL.Host {
		case "api.openalex.org":
			return 200, `{"results":[]}`
		case "api.unpaywall.org":
			if metadataCalls.Add(1) == 1 {
				return 429, "rate limited"
			}
			if strings.HasSuffix(r.URL.Path, doi) {
				return 200, `{"best_oa_location":{"url_for_pdf":"https://publisher.example/cited.pdf"}}`
			}
			return 404, ""
		case "publisher.example":
			if pdfCalls.Add(1) == 1 {
				return 503, "temporarily unavailable"
			}
			return 206, "%PDF-1.7 verified"
		default:
			t.Errorf("unexpected lookup host: %s", r.URL.Host)
			return 404, ""
		}
	}))
	record := sourceRecord{ID: "2.1", URL: "https://publisher.example/cited", DOI: doi, Title: "Cited paper"}
	row, err := inspectRecord(ctx, db, record)
	if err != nil || !row.NeedsPDFLookup {
		t.Fatalf("inspect: %+v %v", row, err)
	}
	items := []SourceInspection{row}
	if err = lookupSourcePDFs(ctx, db, "alice", SourceContext{}, items); err != nil || items[0].NeedsPDFLookup {
		t.Fatalf("lookup: %+v %v", items, err)
	}
	if metadataCalls.Load() != 2 || pdfCalls.Load() != 2 || items[0].PDFLookupFailed {
		t.Fatalf("automatic recovery: metadata=%d PDF=%d items=%+v", metadataCalls.Load(), pdfCalls.Load(), items)
	}
	// Import resolves the saved citation again without a network call.
	row, err = inspectRecord(t.Context(), db, record)
	if err != nil || row.NeedsPDFLookup || row.Candidate.URL != pdf || row.PaperKey != "doi:"+doi {
		t.Fatalf("import inspection: %+v %v", row, err)
	}
}

func TestSourcePDFLookupDistinguishesUnavailableAndFailed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		calls  int32
		failed bool
	}{
		{"no record", 404, "", 1, false},
		{"service remains unavailable", 503, "unavailable", 3, true},
		{"access denied", 403, "forbidden", 1, true},
		{"invalid response", 200, "not JSON", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := sourceTestDB(t)
			var calls atomic.Int32
			ctx := context.WithValue(t.Context(), paperPDFClientKey{}, pdfLookupTestClient(func(r *http.Request) (int, string) {
				if r.URL.Host == "api.openalex.org" {
					return 200, `{"results":[]}`
				}
				calls.Add(1)
				return tc.status, tc.body
			}))
			items := []SourceInspection{{SourceID: "1", Eligible: true, NeedsPDFLookup: true, Work: WorkInput{DOI: "10.1234/test"}}}
			if err := lookupSourcePDFs(ctx, db, "alice", SourceContext{}, items); err != nil {
				t.Fatal(err)
			}
			if !items[0].NeedsPDFLookup || items[0].PDFLookupFailed != tc.failed || calls.Load() != tc.calls {
				t.Fatalf("items=%+v calls=%d", items, calls.Load())
			}
		})
	}
}

func TestSourcePickerIgnoresSavedRecommendationsAndChecksReferencePDFs(t *testing.T) {
	db := paperFixture(t).DB
	source := SourceContext{ConversationID: "chat", HistoryID: "answer"}
	for _, record := range []any{
		&orm.Conversation{ID: source.ConversationID, BaseModel: orm.BaseModel{CreateUserID: "u"}},
		&orm.ChatHistory{ID: source.HistoryID, ConversationID: source.ConversationID, RetrievalResult: json.RawMessage(`{
			"sources":[{"source_id":"html","url":"https://arxiv.org/abs/2405.12981"},
			           {"source_id":"pdf","url":"https://arxiv.org/abs/2210.03629","title":"ReAct"}],
			"recommended_papers":[{"title":"Toolformer","arxiv_id":"2302.04761"}]
		}`)},
	} {
		if err := db.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("LAZYMIND_AGENTIC_WORKSPACE", "")
	items, err := inspectSourcePDFs(t.Context(), db, "u", source, nil)
	if err != nil || len(items) != 2 || items[0].SourceID != "html" || items[1].SourceID != "pdf" ||
		!items[0].NeedsPDFLookup || !items[1].NeedsPDFLookup {
		t.Fatalf("unverified sources: %+v, %v", items, err)
	}
	client := pdfLookupTestClient(func(r *http.Request) (int, string) {
		if r.URL.String() == "https://arxiv.org/pdf/2210.03629" {
			return 206, "%PDF-1.7 verified"
		}
		return 200, "<html>no PDF</html>"
	})
	ctx := context.WithValue(t.Context(), paperPDFClientKey{}, client)
	if err := lookupSourcePDFs(ctx, db, "u", source, items); err != nil || !items[0].NeedsPDFLookup || items[1].NeedsPDFLookup {
		t.Fatalf("PDF probe results: %+v, %v", items, err)
	}
	items, err = inspectSourcePDFs(t.Context(), db, "u", source, nil)
	if err != nil || !items[0].NeedsPDFLookup || items[1].NeedsPDFLookup {
		t.Fatalf("reopened picker: %+v, %v", items, err)
	}
}

func TestWorkSourceOwnershipAndSourceWorkIdentity(t *testing.T) {
	db := sourceTestDB(t)
	ctx := context.Background()
	task := orm.SubAgentTask{ID: "task", CreateUserID: "alice", InputSlots: json.RawMessage(`[]`), OutputSlots: json.RawMessage(`[]`), Sources: orm.RawJSON(`[{"url":"https://arxiv.org/html/2210.03629v2"}]`)}
	if e := db.Create(&task).Error; e != nil {
		t.Fatal(e)
	}
	rows, e := inspectSources(ctx, db, "alice", SourceContext{TaskID: "task"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	a, e := sourceWork(ctx, db, rows[0])
	if e != nil {
		t.Fatal(e)
	}
	b, e := sourceWork(ctx, db, rows[0])
	if e != nil || a.ID != b.ID {
		t.Fatal("identity duplicated", e)
	}
	if _, e = inspectSources(ctx, db, "bob", SourceContext{TaskID: "task"}, nil); e == nil {
		t.Fatal("cross-user Work allowed")
	}
}
func TestSourceJobFenceRejectsLateAttempt(t *testing.T) {
	db := sourceTestDB(t)
	job := orm.AsyncJob{ID: "job", Status: "running", AttemptCount: 2, PayloadJSON: json.RawMessage(`{}`)}
	if e := db.Create(&job).Error; e != nil {
		t.Fatal(e)
	}
	batch := orm.PaperImportBatch{ID: "batch", AsyncJobID: job.ID, Status: "pending", SourceDocumentIDsJSON: json.RawMessage(`[]`), PolicySnapshotJSON: json.RawMessage(`{}`)}
	if e := db.Create(&batch).Error; e != nil {
		t.Fatal(e)
	}
	if e := sourceJobFence(db, asyncjob.Job{ID: "job", AttemptCount: 1}, batch.ID); e == nil {
		t.Fatal("late attempt allowed")
	}
	if e := sourceJobFence(db, asyncjob.Job{ID: "job", AttemptCount: 2}, batch.ID); e != nil {
		t.Fatal(e)
	}
	db.Model(&batch).Update("async_job_id", "replacement")
	if e := sourceJobFence(db, asyncjob.Job{ID: "job", AttemptCount: 2}, batch.ID); e == nil {
		t.Fatal("superseded job allowed")
	}
}

func TestSourcePresenceIgnoresOtherLibrariesAndDeletedDocuments(t *testing.T) {
	db := sourceTestDB(t)
	for _, d := range []orm.Document{{ID: "a", DatasetID: "first"}, {ID: "b", DatasetID: "second"}} {
		path := filepath.Join(t.TempDir(), d.ID+".pdf")
		if err := os.WriteFile(path, []byte("%PDF fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		d.Ext = marshal(map[string]string{"stored_path": path})
		if e := db.Create(&d).Error; e != nil {
			t.Fatal(e)
		}
	}
	for _, link := range []orm.AcademicWorkDocument{{AcademicWorkID: "paper", DatasetID: "first", DocumentID: "a"}, {AcademicWorkID: "paper", DatasetID: "second", DocumentID: "b"}} {
		if e := db.Create(&link).Error; e != nil {
			t.Fatal(e)
		}
	}
	d, e := sourcePresentDocument(db, "second", "paper", "", false)
	if e != nil || d.ID != "b" {
		t.Fatalf("wrong library: %+v %v", d, e)
	}
	if _, e = sourcePresentDocument(db, "third", "paper", "", false); !errors.Is(e, gorm.ErrRecordNotFound) {
		t.Fatal(e)
	}
	if e = db.Model(&orm.Document{}).Where("id = ?", "b").Update("deleted_at", time.Now()).Error; e != nil {
		t.Fatal(e)
	}
	if _, e = sourcePresentDocument(db, "second", "paper", "", false); !errors.Is(e, gorm.ErrRecordNotFound) {
		t.Fatal("deleted document suppressed import", e)
	}
}
