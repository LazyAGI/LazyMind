package doc

import (
	"context"
	"encoding/json"
	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/readingpolicy"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAutomaticReadingEnrollmentFutureOnlyDurableAndBounded(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.AsyncJob{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	seedDocumentListDataset(t, db, "ds", "owner")
	db.Model(&orm.Dataset{}).Where("id=?", "ds").Update("scene", "academic_papers")
	p := readingpolicy.Defaults()
	p.Preset = "full"
	p.Limits.DailyDocuments = 2
	p.Limits.StudyItemsPerDocument = 3
	p, err := readingpolicy.Save(ctx, db.DB, "owner", p)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"old", "new1", "new2", "new3"} {
		created := p.AutomaticEligibleAfter.Add(time.Duration(i) * time.Second)
		if i == 0 {
			created = created.Add(-time.Hour)
		}
		seedDocumentListDoc(t, db, "ds", id, id+".pdf", created.Add(time.Hour), "owner", nil)
		source := filepath.Join(t.TempDir(), "paper.pdf")
		os.WriteFile(source, []byte("%PDF-original"), 0600)
		db.Model(&orm.Document{}).Where("id=?", id).Update("ext", mustJSON(documentExt{StoredPath: source, OriginalFilename: id + ".pdf"}))
	}
	for i := 0; i < 2; i++ {
		if err := enrollReadingPreparation(ctx, db.DB); err != nil {
			t.Fatal(err)
		}
	}
	var jobs []orm.AsyncJob
	db.Where("job_type=?", ReadingPreparationJob).Find(&jobs)
	if len(jobs) != 2 {
		t.Fatalf("quota/dedupe: %d", len(jobs))
	}
	for _, j := range jobs {
		var payload readingPreparationPayload
		json.Unmarshal(j.PayloadJSON, &payload)
		if payload.DocumentID == "old" || payload.ItemLimit != 3 || !payload.Automatic || len(payload.Scopes) != 4 || j.MaxAttempts != 1 {
			t.Fatalf("bad enrollment: %+v %+v", j, payload)
		}
	}
	// Terminal failures consume their reservation and never silently reenroll.
	db.Model(&orm.AsyncJob{}).Where("job_type=?", ReadingPreparationJob).Update("status", "failed")
	if err := enrollReadingPreparation(ctx, db.DB); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&orm.AsyncJob{}).Count(&count)
	if count != 2 {
		t.Fatal("failed job retried automatically")
	}
	p.Preset = "demand"
	if _, err := readingpolicy.Save(ctx, db.DB, "owner", p); err != nil {
		t.Fatal(err)
	}
	if err := readingpolicy.Check(readingpolicy.AutomaticContext(ctx), db.DB, "owner", readingpolicy.Formula); err == nil {
		t.Fatal("stopped policy permits queued call")
	}
}
func TestExplicitReadingJobIdentityIncludesUserAndScopes(t *testing.T) {
	db := newDocumentTestDB(t)
	db.AutoMigrate(&orm.AsyncJob{})
	row := orm.Document{ID: "doc", DatasetID: "ds"}
	ctx := context.Background()
	a, e := enqueueReadingPreparation(ctx, db.DB, row, "a", []string{"formula"}, false, "hash", 10)
	if e != nil {
		t.Fatal(e)
	}
	b, e := enqueueReadingPreparation(ctx, db.DB, row, "b", []string{"formula"}, false, "hash", 10)
	if e != nil {
		t.Fatal(e)
	}
	c, e := enqueueReadingPreparation(ctx, db.DB, row, "a", []string{"figure"}, false, "hash", 10)
	if e != nil {
		t.Fatal(e)
	}
	if a.ID == b.ID || a.ID == c.ID || b.ID == c.ID {
		t.Fatal("distinct preparation collided")
	}
}

func TestReadOnlyParseStatusNeverCreatesTask(t *testing.T) {
	installOriginalSourceTransport(t)
	db := newDocumentTestDB(t)
	seedDocumentListDataset(t, db, "ds", "owner")
	seedDocumentListDoc(t, db, "ds", "doc", "paper.pdf", time.Now(), "owner", nil)
	r := httptest.NewRequest("GET", "/parse-status", nil)
	r.Header.Set("X-User-Id", "owner")
	r = mux.SetURLVars(r, map[string]string{"dataset": "ds", "document": "doc"})
	w := httptest.NewRecorder()
	EnsureParsed(w, r)
	if w.Code != 200 {
		t.Fatalf("status=%d %s", w.Code, w.Body)
	}
	var body EnsureDocumentParsedResult
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Status != "not_requested" {
		t.Fatalf("read status: %s %v", w.Body, err)
	}
	var count int64
	db.Model(&orm.Task{}).Count(&count)
	if count != 0 {
		t.Fatal("read-only poll created parsing task")
	}
}

func TestReadingModelRequestCounterPersistsFailureWithoutDeletingOutputs(t *testing.T) {
	db := newDocumentTestDB(t)
	db.AutoMigrate(&orm.AsyncJob{})
	if err := db.Create(&orm.AsyncJob{ID: "count-job", JobType: ReadingPreparationJob, Status: "running"}).Error; err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(502)
		_, _ = w.Write([]byte(`{"detail":{"error_code":"model_call_failed","usage":{"model_calls":1}}}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	counter := &readingCallCounter{jobID: "count-job"}
	ctx := context.WithValue(context.Background(), readingCallCounterKey{}, counter)
	old := filepath.Join(t.TempDir(), "saved.json")
	os.WriteFile(old, []byte("existing result"), 0600)
	_, err := defaultFormulaHandler().generate(ctx, `{"language":"zh"}`, "", map[string]any{})
	if err == nil || calls != 1 {
		t.Fatalf("transport: calls=%d err=%v", calls, err)
	}
	var row orm.AsyncJob
	if err := db.Where("id=?", "count-job").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if preparationStatus(row).ModelRequests != 1 {
		t.Fatal("failed model request not accounted")
	}
	data, err := os.ReadFile(old)
	if err != nil || string(data) != "existing result" {
		t.Fatal("existing output deleted")
	}
}
