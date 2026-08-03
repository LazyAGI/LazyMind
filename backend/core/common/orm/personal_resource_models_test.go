package orm

import (
	"path/filepath"
	"testing"
)

// TestPersonalResourceModelsAutoMigrate verifies that personal resource tables are created correctly.
func TestPersonalResourceModelsAutoMigrate(t *testing.T) {
	db, err := Connect(DriverSQLite, filepath.Join(t.TempDir(), "personal-resource.db"))
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}

	if err := db.AutoMigrate(&PersonalResource{}, &PersonalResourceBlob{}, &PersonalResourceRevision{}, &PersonalResourceDraft{}); err != nil {
		t.Fatalf("auto migrate personal resource models: %v", err)
	}

	// Verify all tables exist.
	for _, model := range []any{
		&PersonalResource{},
		&PersonalResourceBlob{},
		&PersonalResourceRevision{},
		&PersonalResourceDraft{},
	} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("expected table for %T to exist", model)
		}
	}

	// Verify PersonalResource key columns.
	if !db.Migrator().HasColumn(&PersonalResource{}, "id") {
		t.Fatal("expected personal_resources.id column")
	}
	if !db.Migrator().HasColumn(&PersonalResource{}, "user_id") {
		t.Fatal("expected personal_resources.user_id column")
	}
	if !db.Migrator().HasColumn(&PersonalResource{}, "resource_type") {
		t.Fatal("expected personal_resources.resource_type column")
	}

	// Verify PersonalResourceBlob hash primary key.
	if !db.Migrator().HasColumn(&PersonalResourceBlob{}, "hash") {
		t.Fatal("expected personal_resource_blobs.hash column")
	}
	if !db.Migrator().HasColumn(&PersonalResourceBlob{}, "size") {
		t.Fatal("expected personal_resource_blobs.size column")
	}

	// Verify PersonalResourceRevision columns.
	if !db.Migrator().HasColumn(&PersonalResourceRevision{}, "resource_id") {
		t.Fatal("expected personal_resource_revisions.resource_id column")
	}
	if !db.Migrator().HasColumn(&PersonalResourceRevision{}, "revision_no") {
		t.Fatal("expected personal_resource_revisions.revision_no column")
	}
}
