package orm

import (
	"testing"
)

func TestSkillV2ModelsAutoMigrate(t *testing.T) {
	db := MigrateTestDB(t, &SkillV2Skill{}, &SkillV2Blob{}, &SkillV2Revision{}, &SkillV2RevisionEntry{}, &SkillV2Draft{}, &SkillV2DraftEntry{})

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

	if !db.Migrator().HasColumn(&SkillV2Blob{}, "hash") {
		t.Fatal("expected skill_blobs.hash column")
	}

	if !db.Migrator().HasColumn(&SkillV2Revision{}, "skill_id") {
		t.Fatal("expected skill_revisions.skill_id column")
	}
	if !db.Migrator().HasColumn(&SkillV2Revision{}, "revision_no") {
		t.Fatal("expected skill_revisions.revision_no column")
	}
}
