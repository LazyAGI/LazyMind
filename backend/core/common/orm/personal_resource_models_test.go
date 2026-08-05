package orm

import (
	"testing"
)

func TestPersonalResourceModelsAutoMigrate(t *testing.T) {
	db := MigrateTestDB(t, &PersonalResource{}, &PersonalResourceBlob{}, &PersonalResourceRevision{}, &PersonalResourceDraft{})

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

	if !db.Migrator().HasColumn(&PersonalResource{}, "id") {
		t.Fatal("expected personal_resources.id column")
	}
	if !db.Migrator().HasColumn(&PersonalResource{}, "user_id") {
		t.Fatal("expected personal_resources.user_id column")
	}
	if !db.Migrator().HasColumn(&PersonalResource{}, "resource_type") {
		t.Fatal("expected personal_resources.resource_type column")
	}

	if !db.Migrator().HasColumn(&PersonalResourceBlob{}, "hash") {
		t.Fatal("expected personal_resource_blobs.hash column")
	}
	if !db.Migrator().HasColumn(&PersonalResourceBlob{}, "size") {
		t.Fatal("expected personal_resource_blobs.size column")
	}

	if !db.Migrator().HasColumn(&PersonalResourceRevision{}, "resource_id") {
		t.Fatal("expected personal_resource_revisions.resource_id column")
	}
	if !db.Migrator().HasColumn(&PersonalResourceRevision{}, "revision_no") {
		t.Fatal("expected personal_resource_revisions.revision_no column")
	}
}
