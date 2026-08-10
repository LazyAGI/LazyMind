package orm

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestSkillDraftConversationIDMigrationContract(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	migrationDir := filepath.Join(filepath.Dir(file), "..", "..", "migrations", "dev_mode", "v0_2")
	up, err := os.ReadFile(filepath.Join(migrationDir, "20260714170000_expand_skill_draft_conversation_id.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(up)
	if !strings.Contains(content, "ALTER TABLE public.skill_drafts") {
		t.Fatal("up migration does not alter skill_drafts")
	}
	if !strings.Contains(content, "ALTER COLUMN conversation_id TYPE VARCHAR(128)") {
		t.Fatal("up migration must expand skill draft conversation_id to VARCHAR(128)")
	}

	model := SkillV2Draft{}
	field, ok := reflect.TypeOf(model).FieldByName("ConversationID")
	if !ok || !strings.Contains(field.Tag.Get("gorm"), "type:varchar(128)") {
		t.Fatalf("%T ConversationID must use varchar(128)", model)
	}
}
