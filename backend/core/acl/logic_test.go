package acl

import (
	"path/filepath"
	"testing"

	"lazymind/core/common/orm"
)

func TestEvalSetPermissionNormalization(t *testing.T) {
	tests := map[string]string{
		PermRead:               PermissionEvalSetRead,
		PermWrite:              PermissionEvalSetWrite,
		PermissionEvalSetRead:  PermissionEvalSetRead,
		PermissionEvalSetWrite: PermissionEvalSetWrite,
	}

	for input, want := range tests {
		if got := normalizePermission(ResourceTypeEvalSet, input); got != want {
			t.Fatalf("normalizePermission(%q) = %q, want %q", input, got, want)
		}
	}

	perms := ownerPermissions(ResourceTypeEvalSet)
	if len(perms) != 2 || perms[0] != PermissionEvalSetRead || perms[1] != PermissionEvalSetWrite {
		t.Fatalf("ownerPermissions(eval_set) = %#v", perms)
	}

	if got := actionToPermission(ResourceTypeEvalSet, PermRead); got != PermissionEvalSetRead {
		t.Fatalf("read action = %q, want %q", got, PermissionEvalSetRead)
	}
	if got := actionToPermission(ResourceTypeEvalSet, PermWrite); got != PermissionEvalSetWrite {
		t.Fatalf("write action = %q, want %q", got, PermissionEvalSetWrite)
	}
}

func TestEvalSetWriteAllowsRead(t *testing.T) {
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", "http://%")

	db, err := orm.Connect(orm.DriverSQLite, filepath.Join(t.TempDir(), "acl.db"))
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}
	if err := db.AutoMigrate(&orm.ACLModel{}, &orm.UserGroupModel{}); err != nil {
		t.Fatalf("auto migrate acl models: %v", err)
	}

	previousStore := defaultStore
	t.Cleanup(func() { defaultStore = previousStore })
	InitStore(db)

	if id := GetStore().AddACL(ResourceTypeEvalSet, "eval_set_1", GranteeUser, "user_1", PermissionEvalSetWrite, "owner_1", nil); id == 0 {
		t.Fatal("expected eval set write ACL to be inserted")
	}
	if !Can("user_1", ResourceTypeEvalSet, "eval_set_1", PermRead) {
		t.Fatal("expected EVAL_SET_WRITE to allow read")
	}
	if !Can("user_1", ResourceTypeEvalSet, "eval_set_1", PermWrite) {
		t.Fatal("expected EVAL_SET_WRITE to allow write")
	}
}
