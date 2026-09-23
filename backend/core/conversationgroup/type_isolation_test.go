package conversationgroup

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestGroupTypeIsolation(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ExternalAgentBinding{}, &orm.ExternalAgentSession{}, &orm.Conversation{}, &orm.ConversationOpening{}, &orm.ConversationGroup{}, &orm.ConversationGroupMember{}, &orm.ConversationGroupState{}, &orm.ConversationOrganizerRun{}, &orm.ConversationOrganizerSnapshotItem{})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	invoke := func(handler http.HandlerFunc, method, path, id string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("X-User-Id", "u")
		r = mux.SetURLVars(r, map[string]string{"group_id": id})
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	create := func(task bool) string {
		w := invoke(CreateGroup, "POST", "/", "", map[string]any{"name": "Same", "is_task_conv": task})
		if w.Code != 201 {
			t.Fatalf("create: %d %s", w.Code, w.Body.String())
		}
		var body struct {
			Group GroupDTO `json:"group"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Group.IsTaskConv != task {
			t.Fatal("lost type")
		}
		return body.Group.ID
	}
	normal, task := create(false), create(true)
	for _, tc := range []struct{ query, id string }{{"false", normal}, {"true", task}} {
		w := invoke(ListGroups, "GET", "/?is_task_conv="+tc.query, "", nil)
		var body struct {
			Groups []GroupDTO `json:"groups"`
		}
		json.Unmarshal(w.Body.Bytes(), &body)
		if w.Code != 200 || len(body.Groups) != 1 || body.Groups[0].ID != tc.id {
			t.Fatalf("list: %d %s", w.Code, w.Body.String())
		}
	}
	if w := invoke(CreateGroup, "POST", "/", "", map[string]any{"name": "same", "is_task_conv": true}); w.Code != 409 {
		t.Fatalf("duplicate: %d", w.Code)
	}
	if w := invoke(UpdateGroup, "PATCH", "/", task, map[string]any{"is_task_conv": false}); w.Code != 400 {
		t.Fatalf("type update: %d", w.Code)
	}
	if err := db.Create(&orm.Conversation{ID: "task", IsTaskConv: true, BaseModel: orm.BaseModel{CreateUserID: "u"}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return AttachNewConversation(t.Context(), tx, "u", "task", normal) }); err == nil {
		t.Fatal("cross-type creation accepted")
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return AttachNewConversation(t.Context(), tx, "u", "task", task) }); err != nil {
		t.Fatal(err)
	}
	if w := invoke(AddMember, "POST", "/", normal, map[string]any{"conversation_id": "task"}); w.Code != 409 {
		t.Fatalf("move: %d %s", w.Code, w.Body.String())
	}
	var member orm.ConversationGroupMember
	db.Where("conversation_id=?", "task").Take(&member)
	if member.GroupID != task || member.Revision != 1 {
		t.Fatalf("failed move changed membership: %+v", member)
	}
	if w := invoke(UpdateGroupPlacement, "PATCH", "/", task, map[string]any{"before_group_id": normal}); w.Code != 404 {
		t.Fatalf("cross-type anchor: %d", w.Code)
	}
	snap, _, err := buildSnapshot(t.Context(), db.DB, "run", "u")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Groups) != 1 || snap.Groups[0].ID != normal {
		t.Fatalf("organizer groups: %+v", snap.Groups)
	}
	if err := db.Create(&orm.ConversationOrganizerRun{ID: "run", UserID: "u", Status: "running", SnapshotJSON: []byte(`{}`), SnapshotHash: "x", ModelConfigJSON: []byte(`{}`)}).Error; err != nil {
		t.Fatal(err)
	}
	if w := invoke(UpdateGroup, "PATCH", "/", task, map[string]any{"name": "Task renamed"}); w.Code != 200 {
		t.Fatalf("task rename locked: %d %s", w.Code, w.Body.String())
	}
	if w := invoke(UpdateGroup, "PATCH", "/", normal, map[string]any{"name": "Normal renamed"}); w.Code != 409 {
		t.Fatalf("normal rename unlocked: %d", w.Code)
	}
	if w := invoke(CreateGroup, "POST", "/", "", map[string]any{"name": "Another task", "is_task_conv": true}); w.Code != 201 {
		t.Fatalf("task create locked: %d", w.Code)
	}
}

func TestHistoricalOrganizerCannotRestoreCrossTypeMembership(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.ConversationGroup{}, &orm.ConversationGroupMember{}, &orm.ConversationGroupState{}, &orm.ConversationOrganizerRun{}, &orm.ConversationOrganizerChange{}, &orm.ConversationOrganizerSnapshotItem{}, &orm.AsyncJob{}, &orm.UserSelectedModel{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	target := "old-group"
	for _, value := range []any{
		&orm.Conversation{ID: "c", BaseModel: orm.BaseModel{CreateUserID: "u"}},
		&orm.ConversationGroup{ID: target, UserID: "u", Name: "old", NormalizedName: "old", IsTaskConv: true, Version: 2},
		&orm.ConversationGroupState{ConversationID: "c", UserID: "u", Revision: 1, SourceRunID: "run"},
		&orm.ConversationOrganizerRun{ID: "run", UserID: "u", Status: "succeeded", SnapshotJSON: json.RawMessage(`{}`), ModelConfigJSON: json.RawMessage(`{}`)},
		&orm.ConversationOrganizerChange{ID: "change", RunID: "run", ConversationID: "c", BeforeGroupID: &target, AfterMemberRevision: 1, Kind: "correction"},
	} {
		if err := db.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-User-Id", "u")
	req = mux.SetURLVars(req, map[string]string{"run_id": "run"})
	response := httptest.NewRecorder()
	UndoOrganizer(response, req)
	if response.Code != 200 {
		t.Fatalf("undo: %d %s", response.Code, response.Body.String())
	}
	var run orm.ConversationOrganizerRun
	if err := db.Where("id=?", "run").Take(&run).Error; err != nil {
		t.Fatal(err)
	}
	var result organizerResult
	if err := json.Unmarshal(run.ResultJSON, &result); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&orm.ConversationGroupMember{}).Where("conversation_id=?", "c").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != "undone" || result.SkippedCount != 1 || count != 0 {
		t.Fatalf("cross-type undo: status=%s skipped=%d members=%d", run.Status, result.SkippedCount, count)
	}

	// An old snapshot must be rebuilt after its group was retyped or versioned by migration.
	run.Status = "failed"
	run.ErrorCode = "connection_error"
	run.SnapshotJSON = json.RawMessage(`{"groups":[{"id":"old-group","version":1}]}`)
	if got := organizerRecovery(t.Context(), db.DB, run); got != recoveryRestart {
		t.Fatalf("retyped group recovery=%s", got)
	}
	if err := db.Model(&orm.ConversationGroup{}).Where("id=?", target).Update("is_task_conv", false).Error; err != nil {
		t.Fatal(err)
	}
	if got := organizerRecovery(t.Context(), db.DB, run); got != recoveryRestart {
		t.Fatalf("changed group recovery=%s", got)
	}
}
