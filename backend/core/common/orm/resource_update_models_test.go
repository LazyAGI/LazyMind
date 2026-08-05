package orm

import (
	"reflect"
	"testing"
	"time"
)

func TestResourceUpdateSchemaModelsAutoMigrate(t *testing.T) {
	db := MigrateTestDB(t, &Conversation{}, &ChatHistory{})
	if !db.Migrator().HasColumn(&ChatHistory{}, "tool_call_turns") {
		t.Fatal("expected chat_histories.tool_call_turns column")
	}
}

func TestDeprecatedResourceUpdateModelsNotRegisteredForDDL(t *testing.T) {
	models := AllModelsForDDL()
	for _, deprecated := range []any{
		&SkillResource{},
		&ResourceVersion{},
		&ResourceSuggestion{},
	} {
		if modelListContains(models, deprecated) {
			t.Fatalf("deprecated model %T still registered in AllModelsForDDL", deprecated)
		}
	}

	names := map[string]bool{}
	for _, name := range TableNamesForDDL() {
		names[name] = true
	}
	for _, deprecated := range []string{
		"memory_review",
		"resource_versions",
		"resource_suggestions",
		"skill_resources",
	} {
		if names[deprecated] {
			t.Fatalf("deprecated table %s still registered in TableNamesForDDL", deprecated)
		}
	}
}

func TestChatHistoryToolCallTurnsDefaultsAndRejectsNegative(t *testing.T) {
	db := MigrateTestDB(t, &ChatHistory{})

	now := time.Now()
	if err := db.Create(&ChatHistory{
		ID:             "history-default",
		Seq:            1,
		ConversationID: "conversation-1",
		TimeMixin: TimeMixin{
			CreateTime: now,
			UpdateTime: now,
		},
	}).Error; err != nil {
		t.Fatalf("create chat history with default tool_call_turns: %v", err)
	}

	var got ChatHistory
	if err := db.First(&got, "id = ?", "history-default").Error; err != nil {
		t.Fatalf("read chat history: %v", err)
	}
	if got.ToolCallTurns != 0 {
		t.Fatalf("expected default tool_call_turns=0, got %d", got.ToolCallTurns)
	}

	err := db.Create(&ChatHistory{
		ID:             "history-negative",
		Seq:            2,
		ConversationID: "conversation-1",
		ToolCallTurns:  -1,
		TimeMixin: TimeMixin{
			CreateTime: now,
			UpdateTime: now,
		},
	}).Error
	if err == nil {
		t.Fatal("expected negative tool_call_turns to fail")
	}
}

func modelListContains(models []interface{}, want any) bool {
	wantType := reflect.TypeOf(want)
	for _, model := range models {
		if reflect.TypeOf(model) == wantType {
			return true
		}
	}
	return false
}
