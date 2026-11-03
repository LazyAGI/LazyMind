package academic

import (
	"context"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestArxivLiveUnifiedImport(t *testing.T) {
	if os.Getenv("LAZYMIND_TEST_ARXIV_LIVE") != "1" {
		t.Skip("explicit live-network opt-in required")
	}
	network := http.DefaultTransport
	db, b, item, svc := importPipelineFixture(t)
	scan := http.DefaultTransport
	http.DefaultTransport = sourceTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "scan.test" {
			return scan.RoundTrip(r)
		}
		return network.RoundTrip(r)
	})
	item.SelectedCandidateJSON = marshal(FulltextCandidate{URL: "https://arxiv.org/pdf/2404.06654v3", Version: "v3", SourceProvider: "arxiv", SourceType: "official_pdf"})
	db.Model(&item).Update("selected_candidate_json", item.SelectedCandidateJSON)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	job := asyncjob.Job{ID: "job", AttemptCount: 1}
	if err := processImportItem(ctx, job, b, item, sourceImportCaller(b), svc); err != nil {
		t.Fatal(err)
	}
	var first orm.PaperImportItem
	db.Where("id=?", item.ID).Take(&first)
	item.ID = "live-repeat"
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	if err := processImportItem(ctx, job, b, item, sourceImportCaller(b), svc); err != nil {
		t.Fatal(err)
	}
	var again orm.PaperImportItem
	db.Where("id=?", item.ID).Take(&again)
	var link orm.AcademicWorkDocument
	db.Where("document_id=?", first.DocumentID).Take(&link)
	var docs, tasks int64
	db.Model(&orm.Document{}).Count(&docs)
	db.Model(&orm.Task{}).Count(&tasks)
	if first.Status != "succeeded" || again.Status != "skipped" || again.DocumentID != first.DocumentID || link.SourceVersion != "v3" || len(link.ContentSHA256) != 64 || docs != 1 || tasks != 1 {
		t.Fatalf("live import or dedup failed: first=%+v again=%+v link=%+v docs=%d tasks=%d", first, again, link, docs, tasks)
	}
	t.Logf("real arXiv v3 downloaded through OriginalService; document=%s sha256=%s; duplicate reused the same document and task", first.DocumentID, link.ContentSHA256)
}
