package main

import (
	"context"
	"encoding/json"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/knowledgeruntime"
	"lazymind/core/store"
	"path/filepath"
	"testing"
)

func TestKnowledgeDecisionPersistsAnswersAndDoesNotAnswerReplacement(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "decision.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&orm.ChatHistory{}, &orm.SubAgentTask{}); err != nil {
		t.Fatal(err)
	}
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	ctx := context.Background()
	p := knowledgeruntime.Prepared{ConfirmationID: "confirm", ArgumentsDigest: "digest", Snapshot: "snapshot"}
	for _, decision := range []string{"approved", "rejected"} {
		raw := json.RawMessage(`{"ask_pending":{"ask_id":"confirm","knowledge_management":{"confirmation_id":"confirm","arguments_digest":"digest","snapshot":"snapshot"}},"ask_answered":false}`)
		history := orm.ChatHistory{ID: decision, ConversationID: "conversation", Ext: raw}
		if err = db.Create(&history).Error; err != nil {
			t.Fatal(err)
		}
		execution := knowledgeruntime.Execution{UserID: "owner", ConversationID: "conversation", HistoryID: decision}
		if decision == "rejected" {
			task := orm.SubAgentTask{ID: "task", ConversationID: "conversation", TriggerHistoryID: decision, CreateUserID: "owner", InputSlots: json.RawMessage(`[]`), OutputSlots: json.RawMessage(`[]`)}
			if err = db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			execution.HistoryID, execution.TaskID = "", "task"
		}
		if err = persistKnowledgeDecisionDB(ctx, db, execution, p, decision); err != nil {
			t.Fatal(err)
		}
		if err = db.First(&history, "id = ?", decision).Error; err != nil {
			t.Fatal(err)
		}
		var ext map[string]any
		if err = json.Unmarshal(history.Ext, &ext); err != nil {
			t.Fatal(err)
		}
		want := "__ask_user_no__"
		if decision == "approved" {
			want = "__ask_user_yes__"
		}
		saved := ext["ask_saved_answers"].(map[string]any)["0"].(map[string]any)
		if ext["ask_answered"] != true || saved["value"] != want {
			t.Fatalf("wrong durable decision: %s", history.Ext)
		}
		if err = persistKnowledgeConfirmation(ctx, execution, p); err != nil {
			t.Fatal(err)
		}
		var afterDuplicate orm.ChatHistory
		if err = db.First(&afterDuplicate, "id = ?", decision).Error; err != nil {
			t.Fatal(err)
		}
		if string(afterDuplicate.Ext) != string(history.Ext) {
			t.Fatal("duplicate prepare reopened answered card")
		}
		replacement := json.RawMessage(`{"ask_pending":{"ask_id":"next","knowledge_management":{"confirmation_id":"next"}},"ask_answered":false}`)
		if err = db.Model(&history).UpdateColumn("ext", replacement).Error; err != nil {
			t.Fatal(err)
		}
		if err = persistKnowledgeDecisionDB(ctx, db, execution, p, decision); err != nil {
			t.Fatal(err)
		}
		if err = db.First(&history, "id = ?", decision).Error; err != nil {
			t.Fatal(err)
		}
		if string(history.Ext) != string(replacement) {
			t.Fatalf("old answer overwrote replacement: %s", history.Ext)
		}
	}
}
