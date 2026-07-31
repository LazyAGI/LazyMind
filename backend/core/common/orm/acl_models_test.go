package orm

import (
	"path/filepath"
	"testing"
)

// TestACLModelsAutoMigrate verifies that all ACL-related tables are created with the expected schema.
func TestACLModelsAutoMigrate(t *testing.T) {
	db, err := Connect(DriverSQLite, filepath.Join(t.TempDir(), "acl.db"))
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}

	if err := db.AutoMigrate(&VisibilityModel{}, &ACLModel{}, &KBModel{}, &ACLGroupModel{}, &UserGroupModel{}); err != nil {
		t.Fatalf("auto migrate acl models: %v", err)
	}

	// Verify tables exist.
	for _, model := range []any{
		&VisibilityModel{},
		&ACLModel{},
		&KBModel{},
		&ACLGroupModel{},
		&UserGroupModel{},
	} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("expected table for %T to exist", model)
		}
	}

	// Verify columns on ACLModel.
	if !db.Migrator().HasColumn(&ACLModel{}, "resource_type") {
		t.Fatal("expected acl_rows.resource_type column")
	}
	if !db.Migrator().HasColumn(&ACLModel{}, "resource_id") {
		t.Fatal("expected acl_rows.resource_id column")
	}
	if !db.Migrator().HasColumn(&ACLModel{}, "permission") {
		t.Fatal("expected acl_rows.permission column")
	}

	// Verify composite index on ACLModel.
	if !db.Migrator().HasIndex(&ACLModel{}, "idx_acl_resource") {
		t.Fatal("expected idx_acl_resource index on acl_rows")
	}

	// Verify VisibilityModel columns.
	if !db.Migrator().HasColumn(&VisibilityModel{}, "level") {
		t.Fatal("expected acl_visibility.level column")
	}
	if !db.Migrator().HasColumn(&VisibilityModel{}, "resource_id") {
		t.Fatal("expected acl_visibility.resource_id column")
	}

	// Verify KBModel primary key and columns.
	if !db.Migrator().HasColumn(&KBModel{}, "owner_id") {
		t.Fatal("expected acl_kbs.owner_id column")
	}
	if !db.Migrator().HasColumn(&KBModel{}, "visibility") {
		t.Fatal("expected acl_kbs.visibility column")
	}
}
