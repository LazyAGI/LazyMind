package conversationgroup

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestManualGroupLifecycleAndOrganizerDeleteLock(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ConversationOpening{}, &orm.Conversation{}, &orm.ConversationGroup{}, &orm.ConversationGroupMember{}, &orm.ConversationGroupState{}, &orm.ConversationOrganizerRun{}, &orm.ConversationOrganizerSnapshotItem{})
	store.Init(db.DB, nil, nil)
	const uid = "group-user"
	now := time.Now().UTC()
	invoke := func(handler http.HandlerFunc, method string, body any, vars map[string]string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, "/", bytes.NewReader(raw))
		req.Header.Set("X-User-Id", uid)
		req = mux.SetURLVars(req, vars)
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}
	created := invoke(CreateGroup, http.MethodPost, map[string]any{"name": "  邮件处理  ", "scope": "查询和发送邮件"}, nil)
	if created.Code != 201 {
		t.Fatalf("create %d %s", created.Code, created.Body.String())
	}
	var payload struct {
		Group GroupDTO `json:"group"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Group.Name != "邮件处理" || payload.Group.Scope != "查询和发送邮件" || payload.Group.MemberCount != 0 {
		t.Fatalf("unexpected group %+v", payload.Group)
	}
	duplicate := invoke(CreateGroup, http.MethodPost, map[string]any{"name": "邮件处理"}, nil)
	if duplicate.Code != 409 {
		t.Fatalf("duplicate %d %s", duplicate.Code, duplicate.Body.String())
	}
	conv := orm.Conversation{ID: "conversation-1", DisplayName: "邮件", BaseModel: orm.BaseModel{CreateUserID: uid, CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(&conv).Error; err != nil {
		t.Fatal(err)
	}
	added := invoke(AddMember, http.MethodPost, map[string]any{"conversation_id": conv.ID}, map[string]string{"group_id": payload.Group.ID})
	if added.Code != 200 {
		t.Fatalf("add %d %s", added.Code, added.Body.String())
	}
	updated := invoke(UpdateGroup, http.MethodPatch, map[string]any{"scope": "只包含实际邮件操作，不包含测试"}, map[string]string{"group_id": payload.Group.ID})
	if updated.Code != 200 {
		t.Fatalf("update %d %s", updated.Code, updated.Body.String())
	}
	var after struct {
		Group GroupDTO `json:"group"`
	}
	_ = json.Unmarshal(updated.Body.Bytes(), &after)
	if after.Group.Version != 2 || after.Group.MemberCount != 1 {
		t.Fatalf("updated group %+v", after.Group)
	}
	run := orm.ConversationOrganizerRun{ID: "run-1", UserID: uid, Status: "running", SnapshotJSON: json.RawMessage(`{}`), SnapshotHash: "hash", ModelConfigJSON: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ConversationOrganizerSnapshotItem{RunID: run.ID, ConversationID: conv.ID, UserID: uid, Title: "邮件", Summary: "发送邮件", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	locked, err := IsDeleteLocked(t.Context(), db.DB, uid, []string{conv.ID})
	if err != nil || !locked {
		t.Fatalf("lock=%v err=%v", locked, err)
	}
	second := orm.ConversationGroup{ID: "group-2", UserID: uid, Name: "第二组", NormalizedName: "第二组", Version: 1, CreatedBy: CreatedByUser, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	moveLocked := invoke(AddMember, http.MethodPost, map[string]any{"conversation_id": conv.ID}, map[string]string{"group_id": second.ID})
	if moveLocked.Code != 409 {
		t.Fatalf("locked move %d %s", moveLocked.Code, moveLocked.Body.String())
	}
	var stillMember orm.ConversationGroupMember
	if err := db.Where("conversation_id=?", conv.ID).Take(&stillMember).Error; err != nil || stillMember.GroupID != payload.Group.ID {
		t.Fatalf("locked move changed membership err=%v member=%+v", err, stillMember)
	}
	removed := invoke(RemoveMember, http.MethodDelete, nil, map[string]string{"group_id": payload.Group.ID, "conversation_id": conv.ID})
	if removed.Code != 409 {
		t.Fatalf("locked remove %d %s", removed.Code, removed.Body.String())
	}
	if err := db.Model(&orm.ConversationOrganizerRun{}).Where("id=?", run.ID).Update("status", "canceled").Error; err != nil {
		t.Fatal(err)
	}
	removed = invoke(RemoveMember, http.MethodDelete, nil, map[string]string{"group_id": payload.Group.ID, "conversation_id": conv.ID})
	if removed.Code != 200 {
		t.Fatalf("remove %d %s", removed.Code, removed.Body.String())
	}
	stale := invoke(RemoveMember, http.MethodDelete, nil, map[string]string{"group_id": payload.Group.ID, "conversation_id": conv.ID})
	if stale.Code != 409 {
		t.Fatalf("stale remove %d %s", stale.Code, stale.Body.String())
	}
	deleted := invoke(DeleteGroup, http.MethodDelete, nil, map[string]string{"group_id": payload.Group.ID})
	if deleted.Code != 200 {
		t.Fatalf("delete %d %s", deleted.Code, deleted.Body.String())
	}
	recreated := invoke(CreateGroup, http.MethodPost, map[string]any{"name": "邮件处理"}, nil)
	if recreated.Code != 201 {
		t.Fatalf("recreate %d %s", recreated.Code, recreated.Body.String())
	}
}

func TestUndoDoesNotOverwriteFreeMembershipABA(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ConversationOpening{}, &orm.Conversation{}, &orm.ConversationGroup{}, &orm.ConversationGroupMember{}, &orm.ConversationGroupState{}, &orm.ConversationOrganizerRun{}, &orm.ConversationOrganizerChange{})
	store.Init(db.DB, nil, nil)
	const uid = "undo-user"
	now := time.Now().UTC()
	conv := orm.Conversation{ID: "c", DisplayName: "c", BaseModel: orm.BaseModel{CreateUserID: uid, CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(&conv).Error; err != nil {
		t.Fatal(err)
	}
	group := orm.ConversationGroup{ID: "g", UserID: uid, Name: "组", NormalizedName: "组", Version: 1, CreatedBy: CreatedByUser, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	run := orm.ConversationOrganizerRun{ID: "r", UserID: uid, Status: "succeeded", SnapshotJSON: json.RawMessage(`{}`), SnapshotHash: "h", ModelConfigJSON: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	state := orm.ConversationGroupState{ConversationID: conv.ID, UserID: uid, Revision: 2, SourceRunID: run.ID, UpdatedAt: now}
	if err := db.Create(&state).Error; err != nil {
		t.Fatal(err)
	}
	change := orm.ConversationOrganizerChange{ID: "ch", RunID: run.ID, ConversationID: conv.ID, AfterMemberRevision: 2, Kind: "correction", CreatedAt: now}
	if err := db.Create(&change).Error; err != nil {
		t.Fatal(err)
	}
	if err := MoveConversation(t.Context(), db.DB, uid, conv.ID, group.ID, CreatedByUser, ""); err != nil {
		t.Fatal(err)
	}
	if err := MoveConversation(t.Context(), db.DB, uid, conv.ID, "", CreatedByUser, ""); err != nil {
		t.Fatal(err)
	}
	var before orm.ConversationGroupState
	db.Where("conversation_id=?", conv.ID).Take(&before)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-User-Id", uid)
	req = mux.SetURLVars(req, map[string]string{"run_id": run.ID})
	rec := httptest.NewRecorder()
	UndoOrganizer(rec, req)
	if rec.Code != 200 {
		t.Fatalf("undo %d %s", rec.Code, rec.Body.String())
	}
	var after orm.ConversationGroupState
	db.Where("conversation_id=?", conv.ID).Take(&after)
	if after.Revision != before.Revision || after.SourceRunID != "" {
		t.Fatalf("undo overwrote later free state before=%+v after=%+v", before, after)
	}
	var stored orm.ConversationOrganizerChange
	db.Where("id=?", change.ID).Take(&stored)
	if stored.UndoneAt != nil {
		t.Fatal("skipped ABA change marked undone")
	}
}

func TestSanitizeModelConfigRemovesCredentials(t *testing.T) {
	raw := map[string]any{"llm": map[string]any{"model": "qwen", "api_key": "secret", "nested": map[string]any{"client_secret": "also-secret"}}, "max_input_tokens": "64000"}
	encoded, _ := json.Marshal(sanitizeModelConfig(raw))
	if bytes.Contains(encoded, []byte("secret")) {
		t.Fatalf("sanitized config leaked credential: %s", encoded)
	}
	if !bytes.Contains(encoded, []byte("qwen")) || !bytes.Contains(encoded, []byte("64000")) {
		t.Fatalf("sanitized config lost routing identity: %s", encoded)
	}
}
