package store

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func TestCreateHostSessionPersistsConversationBinding(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.WorkflowSession{}); err != nil {
		t.Fatal(err)
	}
	repo := New(db)
	session, created, err := repo.CreateHostSession(
		context.Background(), "owner", "session-1", "conversation-1",
		"lazymind", "conversation-1", "lazymind",
		WorkflowPackage{WorkflowID: "test-workflow", WorkflowRef: "builtin:test-workflow",
			RevisionID: "revision-1", RevisionNo: 1, TreeHash: "tree-1", GraphHash: "graph-1",
			GraphVersion: "3", CompiledGraph: []byte(`{"nodes":{}}`)},
	)
	if err != nil || !created {
		t.Fatalf("create session: created=%v err=%v", created, err)
	}
	if session.ConversationID != "conversation-1" || session.OriginRef != "conversation-1" {
		t.Fatalf("conversation binding lost: %#v", session)
	}
	if session.CreatedAt.After(time.Now().UTC()) {
		t.Fatalf("invalid creation time: %v", session.CreatedAt)
	}
}
