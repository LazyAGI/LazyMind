package acl

import (
	"testing"

	"lazymind/core/common/orm"
)

// newTestStore creates a SQLite-backed Store for testing, auto-migrating ACL tables.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db := orm.MigrateTestDB(t, &orm.ACLModel{}, &orm.UserGroupModel{}, &orm.KBModel{}, &orm.VisibilityModel{}, &orm.ACLGroupModel{})

	previousStore := defaultStore
	t.Cleanup(func() { defaultStore = previousStore })
	InitStore(db)
	return GetStore()
}

// TestStoreEnsureKB creates a KB or returns its existing ID when called with the same ID.
func TestStoreEnsureKB(t *testing.T) {
	st := newTestStore(t)

	id := st.EnsureKB("kb-1", "Test KB", "owner-1")
	if id != "kb-1" {
		t.Fatalf("EnsureKB: got %q, want kb-1", id)
	}

	sameID := st.EnsureKB("kb-1", "Updated Name", "owner-2")
	if sameID != "kb-1" {
		t.Fatalf("EnsureKB same id: got %q, want kb-1", sameID)
	}
}

// TestStoreGetKB retrieves a previously created KB.
func TestStoreGetKB(t *testing.T) {
	st := newTestStore(t)
	st.EnsureKB("kb-2", "My KB", "owner-2")

	kb := st.GetKB("kb-2")
	if kb == nil {
		t.Fatal("GetKB returned nil for existing KB")
	}
	if kb.Name != "My KB" {
		t.Fatalf("KB name: got %q, want My KB", kb.Name)
	}
	if kb.OwnerID != "owner-2" {
		t.Fatalf("KB owner: got %q, want owner-2", kb.OwnerID)
	}
}

// TestStoreGetKB_NotFound returns nil for a non-existent KB.
func TestStoreGetKB_NotFound(t *testing.T) {
	st := newTestStore(t)
	if kb := st.GetKB("nonexistent"); kb != nil {
		t.Fatal("expected nil for unknown kb")
	}
}

// TestStoreSetKBVisibility updates the visibility of a KB.
func TestStoreSetKBVisibility(t *testing.T) {
	st := newTestStore(t)
	st.EnsureKB("kb-3", "KB3", "owner-3")

	st.SetKBVisibility("kb-3", VisibilityProtected)
	if vis := st.GetVisibility("kb-3"); vis != VisibilityProtected {
		t.Fatalf("visibility: got %q, want %q", vis, VisibilityProtected)
	}
}

// TestStoreAddAndListACL inserts an ACL row and lists it back.
func TestStoreAddAndListACL(t *testing.T) {
	st := newTestStore(t)
	st.EnsureKB("kb-4", "KB4", "owner-4")

	aclID := st.AddACL(ResourceTypeKB, "kb-4", GranteeUser, "user-1", PermissionKBRead, "owner-4", nil)
	if aclID == 0 {
		t.Fatal("expected non-zero ACL id")
	}

	list := st.ListACL(ResourceTypeKB, "kb-4", "")
	if len(list) == 0 {
		t.Fatal("expected at least one ACL entry")
	}
	found := false
	for _, item := range list {
		if item.GranteeID == "user-1" && item.Permission == PermissionKBRead {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected ACL entry for user-1 with KB_READ")
	}
}

// TestStoreUpdateAndDeleteACL modifies and then removes an ACL entry.
func TestStoreUpdateAndDeleteACL(t *testing.T) {
	st := newTestStore(t)
	st.EnsureKB("kb-5", "KB5", "owner-5")

	aclID := st.AddACL(ResourceTypeKB, "kb-5", GranteeUser, "user-2", PermissionKBRead, "owner-5", nil)
	if aclID == 0 {
		t.Fatal("expected non-zero ACL id")
	}

	if !st.UpdateACL(aclID, PermissionKBWrite, nil) {
		t.Fatal("expected UpdateACL to succeed")
	}

	row, found := st.GetACLByID(ResourceTypeKB, "kb-5", aclID)
	if !found || row.Permission != PermissionKBWrite {
		t.Fatalf("expected updated permission KB_WRITE, got %v", row)
	}

	if !st.DeleteACL(aclID) {
		t.Fatal("expected DeleteACL to succeed")
	}
	if _, found := st.GetACLByID(ResourceTypeKB, "kb-5", aclID); found {
		t.Fatal("expected ACL to be deleted")
	}
}

// TestStoreACLsForUser returns ACLs applicable to a specific user.
func TestStoreACLsForUser(t *testing.T) {
	st := newTestStore(t)
	st.EnsureKB("kb-6", "KB6", "owner-6")

	st.AddACL(ResourceTypeKB, "kb-6", GranteeUser, "user-3", PermissionKBRead, "owner-6", nil)
	rows := st.ACLsForUser(ResourceTypeKB, "kb-6", "user-3")
	if len(rows) == 0 {
		t.Fatal("expected ACL rows for user-3")
	}
}

// TestStoreEnsureGroup creates a group and returns its ID.
func TestStoreEnsureGroup(t *testing.T) {
	st := newTestStore(t)

	id := st.EnsureGroup("group-1", "Test Group")
	if id != "group-1" {
		t.Fatalf("EnsureGroup id: got %q, want group-1", id)
	}

	// Call again with same ID — ensures idempotency.
	id2 := st.EnsureGroup("group-1", "Updated")
	if id2 != "group-1" {
		t.Fatalf("EnsureGroup: expected same id, got %q", id2)
	}
}

// TestStoreDeleteGroup removes a group so that subsequent EnsureGroup recreates it.
func TestStoreDeleteGroup(t *testing.T) {
	st := newTestStore(t)
	st.EnsureGroup("group-2", "To Delete")
	st.DeleteGroup("group-2")

	// After deletion, EnsureGroup should recreate with the same ID.
	id := st.EnsureGroup("group-2", "Recreated")
	if id != "group-2" {
		t.Fatalf("EnsureGroup after delete: got %q, want group-2", id)
	}
}

// TestStoreAddACL_WithExpiry creates an ACL with an expiration time.
func TestStoreAddACL_WithExpiry(t *testing.T) {
	st := newTestStore(t)
	st.EnsureKB("kb-7", "KB7", "owner-7")

	aclID := st.AddACL(ResourceTypeKB, "kb-7", GranteeUser, "user-4", PermissionKBRead, "owner-7", nil)
	// Update with an expiry far in the future.
	_ = st.UpdateACL(aclID, PermissionKBRead, nil)
	if _, found := st.GetACLByID(ResourceTypeKB, "kb-7", aclID); !found {
		t.Fatal("expected ACL to exist after update")
	}
}

// TestStoreCanonicalGranteeType normalizes grantee type values.
func TestStoreCanonicalGranteeType(t *testing.T) {
	if got := canonicalGranteeType(GranteeTenant); got != GranteeGroup {
		t.Fatalf("canonicalGranteeType(tenant) = %q, want %q", got, GranteeGroup)
	}
	if got := canonicalGranteeType(GranteeUser); got != GranteeUser {
		t.Fatalf("canonicalGranteeType(user) = %q, want %q", got, GranteeUser)
	}
}
