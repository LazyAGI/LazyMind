package orm

import (
	"path/filepath"
	"testing"
)

// TestSkillV2ModelsAutoMigrate verifies that Skill V2 tables are created correctly.
func TestSkillV2ModelsAutoMigrate(t *testing.T) {
	db, err := Connect(DriverSQLite, filepath.Join(t.TempDir(), "skillv2.db"))
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}

	if err := db.AutoMigrate(&SkillV2Skill{}, &SkillV2Blob{}, &SkillV2Revision{}, &SkillV2RevisionEntry{}, &SkillV2Draft{}, &SkillV2DraftEntry{}); err != nil {
		t.Fatalf("auto migrate skill v2 models: %v", err)
	}

	// Verify all tables exist.
	for _, model := range []any{
		&SkillV2Skill{},
		&SkillV2Blob{},
		&SkillV2Revision{},
		&SkillV2RevisionEntry{},
		&SkillV2Draft{},
		&SkillV2DraftEntry{},
	} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("expected table for %T to exist", model)
		}
	}

	// Verify SkillV2Skill columns and indexes.
	if !db.Migrator().HasColumn(&SkillV2Skill{}, "id") {
		t.Fatal("expected skills.id column")
	}
	if !db.Migrator().HasColumn(&SkillV2Skill{}, "owner_user_id") {
		t.Fatal("expected skills.owner_user_id column")
	}
	if !db.Migrator().HasColumn(&SkillV2Skill{}, "skill_name") {
		t.Fatal("expected skills.skill_name column")
	}
	if !db.Migrator().HasIndex(&SkillV2Skill{}, "uk_skills_owner_identity") {
		t.Fatal("expected uk_skills_owner_identity index")
	}

	// Verify SkillV2Blob hash primary key.
	if !db.Migrator().HasColumn(&SkillV2Blob{}, "hash") {
		t.Fatal("expected skill_blobs.hash column")
	}

	// Verify SkillV2Revision foreign key and indexes.
	if !db.Migrator().HasColumn(&SkillV2Revision{}, "skill_id") {
		t.Fatal("expected skill_revisions.skill_id column")
	}
	if !db.Migrator().HasColumn(&SkillV2Revision{}, "revision_no") {
		t.Fatal("expected skill_revisions.revision_no column")
	}
}
