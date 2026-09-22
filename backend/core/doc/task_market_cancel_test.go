package doc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func TestMarketControlCanceledImportDoesNotRegisterOrSubmitFiles(t *testing.T) {
	db := newDocumentTestDB(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	seedDocumentListDataset(t, db, "ds-control", "user-1")
	if err := db.Model(&orm.Dataset{}).Where("id = ?", "ds-control").Update("processing_level", ProcessingLevelStored).Error; err != nil {
		t.Fatal(err)
	}
	var ds orm.Dataset
	if err := db.Take(&ds, "id = ?", "ds-control").Error; err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fixture.md")
	if err := os.WriteFile(path, []byte("# Synthetic input"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := ImportMarketFiles(ctx, &ds, "user-1", "Fixture", []MarketImportFile{{LocalPath: path, DisplayName: "fixture.md"}})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("canceled import reported success or ordinary file failure: result=%+v error=%v", result, err)
	}
	var count int64
	if err := db.Model(&orm.Document{}).Where("dataset_id = ?", ds.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("canceled import created %d documents", count)
	}
}

func TestMarketControlStopDuringImportPreservesCompletedFile(t *testing.T) {
	db := newDocumentTestDB(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	seedDocumentListDataset(t, db, "ds-control-mid", "user-1")
	if err := db.Model(&orm.Dataset{}).Where("id = ?", "ds-control-mid").Update("processing_level", ProcessingLevelStored).Error; err != nil {
		t.Fatal(err)
	}
	var ds orm.Dataset
	if err := db.Take(&ds, "id = ?", "ds-control-mid").Error; err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fixture.md")
	if err := os.WriteFile(path, []byte("# Synthetic input"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Synchronize at the first committed stored-file result. This exercises
	// Go's import interruption only, not Algorithm execution or vector writes.
	if err := db.Callback().Update().After("gorm:commit_or_rollback_transaction").Register("test:stop-after-file-commit", func(tx *gorm.DB) {
		if tx.Error == nil && tx.Statement.Table == "tasks" {
			cancel()
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Update().Remove("test:stop-after-file-commit") })
	files := make([]MarketImportFile, 51)
	for i := range files {
		files[i] = MarketImportFile{LocalPath: path, DisplayName: fmt.Sprintf("fixture-%d.md", i)}
	}
	result, err := ImportMarketFiles(ctx, &ds, "user-1", "Fixture", files)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("mid-import stop was not reported: result=%+v error=%v", result, err)
	}
	var tasks []orm.Task
	if err := db.Where("dataset_id = ?", ds.ID).Find(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	completed := 0
	for _, task := range tasks {
		var ext taskExt
		if err := json.Unmarshal(task.Ext, &ext); err != nil {
			t.Fatal(err)
		}
		if ext.TaskState == string(TaskStateSucceeded) {
			completed++
			var document orm.Document
			if err := db.Where("id = ? AND deleted_at IS NULL", task.DocID).Take(&document).Error; err != nil {
				t.Fatalf("completed document removed: %v", err)
			}
		}
	}
	if completed != 1 {
		t.Fatalf("completed %d files, want exactly the one committed before stop", completed)
	}
}
