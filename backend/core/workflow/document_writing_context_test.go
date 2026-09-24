package workflow

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func TestDocumentWritingContextsRespectSessionAndRevisionState(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.WorkflowSession{}, &orm.WorkflowSlotRevision{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	session := orm.WorkflowSession{ID: "session", CreateUserID: "owner"}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for index, name := range []string{"old", "current", "stale", "unselected", "other-session", "document"} {
		schema := "lazyllm.tools.writer.data_models.context.WritingContext"
		if name == "document" {
			schema = "lazyllm.tools.writer.data_models.writer_ir.WriterDocument"
		}
		value, err := json.Marshal(map[string]any{"schema": schema, "data": map[string]any{"context_id": name}})
		if err != nil {
			t.Fatal(err)
		}
		row := orm.WorkflowSlotRevision{
			ID: name, SessionID: session.ID, SlotID: name, Slot: name, Revision: 1,
			Selected: true, Validity: "effective", ContentSnapshot: value,
			CreatedAt: now.Add(time.Duration(index) * time.Second),
		}
		if name == "stale" {
			row.Validity = "stale"
		}
		if name == "other-session" {
			row.SessionID = "other"
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		if name == "unselected" {
			if err := db.Model(&row).Update("selected", false).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	target := &documentActionContext{db: db, owner: "owner", session: &session}
	contexts, err := documentWritingContexts(t.Context(), target)
	if err != nil || len(contexts) != 2 {
		t.Fatalf("contexts = %s, error = %v", contexts, err)
	}
	for index, want := range []string{"current", "old"} {
		var got struct {
			ContextID string `json:"context_id"`
		}
		if err := json.Unmarshal(contexts[index], &got); err != nil || got.ContextID != want {
			t.Fatalf("context %d = %s, error = %v", index, contexts[index], err)
		}
	}
	target.owner = "another-user"
	if _, err := documentWritingContexts(t.Context(), target); err == nil {
		t.Fatal("accepted another user's context")
	}
}
