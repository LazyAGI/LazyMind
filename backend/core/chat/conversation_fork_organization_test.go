package chat

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/conversationgroup"
	"lazymind/core/doc"
	"lazymind/core/store"
)

func forkOrganizationRequest(t *testing.T, db *gorm.DB, id string) forkCreateRequest {
	t.Helper()
	var histories []orm.ChatHistory
	if err := db.Where("conversation_id=?", id).Order("seq ASC").Find(&histories).Error; err != nil {
		t.Fatal(err)
	}
	if len(histories) == 0 {
		t.Fatal("missing branch history")
	}
	revision, err := forkPrefixRevision(histories)
	if err != nil {
		t.Fatal(err)
	}
	return forkCreateRequest{SourceHistoryID: histories[len(histories)-1].ID, ExpectedPrefixRevision: revision}
}

func TestForkNestedBranchesShareNumberingAndPreserveLiteralTitleSuffix(t *testing.T) {
	db, source, _, request := forkFixture(t, 1)
	if err := db.Model(&source).UpdateColumns(map[string]any{"display_name": "Plan（2026）", "title_source": "user"}).Error; err != nil {
		t.Fatal(err)
	}
	caller := doc.DatasetCatalogCaller{UserID: "u1"}
	first, err := createConversationFork(t.Context(), db, caller, source.ID, "first", request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := createConversationFork(t.Context(), db, caller, source.ID, "second", request)
	if err != nil {
		t.Fatal(err)
	}
	firstID := first.Conversation["conversation_id"].(string)
	third, err := createConversationFork(t.Context(), db, caller, firstID, "third", forkOrganizationRequest(t, db, firstID))
	if err != nil {
		t.Fatal(err)
	}
	for i, result := range []*forkResult{first, second, third} {
		want := []string{"Plan（2026）（1）", "Plan（2026）（2）", "Plan（2026）（3）"}[i]
		if result.Conversation["display_name"] != want {
			t.Fatalf("name=%v, want %s", result.Conversation["display_name"], want)
		}
	}
	if err := db.Where("id=?", source.ID).Delete(&orm.Conversation{}).Error; err != nil {
		t.Fatal(err)
	}
	thirdID := third.Conversation["conversation_id"].(string)
	fourth, err := createConversationFork(t.Context(), db, caller, thirdID, "fourth", forkOrganizationRequest(t, db, thirdID))
	if err != nil {
		t.Fatal(err)
	}
	if fourth.Conversation["display_name"] != "Plan（2026）（4）" {
		t.Fatalf("deleted-root name=%v", fourth.Conversation["display_name"])
	}
}

func TestForkNumberingSurvivesPermanentDeletionOfIntermediateAndLeafBranches(t *testing.T) {
	db, source, _, request := forkFixture(t, 1)
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", t.TempDir())
	t.Setenv("LAZYMIND_AGENTIC_WORKSPACE", t.TempDir())
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	caller := doc.DatasetCatalogCaller{UserID: "u1"}
	create := func(sourceID, key, title string) string {
		t.Helper()
		result, err := createConversationFork(t.Context(), db, caller, sourceID, key, forkOrganizationRequest(t, db, sourceID))
		if err != nil {
			t.Fatal(err)
		}
		if result.Conversation["display_name"] != title {
			t.Fatalf("name=%v, want %s", result.Conversation["display_name"], title)
		}
		return result.Conversation["conversation_id"].(string)
	}
	purge := func(id string) {
		t.Helper()
		if err := archiveConversation(t.Context(), db, id, "u1"); err != nil {
			t.Fatal(err)
		}
		if err := purgeConversation(db, id, "u1"); err != nil {
			t.Fatal(err)
		}
		for _, model := range []any{&orm.Conversation{}, &orm.ChatHistory{}, &orm.ConversationArtifact{}} {
			column := "conversation_id"
			if _, ok := model.(*orm.Conversation); ok {
				column = "id"
			}
			var count int64
			if err := db.Model(model).Where(column+"=?", id).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("purged content retained in %T: count=%d error=%v", model, count, err)
			}
		}
	}
	first := create(source.ID, "first", "Source（1）")
	second := create(first, "second", "Source（2）")
	// This branch-only content was never copied into the surviving branch.
	privateHistory := sidechatTestHistory(t, db, "private-history", first, 2, "private branch input", "private branch response", "completed")
	if err := db.Create(&orm.ConversationArtifact{ID: "private-artifact", ConversationID: first, HistoryID: privateHistory.ID,
		Filename: "private.txt", ContentType: "text", Value: json.RawMessage(`{"text":"private branch artifact"}`), CreateUserID: "u1"}).Error; err != nil {
		t.Fatal(err)
	}
	artifactRoot := conversationArtifactConversationRoot("u1", first)
	if err := os.MkdirAll(artifactRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactRoot, "private.txt"), []byte("private branch file"), 0o600); err != nil {
		t.Fatal(err)
	}
	purge(first)
	if _, err := os.Stat(artifactRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("purged branch files remain: %v", err)
	}
	var branch orm.Conversation
	if err := db.Where("id=?", second).Take(&branch).Error; err != nil {
		t.Fatal(err)
	}
	origin, err := loadForkOrigin(t.Context(), db, branch)
	if err != nil || origin == nil || origin.SourceStatus != "unavailable" || origin.CanLocate || origin.SourceTitleSnapshot != "" {
		t.Fatalf("purged source remained visible: %+v %v", origin, err)
	}
	third := create(second, "third", "Source（3）")
	create(source.ID, "fourth", "Source（4）")
	purge(third)
	create(second, "fifth", "Source（5）")
	purge(source.ID)
	create(second, "sixth", "Source（6）")
	_, err = createConversationFork(t.Context(), db, caller, source.ID, "first", request)
	var problem *forkError
	if !errors.As(err, &problem) || problem.Code != "FORK_RESULT_UNAVAILABLE" {
		t.Fatalf("purged branch recreated by replay: %v", err)
	}
}

func TestForkInheritsOrdinaryGroupAndKeepsIndependentLifecycle(t *testing.T) {
	db, source, _, request := forkFixture(t, 1)
	group := orm.ConversationGroup{ID: "ordinary-group", UserID: "u1", Name: "Group", NormalizedName: "group"}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	if err := conversationgroup.UserTransaction(t.Context(), db, "u1", func(tx *gorm.DB) error {
		return conversationgroup.AttachNewConversation(t.Context(), tx, "u1", source.ID, group.ID)
	}); err != nil {
		t.Fatal(err)
	}
	result, err := createConversationFork(t.Context(), db, doc.DatasetCatalogCaller{UserID: "u1"}, source.ID, "grouped-fork", request)
	if err != nil {
		t.Fatal(err)
	}
	id := result.Conversation["conversation_id"].(string)
	var member orm.ConversationGroupMember
	if err := db.Where("conversation_id=? AND user_id=?", id, "u1").Take(&member).Error; err != nil {
		t.Fatal(err)
	}
	if member.GroupID != group.ID {
		t.Fatalf("group=%s, want %s", member.GroupID, group.ID)
	}
	var state orm.ConversationGroupState
	if err := db.Where("conversation_id=?", id).Take(&state).Error; err != nil || state.GroupID == nil || *state.GroupID != group.ID {
		t.Fatalf("missing durable membership state: %+v %v", state, err)
	}
	var branch orm.Conversation
	if err := db.Where("id=?", id).Take(&branch).Error; err != nil {
		t.Fatal(err)
	}
	if branch.ParentConversationID != nil {
		t.Fatal("fork became a dependent child")
	}
}
