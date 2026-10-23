package chat

import (
	"encoding/json"
	"fmt"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestConversationSelectionSnapshotIsCompleteAndScoped(t *testing.T) {
	db := newConversationTestDB(t).DB
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	for i := 0; i < 51; i++ {
		c := orm.Conversation{ID: fmt.Sprintf("root-%02d", i), DisplayName: "report", BaseModel: orm.BaseModel{CreateUserID: "u1", UpdatedAt: now.Add(-time.Duration(i) * time.Minute)}}
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
	}
	parent := "root-00"
	extras := []orm.Conversation{
		{ID: "fork", DisplayName: "report", BaseModel: orm.BaseModel{CreateUserID: "u1"}},
		{ID: "sidechat", DisplayName: "report", ParentConversationID: &parent, RelationType: "sidechat", BaseModel: orm.BaseModel{CreateUserID: "u1"}},
		{ID: "foreign", DisplayName: "report", BaseModel: orm.BaseModel{CreateUserID: "u2"}},
		{ID: "task", DisplayName: "report", IsTaskConv: true, BaseModel: orm.BaseModel{CreateUserID: "u1"}},
		{ID: "ephemeral", DisplayName: "report", IsEphemeral: true, BaseModel: orm.BaseModel{CreateUserID: "u1"}},
		{ID: "archived", DisplayName: "report", ArchivedAt: &now, BaseModel: orm.BaseModel{CreateUserID: "u1"}},
		{ID: "deleted", DisplayName: "report", BaseModel: orm.BaseModel{CreateUserID: "u1", DeletedAt: &now}},
		{ID: "hidden", DisplayName: "report", BaseModel: orm.BaseModel{CreateUserID: "u1"}},
	}
	for _, c := range extras {
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&orm.ConversationForkOrigin{ConversationID: "fork", SourceConversationID: parent, ForkedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ExternalAgentBinding{ID: "hidden-binding", ConversationID: "hidden", Provider: "codex", HostID: "host", ProviderThreadID: "hidden", CreatedByUserID: "u1"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ConversationGroup{ID: "g", UserID: "u1", Name: "ProjectNeedle", NormalizedName: "projectneedle"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"root-00", "root-01"} {
		if err := db.Create(&orm.ConversationGroupMember{ConversationID: id, GroupID: "g", UserID: "u1"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&orm.Conversation{}).Where("id=?", "root-01").UpdateColumn("pinned_at", now).Error; err != nil {
		t.Fatal(err)
	}
	run := orm.ConversationOrganizerRun{ID: "run", UserID: "u1", Status: "running", SnapshotJSON: json.RawMessage(`{}`), ModelConfigJSON: json.RawMessage(`{}`)}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ConversationOrganizerSnapshotItem{RunID: "run", ConversationID: "sidechat", UserID: "u1"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ConversationGroup{ID: "project", UserID: "u1", Name: "Project", NormalizedName: "project", Kind: "project"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ConversationGroupMember{ConversationID: "root-02", GroupID: "project", UserID: "u1"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ConversationOrganizerSnapshotItem{RunID: "run", ConversationID: "root-02", UserID: "u1"}).Error; err != nil {
		t.Fatal(err)
	}
	fetch := func(userID, query string) []map[string]any {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/?selection_snapshot=true&is_task_conv=false&page_size=1&page_token=50&"+query, nil)
		r.Header.Set("X-User-Id", userID)
		w := httptest.NewRecorder()
		ListConversations(w, r)
		var data struct {
			Conversations []map[string]any `json:"conversations"`
			Complete      bool             `json:"selection_complete"`
			Next          string           `json:"next_page_token"`
			Total         int              `json:"total_size"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &data) != nil {
			t.Fatalf("snapshot: %d %s", w.Code, w.Body.String())
		}
		if !data.Complete || data.Next != "" || data.Total != len(data.Conversations) {
			t.Fatalf("incomplete snapshot: %s", w.Body.String())
		}
		return data.Conversations
	}
	// Reordering an item that was beyond the first history page cannot omit it.
	if err := db.Model(&orm.Conversation{}).Where("id=?", "root-50").UpdateColumn("updated_at", now.Add(time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	rows := fetch("u1", "keyword=report")
	ids := map[string]bool{}
	for _, row := range rows {
		id := row["conversation_id"].(string)
		if ids[id] {
			t.Fatalf("duplicate %s", id)
		}
		ids[id] = true
		if _, exists := row["search_config"]; exists {
			t.Fatal("selection loaded full conversation payload")
		}
		if id == "root-00" && (row["group_id"] != "g" || row["organizing_run_id"] != "run") {
			t.Fatalf("missing placement/family lock: %v", row)
		}
		if id == "root-02" && row["organizing_run_id"] != nil {
			t.Fatalf("project lock exemption changed: %v", row)
		}
	}
	if len(ids) != 52 || !ids["root-50"] || !ids["fork"] {
		t.Fatalf("snapshot scope: %v", ids)
	}
	if rows := fetch("u1", "keyword=ProjectNeedle&selection_group_id=g&assistants=lazymind"); len(rows) != 2 {
		t.Fatalf("group keyword scope: %v", rows)
	}
	if rows := fetch("u1", "keyword=ProjectNeedle&selection_group_id=g&assistants=codex"); len(rows) != 0 {
		t.Fatalf("source intersection: %v", rows)
	}
	if err := db.Create(&orm.ConversationGroup{ID: "foreign-group", UserID: "u2", Name: "Foreign", NormalizedName: "foreign"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ConversationGroupMember{ConversationID: "foreign", GroupID: "foreign-group", UserID: "u2"}).Error; err != nil {
		t.Fatal(err)
	}
	if rows := fetch("u1", "selection_group_id=foreign-group"); len(rows) != 0 {
		t.Fatalf("foreign group: %v", rows)
	}
	if rows := fetch("u2", "selection_group_id=foreign-group"); len(rows) != 1 || rows[0]["conversation_id"] != "foreign" {
		t.Fatalf("owner's group: %v", rows)
	}
	if rows := fetch("u1", "selection_group_id=unknown-group"); len(rows) != 0 {
		t.Fatalf("unknown group: %v", rows)
	}
}

func TestConversationSelectionSnapshotAggregatesActiveFamilyLocks(t *testing.T) {
	db := newConversationTestDB(t).DB
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	rootID := "root"
	for _, conversation := range []orm.Conversation{
		{ID: rootID, BaseModel: orm.BaseModel{CreateUserID: "u1"}},
		{ID: "child", ParentConversationID: &rootID, RelationType: "sidechat", BaseModel: orm.BaseModel{CreateUserID: "u1"}},
		{ID: "legacy-fork", ParentConversationID: &rootID, RelationType: "fork", BaseModel: orm.BaseModel{CreateUserID: "u1"}},
		{ID: "fork", BaseModel: orm.BaseModel{CreateUserID: "u1"}},
		{ID: "completed", BaseModel: orm.BaseModel{CreateUserID: "u1"}},
		{ID: "unlocked", BaseModel: orm.BaseModel{CreateUserID: "u1"}},
		{ID: "foreign", ParentConversationID: &rootID, RelationType: "sidechat", BaseModel: orm.BaseModel{CreateUserID: "u2"}},
	} {
		if err := db.Create(&conversation).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&orm.ConversationForkOrigin{ConversationID: "fork", SourceConversationID: rootID, ForkedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct{ runID, status, userID, conversationID string }{
		{"a-finished", "succeeded", "u1", rootID},
		{"b-child", "pending", "u1", "child"},
		{"c-root", "applying", "u1", rootID},
		{"a-fork", "running", "u1", "fork"},
		{"0-legacy-fork", "running", "u1", "legacy-fork"},
		{"0-foreign", "running", "u2", "foreign"},
		{"a-completed", "confirmed", "u1", "completed"},
	} {
		run := orm.ConversationOrganizerRun{ID: fixture.runID, UserID: fixture.userID, Status: fixture.status, SnapshotJSON: json.RawMessage(`{}`), ModelConfigJSON: json.RawMessage(`{}`)}
		if err := db.Create(&run).Error; err != nil {
			t.Fatal(err)
		}
		item := orm.ConversationOrganizerSnapshotItem{RunID: fixture.runID, UserID: fixture.userID, ConversationID: fixture.conversationID}
		if err := db.Create(&item).Error; err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/?selection_snapshot=true&is_task_conv=false", nil)
	r.Header.Set("X-User-Id", "u1")
	w := httptest.NewRecorder()
	ListConversations(w, r)
	var result struct {
		Conversations []struct {
			ID    string  `json:"conversation_id"`
			RunID *string `json:"organizing_run_id"`
		} `json:"conversations"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatalf("snapshot: %d %s", w.Code, w.Body.String())
	}
	expected := map[string]string{rootID: "b-child", "fork": "a-fork", "completed": "", "unlocked": ""}
	if len(result.Conversations) != len(expected) {
		t.Fatalf("snapshot root count: %s", w.Body.String())
	}
	for _, conversation := range result.Conversations {
		want, exists := expected[conversation.ID]
		got := ""
		if conversation.RunID != nil {
			got = *conversation.RunID
		}
		if !exists || got != want {
			t.Fatalf("family lock for %s: got %q want %q", conversation.ID, got, want)
		}
	}
}

func TestConversationArchiveReturnsAffectedFamilyIDs(t *testing.T) {
	db := newChatExecutionTestDB(t).DB
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	root := sidechatTestConversation(t, db, "parent", "u1", "Parent")
	child := sidechatTestConversation(t, db, "child", "u1", "Child")
	sidechatTestConversation(t, db, "independent-fork", "u1", "Fork")
	if err := db.Create(&orm.ConversationForkOrigin{ConversationID: "independent-fork", SourceConversationID: root.ID, ForkedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&child).Updates(map[string]any{"parent_conversation_id": root.ID, "relation_type": "sidechat"}).Error; err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	ArchiveConversation(w, sidechatRequest(http.MethodPost, "/", "u1", `{}`, map[string]string{"conversation_id": root.ID}))
	var result struct {
		IDs []string `json:"archived_ids"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatalf("archive: %d %s", w.Code, w.Body.String())
	}
	ids := map[string]bool{}
	for _, id := range result.IDs {
		ids[id] = true
	}
	if len(ids) != 2 || !ids[root.ID] || !ids[child.ID] {
		t.Fatalf("affected ids: %v", result.IDs)
	}
	var archived int64
	if err := db.Model(&orm.Conversation{}).Where("id IN ? AND archived_at IS NOT NULL", result.IDs).Count(&archived).Error; err != nil || archived != 2 {
		t.Fatalf("affected family was not archived: count=%d err=%v", archived, err)
	}
	var fork orm.Conversation
	if err := db.Where("id=?", "independent-fork").Take(&fork).Error; err != nil || fork.ArchivedAt != nil {
		t.Fatalf("independent fork changed: %+v %v", fork, err)
	}
}
