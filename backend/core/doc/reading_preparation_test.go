package doc

import (
	"context"
	"encoding/json"
	"fmt"
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
		if err := enrollReadingPreparation(ctx, db.DB, nil); err != nil {
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
	if err := enrollReadingPreparation(ctx, db.DB, nil); err != nil {
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

func TestReadingPreparationStatusMatchesCurrentSource(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.AsyncJob{}); err != nil {
		t.Fatal(err)
	}
	seedDocumentListDataset(t, db, "ds", "owner")
	seedDocumentListDoc(t, db, "ds", "doc", "paper.pdf", time.Now(), "owner", nil)
	source := filepath.Join(t.TempDir(), "paper.pdf")
	if err := os.WriteFile(source, []byte("%PDF-current"), 0600); err != nil {
		t.Fatal(err)
	}
	ext := documentExt{StoredPath: source, OriginalFilename: "paper.pdf"}
	if err := db.Model(&orm.Document{}).Where("id=?", "doc").Update("ext", mustJSON(ext)).Error; err != nil {
		t.Fatal(err)
	}
	hash, err := pdfIRHash(ext)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC()
	addJob := func(id, user, status, sourceHash string, offset time.Duration) {
		t.Helper()
		if err := db.Create(&orm.AsyncJob{ID: id, JobType: ReadingPreparationJob, CreateUserID: user, Status: status, CreatedAt: created.Add(offset), PayloadJSON: mustJSON(readingPreparationPayload{DatasetID: "ds", DocumentID: "doc", SourceSHA256: sourceHash})}).Error; err != nil {
			t.Fatal(err)
		}
	}
	call := func(user string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/reading-preparation", nil)
		r.Header.Set("X-User-Id", user)
		r = mux.SetURLVars(r, map[string]string{"dataset": "ds", "document": "doc"})
		w := httptest.NewRecorder()
		GetReadingPreparation(w, r)
		return w
	}
	assertStatus := func(status, jobID string) {
		t.Helper()
		w := call("owner")
		var got struct {
			Data readingPreparationStatus `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK || got.Data.Status != status || got.Data.JobID != jobID {
			t.Fatalf("status: code=%d body=%s error=%v", w.Code, w.Body, err)
		}
	}
	addJob("old-success", "owner", "succeeded", "old-hash", time.Second)
	assertStatus("not_requested", "")
	addJob("old-running", "owner", "running", "old-hash", 2*time.Second)
	assertStatus("not_requested", "")
	addJob("other-user", "other", "running", hash, 3*time.Second)
	assertStatus("not_requested", "")
	addJob("current", "owner", "succeeded", hash, 0)
	assertStatus("succeeded", "current")
	if w := call("outsider"); w.Code != http.StatusNotFound {
		t.Fatalf("unauthorized status: %d %s", w.Code, w.Body)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if w := call("owner"); w.Code != http.StatusConflict {
		t.Fatalf("unavailable source status: %d %s", w.Code, w.Body)
	}
	var count int64
	if err := db.Model(&orm.AsyncJob{}).Count(&count).Error; err != nil || count != 4 {
		t.Fatalf("status poll changed jobs: count=%d err=%v", count, err)
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

func TestAutomaticEnrollmentSkipsHistoryAndAdvancesPastUnavailableBatch(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.AsyncJob{}); err != nil {
		t.Fatal(err)
	}
	seedDocumentListDataset(t, db, "ds", "owner")
	db.Model(&orm.Dataset{}).Where("id=?", "ds").Update("scene", "academic_papers")
	policy := readingpolicy.Defaults()
	policy.Preset = "advance"
	policy, err := readingpolicy.Save(t.Context(), db.DB, "owner", policy)
	if err != nil {
		t.Fatal(err)
	}
	epoch := policy.AutomaticEligibleAfter.Add(time.Hour + time.Second)
	// Unavailable sources must not block a later valid source on every pass.
	for i := 0; i < readingEnrollmentBatch; i++ {
		seedDocumentListDoc(t, db, "ds", fmt.Sprintf("bad-%03d", i), "missing.pdf", epoch, "owner", nil)
	}
	seedDocumentListDoc(t, db, "ds", "valid", "valid.pdf", epoch.Add(time.Second), "owner", nil)
	source := filepath.Join(t.TempDir(), "paper.pdf")
	if err := os.WriteFile(source, []byte("%PDF-original"), 0600); err != nil {
		t.Fatal(err)
	}
	db.Model(&orm.Document{}).Where("id=?", "valid").Update("ext", mustJSON(documentExt{StoredPath: source}))
	// Previously failed enrollment is excluded even when it is from another day.
	seedDocumentListDoc(t, db, "ds", "history", "history.pdf", epoch.Add(-time.Millisecond), "owner", nil)
	if err := db.Create(&orm.AsyncJob{ID: "failed-history", JobType: ReadingPreparationJob, ResourceType: "reading_automatic", CreateUserID: "owner", Status: "failed", CreatedAt: time.Now().Add(-48 * time.Hour), PayloadJSON: mustJSON(readingPreparationPayload{DatasetID: "ds", DocumentID: "history"})}).Error; err != nil {
		t.Fatal(err)
	}
	cursors := make(map[string]readingEnrollmentCursor)
	for pass := 0; pass < 2; pass++ {
		if err := enrollReadingPreparation(t.Context(), db.DB, cursors); err != nil {
			t.Fatal(err)
		}
		var count int64
		db.Model(&orm.AsyncJob{}).Where("job_type=?", ReadingPreparationJob).Count(&count)
		if want := int64(pass + 1); count != want {
			t.Fatalf("pass %d jobs=%d want=%d", pass, count, want)
		}
	}
	var job orm.AsyncJob
	db.Where("id<>?", "failed-history").Take(&job)
	var payload readingPreparationPayload
	json.Unmarshal(job.PayloadJSON, &payload)
	if payload.DocumentID != "valid" {
		t.Fatalf("wrong candidate: %+v", payload)
	}
}
