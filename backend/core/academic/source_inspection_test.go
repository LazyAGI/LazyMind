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
	h := orm.ChatHistory{ID: "answer", ConversationID: "chat", RetrievalResult: json.RawMessage(`{"sources":{"1":{"url":"https://arxiv.org/abs/2210.03629","title":"ReAct"},"2":{"url":"https://arxiv.org/pdf/2210.03629v2.pdf"},"3":{"url":"https://example.com/paper.pdf","title":"ReAct"},"4":{"url":"https://doi.org/10.1234/example"}}}`)}
	if e := db.Create(&h).Error; e != nil {
		t.Fatal(e)
	}
	c := SourceContext{ConversationID: "chat", HistoryID: "answer"}
	rows, e := inspectSources(ctx, db, "alice", c, nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != 4 || !rows[0].Eligible || rows[0].PaperKey != rows[1].PaperKey || rows[2].Eligible || rows[3].Eligible {
		t.Fatalf("%+v", rows)
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
	if row.Eligible {
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
	d, e := sourcePresentDocument(db, "second", "paper", "")
	if e != nil || d.ID != "b" {
		t.Fatalf("wrong library: %+v %v", d, e)
	}
	if _, e = sourcePresentDocument(db, "third", "paper", ""); !errors.Is(e, gorm.ErrRecordNotFound) {
		t.Fatal(e)
	}
	if e = db.Model(&orm.Document{}).Where("id = ?", "b").Update("deleted_at", time.Now()).Error; e != nil {
		t.Fatal(e)
	}
	if _, e = sourcePresentDocument(db, "second", "paper", ""); !errors.Is(e, gorm.ErrRecordNotFound) {
		t.Fatal("deleted document suppressed import", e)
	}
}
