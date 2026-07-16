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
	migrationDir := filepath.Join(filepath.Dir(file), "..", "..", "migrations")
	up, err := os.ReadFile(filepath.Join(migrationDir, "20260714170000_expand_skill_draft_conversation_id.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(up)
	for _, table := range []string{"skill_drafts", "personal_resource_drafts"} {
		if !strings.Contains(content, "ALTER TABLE public."+table) {
			t.Fatalf("up migration does not alter %s", table)
		}
	}
	if strings.Count(content, "ALTER COLUMN conversation_id TYPE VARCHAR(128)") != 2 {
		t.Fatal("up migration must expand both draft conversation_id columns to VARCHAR(128)")
	}

	for _, model := range []any{SkillV2Draft{}, PersonalResourceDraft{}} {
		field, ok := reflect.TypeOf(model).FieldByName("ConversationID")
		if !ok || !strings.Contains(field.Tag.Get("gorm"), "type:varchar(128)") {
			t.Fatalf("%T ConversationID must use varchar(128)", model)
		}
	}
}
