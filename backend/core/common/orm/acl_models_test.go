package orm

import (
	"testing"
)

func TestACLModelsAutoMigrate(t *testing.T) {
	db := MigrateTestDB(t, &VisibilityModel{}, &ACLModel{}, &KBModel{}, &ACLGroupModel{}, &UserGroupModel{})

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

	if !db.Migrator().HasColumn(&ACLModel{}, "resource_type") {
		t.Fatal("expected acl_rows.resource_type column")
	}
	if !db.Migrator().HasColumn(&ACLModel{}, "resource_id") {
		t.Fatal("expected acl_rows.resource_id column")
	}
	if !db.Migrator().HasColumn(&ACLModel{}, "permission") {
		t.Fatal("expected acl_rows.permission column")
	}

	if !db.Migrator().HasIndex(&ACLModel{}, "idx_acl_resource") {
		t.Fatal("expected idx_acl_resource index on acl_rows")
	}

	if !db.Migrator().HasColumn(&VisibilityModel{}, "level") {
		t.Fatal("expected acl_visibility.level column")
	}
	if !db.Migrator().HasColumn(&VisibilityModel{}, "resource_id") {
		t.Fatal("expected acl_visibility.resource_id column")
	}

	if !db.Migrator().HasColumn(&KBModel{}, "owner_id") {
		t.Fatal("expected acl_kbs.owner_id column")
	}
	if !db.Migrator().HasColumn(&KBModel{}, "visibility") {
		t.Fatal("expected acl_kbs.visibility column")
	}
}
