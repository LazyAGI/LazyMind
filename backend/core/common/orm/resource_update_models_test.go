package orm

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestResourceUpdateSchemaModelsAutoMigrate(t *testing.T) {
	db, err := Connect(DriverSQLite, filepath.Join(t.TempDir(), "resource-update.db"))
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}

	if err := db.AutoMigrate(
		&Conversation{},
		&ChatHistory{},
		&SkillResource{},
		&ResourceUpdateTask{},
		&SkillReviewSchedulerState{},
		&ConversationIdleEvent{},
		&SkillReviewResult{},
		&MemoryReviewResult{},
		&ResourceVersion{},
	); err != nil {
		t.Fatalf("auto migrate resource update models: %v", err)
	}

	for _, table := range []string{
		"resource_update_tasks",
		"skill_review_scheduler_state",
		"conversation_idle_events",
		"skill_review_results",
		"memory_review",
		"resource_versions",
	} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("expected table %s to exist", table)
		}
	}
	if !db.Migrator().HasColumn(&ChatHistory{}, "tool_call_turns") {
		t.Fatal("expected chat_histories.tool_call_turns column")
	}
}

func TestResourceUpdateModelsRegisteredForDDL(t *testing.T) {
	models := AllModelsForDDL()
	for _, want := range []any{
		&ResourceUpdateTask{},
		&SkillReviewSchedulerState{},
		&ConversationIdleEvent{},
		&SkillReviewResult{},
		&MemoryReviewResult{},
		&ResourceVersion{},
	} {
		if !modelListContains(models, want) {
			t.Fatalf("expected %T in AllModelsForDDL", want)
		}
	}

	names := map[string]bool{}
	for _, name := range TableNamesForDDL() {
		names[name] = true
	}
	for _, want := range []string{
		"resource_update_tasks",
		"skill_review_scheduler_state",
		"conversation_idle_events",
		"skill_review_results",
		"memory_review",
		"resource_versions",
	} {
		if !names[want] {
			t.Fatalf("expected %s in TableNamesForDDL", want)
		}
	}
}

func TestChatHistoryToolCallTurnsDefaultsAndRejectsNegative(t *testing.T) {
	db, err := Connect(DriverSQLite, filepath.Join(t.TempDir(), "chat-history.db"))
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}
	if err := db.AutoMigrate(&ChatHistory{}); err != nil {
		t.Fatalf("auto migrate chat history: %v", err)
	}

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

	err = db.Create(&ChatHistory{
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

func TestResourceUpdateTaskIdempotencyIndexes(t *testing.T) {
	db, err := Connect(DriverSQLite, filepath.Join(t.TempDir(), "task-indexes.db"))
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}
	if err := db.AutoMigrate(&ResourceUpdateTask{}); err != nil {
		t.Fatalf("auto migrate resource update task: %v", err)
	}

	now := time.Now()
	base := ResourceUpdateTask{
		ID:           "task-1",
		TaskType:     ResourceUpdateTaskTypeGenerateReview,
		ResourceType: ResourceUpdateResourceTypeSkill,
		UserID:       "user-1",
		TriggerType:  ResourceUpdateTriggerTypeScheduled,
		TriggerID:    "trigger-1",
		Status:       ResourceUpdateTaskStatusPending,
		NextRunAt:    now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := db.Create(&base).Error; err != nil {
		t.Fatalf("create first task: %v", err)
	}
	duplicateTrigger := base
	duplicateTrigger.ID = "task-2"
	if err := db.Create(&duplicateTrigger).Error; err == nil {
		t.Fatal("expected duplicate task trigger to fail")
	}

	activeA := ResourceUpdateTask{
		ID:             "task-3",
		TaskType:       ResourceUpdateTaskTypeAutoApplyReview,
		ResourceType:   ResourceUpdateResourceTypeSkill,
		UserID:         "user-1",
		TriggerType:    ResourceUpdateTriggerTypeReviewResult,
		TriggerID:      "skill_review_results:result-1",
		Status:         ResourceUpdateTaskStatusPending,
		ReviewResultID: "result-1",
		NextRunAt:      now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := db.Create(&activeA).Error; err != nil {
		t.Fatalf("create active auto apply task: %v", err)
	}
	activeB := activeA
	activeB.ID = "task-4"
	activeB.TriggerID = "skill:resource-1:result-1:1"
	activeB.TriggerType = ResourceUpdateTriggerTypeAutoEvoEnabled
	if err := db.Create(&activeB).Error; err == nil {
		t.Fatal("expected duplicate active auto apply task to fail")
	}
	done := activeB
	done.ID = "task-5"
	done.Status = ResourceUpdateTaskStatusDone
	if err := db.Create(&done).Error; err != nil {
		t.Fatalf("expected completed auto apply history with same result to be allowed: %v", err)
	}
}

func TestSkillResourceParentSkillNameUniqueOnlyForParents(t *testing.T) {
	db, err := Connect(DriverSQLite, filepath.Join(t.TempDir(), "skill-resource.db"))
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}
	if err := db.AutoMigrate(&SkillResource{}); err != nil {
		t.Fatalf("auto migrate skill resource: %v", err)
	}

	now := time.Now()
	parent := testSkillResource("skill-parent-1", "user-1", "ship-it", "parent", "/ship-it.md", now)
	if err := db.Create(&parent).Error; err != nil {
		t.Fatalf("create parent skill: %v", err)
	}
	duplicateParent := testSkillResource("skill-parent-2", "user-1", "ship-it", "parent", "/ship-it-copy.md", now)
	if err := db.Create(&duplicateParent).Error; err == nil {
		t.Fatal("expected duplicate parent skill name to fail")
	}
	child := testSkillResource("skill-child-1", "user-1", "ship-it", "child", "/ship-it/child.md", now)
	if err := db.Create(&child).Error; err != nil {
		t.Fatalf("expected child skill to allow duplicate skill name: %v", err)
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

func testSkillResource(id, ownerID, skillName, nodeType, relativePath string, now time.Time) SkillResource {
	return SkillResource{
		ID:              id,
		OwnerUserID:     ownerID,
		OwnerUserName:   "User",
		Category:        "system",
		ParentSkillName: "",
		SkillName:       skillName,
		NodeType:        nodeType,
		RelativePath:    relativePath,
		CreateUserID:    ownerID,
		CreateUserName:  "User",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}
