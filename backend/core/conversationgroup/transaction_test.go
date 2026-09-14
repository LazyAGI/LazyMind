package conversationgroup

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestGroupRemovalSerializesNewMembersWithoutBlockingOtherUsers(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.ConversationGroup{}, &orm.ConversationGroupMember{}, &orm.ConversationGroupState{}, &orm.ConversationOrganizerRun{})
	if db.Dialector.Name() != "postgres" {
		t.Skip("PostgreSQL transaction coordination")
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	group := orm.ConversationGroup{ID: "concurrent-group", UserID: "one", Name: "Group", NormalizedName: "group", Version: 1, CreatedBy: CreatedByUser, CreatedAt: now, UpdatedAt: now}
	conv := orm.Conversation{ID: "concurrent-member", BaseModel: orm.BaseModel{CreateUserID: "one", CreatedAt: now, UpdatedAt: now}}
	for _, row := range []any{&group, &conv} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	locked, release, inserted := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		inserted <- UserTransaction(t.Context(), db.DB, "one", func(tx *gorm.DB) error {
			close(locked)
			<-release
			_, err := moveMembershipTx(tx, "one", conv.ID, &group.ID, CreatedByUser, "")
			return err
		})
	}()
	<-locked
	deleted := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest(http.MethodDelete, "/", nil)
		r.Header.Set("X-User-Id", "one")
		r = mux.SetURLVars(r, map[string]string{"group_id": group.ID})
		w := httptest.NewRecorder()
		DeleteGroup(w, r)
		deleted <- w
	}()
	other := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"name":"Other"}`))
		r.Header.Set("X-User-Id", "two")
		w := httptest.NewRecorder()
		CreateGroup(w, r)
		other <- w
	}()
	select {
	case response := <-other:
		if response.Code != 201 {
			close(release)
			t.Fatalf("other user create: %d", response.Code)
		}
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("one user blocked another user's group mutation")
	}
	select {
	case response := <-deleted:
		close(release)
		t.Fatalf("removal overtook uncommitted member: %d", response.Code)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-inserted; err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-deleted:
		if response.Code != 200 {
			t.Fatalf("remove: %d %s", response.Code, response.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("group removal did not finish after transaction release")
	}
	var count int64
	db.Model(&orm.ConversationGroupMember{}).Where("group_id=?", group.ID).Count(&count)
	if count != 0 {
		t.Fatal("group removal left a concurrently added member")
	}
	var state orm.ConversationGroupState
	if err := db.Where("conversation_id=?", conv.ID).Take(&state).Error; err != nil {
		t.Fatal(err)
	}
	if state.GroupID != nil {
		t.Fatal("group removal left stale membership provenance")
	}
}
