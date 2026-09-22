package doc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"lazymind/core/common/orm"
)

func TestImportMarketFilesRetainsSuccessfulBatchAfterFileFailure(t *testing.T) {
	db := newDocumentTestDB(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	seedDocumentListDataset(t, db, "ds-market-partial", "user-1")
	if err := db.Model(&orm.Dataset{}).Where("id = ?", "ds-market-partial").Update("processing_level", ProcessingLevelStored).Error; err != nil {
		t.Fatal(err)
	}
	var ds orm.Dataset
	if err := db.Take(&ds, "id = ?", "ds-market-partial").Error; err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "small.md")
	if err := os.WriteFile(path, []byte("# Synthetic fixture\nA small document."), 0600); err != nil {
		t.Fatal(err)
	}
	files := make([]MarketImportFile, 0, 52)
	for i := 0; i < 50; i++ {
		files = append(files, MarketImportFile{LocalPath: path, DisplayName: fmt.Sprintf("sample-%d.md", i)})
	}
	files = append(files, MarketImportFile{LocalPath: filepath.Join(t.TempDir(), "missing.md"), DisplayName: "missing.md"})
	files = append(files, MarketImportFile{LocalPath: path, DisplayName: "last.md"})
	result, err := ImportMarketFiles(context.Background(), &ds, "user-1", "Test", files)
	if err != nil {
		t.Fatalf("one unreadable file must not abort the whole import: %v", err)
	}
	if result.Submitted != 51 || len(result.TaskIDs) != 51 || len(result.Failures) != 1 || result.Failures[0].Name != "missing.md" {
		t.Fatalf("wrong per-file result: %+v", result)
	}
	var count int64
	if err := db.Model(&orm.Document{}).Where("dataset_id = ? AND deleted_at IS NULL", ds.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 51 {
		t.Fatalf("retained %d documents, want 51", count)
	}
}
