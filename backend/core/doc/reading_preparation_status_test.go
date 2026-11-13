package doc

import (
	"errors"
	"fmt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/pdfir"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadingCapabilityAggregation(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		ready                bool
		total, done, running int
		job                  string
		want                 string
	}{
		{"unparsed is not empty", false, 0, 0, 0, "", "not_requested"},
		{"parsed empty", true, 0, 0, 0, "", "empty"},
		{"cache hit without batch", true, 2, 2, 0, "", "succeeded"},
		{"limit does not complete capability", true, 5, 2, 0, "succeeded", "partial"},
		{"standalone active", true, 2, 0, 1, "", "running"},
		{"queued", true, 2, 0, 0, "pending", "pending"},
		{"batch running", true, 2, 0, 0, "running", "running"},
		{"failed partial", true, 2, 1, 0, "failed", "partial"},
		{"failed", true, 2, 0, 0, "failed", "failed"},
		{"all cached despite failed job", true, 2, 2, 0, "failed", "succeeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := readingCapabilityStatus{ID: "formula", Total: tc.total, Completed: tc.done, Running: tc.running}
			var jobs []orm.AsyncJob
			if tc.job != "" {
				jobs = []orm.AsyncJob{{Status: tc.job, PayloadJSON: mustJSON(readingPreparationPayload{Scopes: []string{"formula"}})}}
			}
			finalizeReadingCapability(&c, tc.ready, jobs)
			if c.Status != tc.want {
				t.Fatalf("got %+v want %s", c, tc.want)
			}
		})
	}
}
func TestReadingCapabilityUnknownStructure(t *testing.T) {
	got, err := readReadingCapabilities(pdfIRResponse{Status: "pending"}, "/unused", "dataset", "document", "user", nil)
	if err != nil || len(got) != 3 {
		t.Fatalf("%+v %v", got, err)
	}
	for _, c := range got {
		if c.Status == "empty" {
			t.Fatal("unparsed structure reported empty")
		}
	}
	got, err = readReadingCapabilities(pdfIRResponse{Status: "ready", Document: &pdfir.Document{Version: 1, CoordinateSpace: "normalized_top_left"}}, "/unused", "dataset", "document", "user", nil)
	if err != nil || got[0].Status != "empty" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestReadingCapabilityCachesAreCurrentValidAndUserScoped(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source.pdf")
	d := pdfir.Document{Version: 1, CoordinateSpace: "normalized_top_left", SourceSHA256: "current", Blocks: []pdfir.Block{{ID: "f", Type: "formula", Latex: "x"}, {ID: "v", Type: "image", Image: &pdfir.Image{Captions: []string{"Figure"}}}, {ID: "t", Type: "table", Table: &pdfir.Table{Captions: []string{"Table"}}}}}
	state := pdfIRResponse{Status: "ready", Document: &d}
	formula, ev, err := formulaExplanationContext(d, "f")
	if err != nil {
		t.Fatal(err)
	}
	formulaDir := readingArtifactDir(source, "ds", "doc", "alice", "formula-explanations")
	for _, lang := range []string{"zh", "en"} {
		key := formulaExplanationCacheKey(d, formula.ID, formula.Latex, "saved-explanation:"+lang, ev)
		if err := savePaperReading(formulaDir, filepath.Join(formulaDir, key+".json"), map[string]any{"summary": "Saved from an older selected model", "model": "old-model"}); err != nil {
			t.Fatal(err)
		}
	}
	_, ev, err = paperVisualContext(d, "v")
	if err != nil {
		t.Fatal(err)
	}
	visualDir := readingArtifactDir(source, "ds", "doc", "alice", "paper-reading")
	visualPath := filepath.Join(visualDir, paperVisualCacheKey(d, "v", "en", ev)+".json")
	if err := savePaperReading(visualDir, visualPath, map[string]any{"summary": "A saved image reading"}); err != nil {
		t.Fatal(err)
	}
	_, ev, err = paperVisualContext(d, "t")
	if err != nil {
		t.Fatal(err)
	}
	tablePath := filepath.Join(visualDir, paperVisualCacheKey(d, "t", "zh", ev)+".json")
	if err := os.WriteFile(tablePath, []byte("{invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readReadingCapabilities(state, source, "ds", "doc", "alice", nil)
	if err != nil || got[0].Completed != 1 || got[1].Completed != 1 || got[2].Completed != 0 {
		t.Fatalf("counts: %+v %v", got, err)
	}
	for _, scope := range []struct{ user, doc string }{{"bob", "doc"}, {"alice", "other"}} {
		got, err = readReadingCapabilities(state, source, "ds", scope.doc, scope.user, nil)
		if err != nil || got[0].Completed != 0 || got[1].Completed != 0 {
			t.Fatalf("cache isolation: %+v %v", got, err)
		}
	}
	d.SourceSHA256 = "replacement"
	got, err = readReadingCapabilities(state, source, "ds", "doc", "alice", nil)
	if err != nil || got[0].Completed != 0 || got[1].Completed != 0 {
		t.Fatalf("stale cache: %+v %v", got, err)
	}
	d.SourceSHA256 = "current"
	finish := startReadingWork(tablePath)
	got, err = readReadingCapabilities(state, source, "ds", "doc", "alice", nil)
	if err != nil || got[2].Status != "running" || got[2].Running != 1 {
		t.Fatalf("live standalone work: %+v %v", got, err)
	}
	finish(errors.New("failed"))
	got, err = readReadingCapabilities(state, source, "ds", "doc", "alice", nil)
	if err != nil || got[2].Status != "failed" || got[2].Failed != 1 {
		t.Fatalf("failed standalone work: %+v %v", got, err)
	}
	finish = startReadingWork(tablePath)
	finish(nil)
}

func TestReadingCapabilityActiveJobNotHiddenByNewerJob(t *testing.T) {
	jobs := []orm.AsyncJob{{Status: "succeeded", PayloadJSON: mustJSON(readingPreparationPayload{Scopes: []string{"figure"}})}, {Status: "running", PayloadJSON: mustJSON(readingPreparationPayload{Scopes: []string{"formula"}})}}
	c := readingCapabilityStatus{ID: "formula", Total: 3}
	finalizeReadingCapability(&c, true, jobs)
	if c.Status != "running" {
		t.Fatalf("%+v", c)
	}
}

func TestReadingCapabilityJobHistoryKeepsLatestPerScopeAndActive(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.AsyncJob{}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		scopes := []string{"figure"}
		status := "succeeded"
		if i == 0 {
			scopes = []string{"formula"}
		}
		if i == 1 {
			scopes = []string{"table"}
			status = "running"
		}
		job := orm.AsyncJob{ID: fmt.Sprintf("history-%02d", i), JobType: ReadingPreparationJob, CreateUserID: "owner", Status: status, CreatedAt: time.Now().Add(time.Duration(i) * time.Second), PayloadJSON: mustJSON(readingPreparationPayload{DatasetID: "ds", DocumentID: "doc", SourceSHA256: "hash", Scopes: scopes})}
		if err := db.Create(&job).Error; err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := loadReadingPreparationJobs(t.Context(), db.DB, orm.Document{DatasetID: "ds", ID: "doc"}, "owner", "hash")
	if err != nil || len(jobs) != 3 || jobs[0].ID != "history-19" {
		t.Fatalf("bounded history: %+v %v", jobs, err)
	}
	for _, scope := range []string{"formula", "figure", "table"} {
		c := readingCapabilityStatus{ID: scope, Total: 3}
		finalizeReadingCapability(&c, true, jobs)
		want := "partial"
		if scope == "table" {
			want = "running"
		}
		if c.Status != want {
			t.Fatalf("%s: %+v", scope, c)
		}
	}
	c := readingCapabilityStatus{ID: "formula", Total: 2, Completed: 1, Failed: 1}
	finalizeReadingCapability(&c, true, nil)
	if c.Status != "partial" || c.ErrorMessage == "" {
		t.Fatalf("failed partial: %+v", c)
	}
}

func TestReadingCapabilityPostgresJSONScopesAreCastToJSONB(t *testing.T) {
	// DryRun plus disabled ping verifies production SQL without a database server.
	db, err := gorm.Open(postgres.Open("host=127.0.0.1 user=unused dbname=unused sslmode=disable"), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	var queries []string
	if err := db.Callback().Query().After("gorm:query").Register("capture_reading_status_sql", func(tx *gorm.DB) { queries = append(queries, tx.Statement.SQL.String()) }); err != nil {
		t.Fatal(err)
	}
	if _, err := loadReadingPreparationJobs(t.Context(), db, orm.Document{DatasetID: "ds", ID: "doc"}, "user", "hash"); err != nil {
		t.Fatal(err)
	}
	scoped := 0
	for _, query := range queries {
		if strings.Contains(query, "@>") {
			scoped++
			if !strings.Contains(query, "(payload_json -> 'scopes')::jsonb @>") {
				t.Fatalf("JSON payload scopes require a JSONB left operand: %s", query)
			}
		}
	}
	if scoped != 3 {
		t.Fatalf("expected three scope queries, got %d: %v", scoped, queries)
	}
}
