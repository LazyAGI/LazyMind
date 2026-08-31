package currentmemory

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestEnsureInitializedHonorsDirectoryContentConstraint(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Exec(`
		CREATE TABLE memory_current_entries (
			user_id varchar(255) NOT NULL,
			path varchar(1024) NOT NULL,
			entry_type varchar(16) NOT NULL,
			content BLOB,
			size integer NOT NULL DEFAULT 0,
			mime varchar(128) NOT NULL DEFAULT '',
			file_type varchar(32) NOT NULL DEFAULT 'unknown',
			binary boolean NOT NULL DEFAULT false,
			created_at datetime NOT NULL,
			updated_at datetime NOT NULL,
			PRIMARY KEY (user_id, path),
			CONSTRAINT chk_memory_current_entry_content CHECK (
				(entry_type = 'file' AND content IS NOT NULL)
				OR (entry_type = 'dir' AND content IS NULL)
			)
		)
	`).Error; err != nil {
		t.Fatalf("create constrained table: %v", err)
	}

	repository := NewRepository(db)
	if err := repository.EnsureInitialized(t.Context(), "user-1"); err != nil {
		t.Fatalf("initialize current memory: %v", err)
	}

	var directoryCount int64
	if err := db.Raw(`
		SELECT COUNT(*) FROM memory_current_entries
		WHERE user_id = ? AND entry_type = 'dir' AND content IS NULL
	`, "user-1").Scan(&directoryCount).Error; err != nil {
		t.Fatalf("count directories: %v", err)
	}
	if directoryCount != 4 {
		t.Fatalf("expected 4 NULL-content directories, got %d", directoryCount)
	}

	var fileCount int64
	if err := db.Raw(`
		SELECT COUNT(*) FROM memory_current_entries
		WHERE user_id = ? AND entry_type = 'file' AND content IS NOT NULL
	`, "user-1").Scan(&fileCount).Error; err != nil {
		t.Fatalf("count files: %v", err)
	}
	if fileCount != 3 {
		t.Fatalf("expected 3 initialized files, got %d", fileCount)
	}
}
