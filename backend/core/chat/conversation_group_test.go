package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestConversationGroupFirstMessagePersistsMembershipBeforeModel(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	group := orm.ConversationGroup{ID: "first-message-group", UserID: "user-1", Name: "项目", NormalizedName: "项目", Version: 1, CreatedBy: "user", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/chat/stream" {
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "tool_groups": []any{}, "data": map[string]any{"items": []any{}}})
			return
		}
		called = true
		var member orm.ConversationGroupMember
		if err := db.Where("conversation_id=?", "group-initial-chat").Take(&member).Error; err != nil || member.GroupID != group.ID {
			t.Errorf("membership missing before upstream call: %v %+v", err, member)
		}
		var payload LazyChatRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "success", "data": map[string]any{"text": "answer"}})
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "success", "data": map[string]any{"runtime_event": completedRunEvent(payload.Conversation.RunID, true)}})
	}))
	defer server.Close()
	for _, name := range []string{"LAZYMIND_CHAT_SERVICE_URL", "LAZYMIND_AUTH_SERVICE_URL", "LAZYMIND_SCAN_CONTROL_PLANE_URL"} {
		t.Setenv(name, server.URL)
	}
	r := sidechatRequest(http.MethodPost, "/api/core/conversations:chat", "user-1", `{"conversation_id":"group-initial-chat","group_id":"first-message-group","query":"first message","stream":false}`, nil)
	w := httptest.NewRecorder()
	ChatConversations(w, r)
	if w.Code != 200 || !called || !strings.Contains(w.Body.String(), "answer") {
		t.Fatalf("first message status=%d called=%v body=%s", w.Code, called, w.Body.String())
	}
	var count int64
	if err := db.Model(&orm.Conversation{}).Where("id=?", "group-initial-chat").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("conversation persistence count=%d err=%v", count, err)
	}
}

func TestConversationGroupFirstMessageRejectsStaleOrExistingTarget(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	request := sidechatRequest(http.MethodPost, "/api/core/conversations:chat", "user-1", `{"conversation_id":"new-group-conversation","group_id":"deleted-group","stream":true,"input":[{"input_type":"text","text":"first message"}]}`, nil)
	recorder := httptest.NewRecorder()
	ChatConversations(recorder, request)
	if recorder.Code != http.StatusNotFound && recorder.Code != http.StatusConflict {
		t.Fatalf("stale group status=%d: %s", recorder.Code, recorder.Body.String())
	}
	var count int64
	if err := db.Model(&orm.Conversation{}).Where("id=?", "new-group-conversation").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("stale group left an empty conversation")
	}
	sidechatTestConversation(t, db.DB, "existing-group-conversation", "user-1", "Existing")
	request = sidechatRequest(http.MethodPost, "/api/core/conversations:chat", "user-1", `{"conversation_id":"existing-group-conversation","group_id":"some-group","stream":true,"input":[{"input_type":"text","text":"continue"}]}`, nil)
	recorder = httptest.NewRecorder()
	ChatConversations(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("existing conversation accepted group_id: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestConversationOrganizerProtectsSingleBatchAndFamilyDelete(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	root := sidechatTestConversation(t, db.DB, "locked-root", "user-1", "Root")
	child := sidechatTestConversation(t, db.DB, "locked-child", "user-1", "Child")
	if err := db.Model(&child).Updates(map[string]any{"parent_conversation_id": root.ID, "relation_type": conversationRelationSidechat}).Error; err != nil {
		t.Fatal(err)
	}
	sidechatTestConversation(t, db.DB, "unlocked-other", "user-1", "Other")
	now := time.Now().UTC()
	run := orm.ConversationOrganizerRun{ID: "delete-lock-run", UserID: "user-1", Status: "running", SnapshotJSON: json.RawMessage(`{}`), ModelConfigJSON: json.RawMessage(`{}`), Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ConversationOrganizerSnapshotItem{RunID: run.ID, ConversationID: root.ID, UserID: "user-1", Title: "Locked", Summary: "Snapshot", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name, method, body string
		handler            http.HandlerFunc
		vars               map[string]string
	}{
		{"single", http.MethodDelete, "", DeleteConversation, map[string]string{"name": root.ID}},
		{"batch", http.MethodPost, `{"conversation_ids":["unlocked-other","locked-root"]}`, BatchDeleteConversations, nil},
		{"archive", http.MethodPost, `{}`, ArchiveConversation, map[string]string{"conversation_id": root.ID}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			r := sidechatRequest(scenario.method, "/", "user-1", scenario.body, scenario.vars)
			w := httptest.NewRecorder()
			scenario.handler(w, r)
			if w.Code != http.StatusConflict {
				t.Fatalf("delete status=%d: %s", w.Code, w.Body.String())
			}
			var deleted int64
			db.Model(&orm.Conversation{}).Where("deleted_at IS NOT NULL").Count(&deleted)
			if deleted != 0 {
				t.Fatal("rejected delete changed a member of the batch/family")
			}
			var archived int64
			db.Model(&orm.Conversation{}).Where("archived_at IS NOT NULL").Count(&archived)
			if archived != 0 {
				t.Fatal("rejected mutation archived a snapshot member")
			}
		})
	}
	// A snapshot of a descendant must also block deletion through its ancestor.
	if err := db.Model(&orm.ConversationOrganizerSnapshotItem{}).Where("run_id=?", run.ID).Update("conversation_id", child.ID).Error; err != nil {
		t.Fatal(err)
	}
	r := sidechatRequest(http.MethodDelete, "/", "user-1", "", map[string]string{"name": root.ID})
	w := httptest.NewRecorder()
	DeleteConversation(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("cascade delete bypassed descendant lock: %d %s", w.Code, w.Body.String())
	}
	if err := db.Model(&run).Update("status", "canceled").Error; err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	DeleteConversation(w, sidechatRequest(http.MethodDelete, "/", "user-1", "", map[string]string{"name": root.ID}))
	if w.Code != http.StatusOK {
		t.Fatalf("canceled run retained lock: %d %s", w.Code, w.Body.String())
	}
	var deleted int64
	db.Model(&orm.Conversation{}).Where("id IN ? AND deleted_at IS NOT NULL", []string{root.ID, child.ID}).Count(&deleted)
	if deleted != 2 {
		t.Fatalf("family deletion after unlock removed %d/2", deleted)
	}
}

func TestConversationDetailReturnsGroupAndOrganizerLock(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	c := sidechatTestConversation(t, db.DB, "group-detail", "user-1", "Grouped")
	now := time.Now().UTC()
	for _, row := range []any{
		&orm.ConversationGroup{ID: "detail-group", UserID: "user-1", Name: "Group", NormalizedName: "group", CreatedAt: now, UpdatedAt: now},
		&orm.ConversationGroupMember{ConversationID: c.ID, GroupID: "detail-group", UserID: "user-1", CreatedAt: now, UpdatedAt: now},
		&orm.ConversationOrganizerRun{ID: "detail-run", UserID: "user-1", Status: "running", SnapshotJSON: json.RawMessage(`{}`), ModelConfigJSON: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now},
		&orm.ConversationOrganizerSnapshotItem{ConversationID: c.ID, RunID: "detail-run", UserID: "user-1"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	rec := httptest.NewRecorder()
	GetConversationDetail(rec, sidechatRequest(http.MethodGet, "/", "user-1", "", map[string]string{"name": c.ID}))
	var response struct {
		Conversation map[string]any `json:"conversation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || response.Conversation["group_id"] != "detail-group" || response.Conversation["organizing_run_id"] != "detail-run" {
		t.Fatalf("detail %d: %s", rec.Code, rec.Body.String())
	}
}
