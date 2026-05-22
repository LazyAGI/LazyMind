package acl_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/acl"
	"lazymind/core/common/orm"
)

func initTestStore(t *testing.T) {
	t.Helper()
	db, err := orm.Connect(orm.DriverSQLite, "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := db.AutoMigrate(orm.AllModelsForDDL()...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	acl.InitStore(db)
}

func TestPermissionFor_Owner(t *testing.T) {
	initTestStore(t)
	st := acl.GetStore()
	kbID := st.EnsureKB("", "test-kb", "100")
	perm, src := acl.PermissionFor(acl.ResourceTypeKB, kbID, "100")
	if perm != acl.PermWrite || src != acl.SourceOwner {
		t.Errorf("owner: got perm=%s src=%s, want write/owner", perm, src)
	}
}

func TestPermissionFor_PublicRead(t *testing.T) {
	initTestStore(t)
	st := acl.GetStore()
	kbID := st.EnsureKB("", "pub-kb", "1")
	st.SetKBVisibility(kbID, acl.VisibilityPublic)
	perm, _ := acl.PermissionFor(acl.ResourceTypeKB, kbID, "999")
	if perm != acl.PermRead {
		t.Errorf("public: got perm=%s, want read", perm)
	}
}

func TestPermissionFor_PrivateNoACL(t *testing.T) {
	initTestStore(t)
	st := acl.GetStore()
	kbID := st.EnsureKB("", "priv-kb", "1")
	perm, _ := acl.PermissionFor(acl.ResourceTypeKB, kbID, "999")
	if perm != acl.PermNone {
		t.Errorf("private: got perm=%s, want none", perm)
	}
}

func TestCan_ReadWrite(t *testing.T) {
	initTestStore(t)
	st := acl.GetStore()
	kbID := st.EnsureKB("", "can-kb", "1")
	st.AddACL(acl.ResourceTypeKB, kbID, acl.GranteeUser, "2", acl.PermRead, "1", nil)
	if !acl.Can("2", acl.ResourceTypeKB, kbID, acl.PermRead) {
		t.Error("user 2 should have read")
	}
	if acl.Can("2", acl.ResourceTypeKB, kbID, acl.PermWrite) {
		t.Error("user 2 should not have write")
	}
	st.AddACL(acl.ResourceTypeKB, kbID, acl.GranteeUser, "3", acl.PermWrite, "1", nil)
	if !acl.Can("3", acl.ResourceTypeKB, kbID, acl.PermWrite) {
		t.Error("user 3 should have write")
	}
}

func TestListACL_InvalidKbID(t *testing.T) {
	initTestStore(t)
	req := httptest.NewRequest(http.MethodGet, "/api/kb//acl", nil)
	req = mux.SetURLVars(req, map[string]string{"kb_id": ""})
	rr := httptest.NewRecorder()
	acl.ListACL(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("empty kb_id: got status %d", rr.Code)
	}
}
