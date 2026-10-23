package academic

import (
	"context"
	"os"
	"testing"
	"time"

	"lazymind/core/academic/arxivsource"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

// Opt in explicitly: this test contacts arXiv, but stores everything in a
// disposable database and temporary directory, never in the user's library.
func TestArxivLiveImport(t *testing.T) {
	if os.Getenv("LAZYMIND_TEST_ARXIV_LIVE") != "1" {
		t.Skip("requires explicit live-network opt-in")
	}
	svc, _ := arxivImportTestService(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	if err := svc.db.AutoMigrate(&orm.UploadedFile{}); err != nil {
		t.Fatal(err)
	}
	svc.lookup = arxivsource.NewClient().Lookup
	svc.acquire = acquirePDF
	svc.importFile = doc.ImportLocalFile
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result, err := svc.importPaper(ctx, "arXiv:2404.06654v3", "dataset", "user", "User")
	if err != nil {
		t.Fatal(err)
	}
	if result.Size < 1024 || result.SourceVersion != "v3" || result.WorkID == "" || result.DocumentID == "" || len(result.ContentSHA256) != 64 {
		t.Fatalf("invalid stored result: %#v", result)
	}
	restored, err := newArxivImportService(svc.db).library(ctx, "dataset")
	if err != nil || len(restored) != 1 || restored[0].DocumentID != result.DocumentID {
		t.Fatalf("restore failed: %#v %v", restored, err)
	}
	again, err := svc.importPaper(ctx, "https://arxiv.org/pdf/2404.06654v3.pdf", "dataset", "user", "User")
	if err != nil || again.Status != "already_present" || again.DocumentID != result.DocumentID {
		t.Fatalf("dedup failed: %#v %v", again, err)
	}
	t.Logf("verified title=%q version=%s bytes=%d sha256=%s; restored and reused same document", result.Paper.Title, result.SourceVersion, result.Size, result.ContentSHA256)
}
