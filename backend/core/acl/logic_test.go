package acl

import (
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

	db := orm.MigrateTestDB(t, &orm.ACLModel{}, &orm.UserGroupModel{})

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

// TestKBPermissionNormalization verifies that KB read/write/create_doc/delete_doc/delete_kb
// permissions normalize correctly to their canonical forms.
func TestKBPermissionNormalization(t *testing.T) {
	tests := map[string]string{
		PermRead:              PermissionKBRead,
		PermWrite:             PermissionKBWrite,
		PermissionKBRead:      PermissionKBRead,
		PermissionKBWrite:     PermissionKBWrite,
		PermissionKBCreateDoc: PermissionKBCreateDoc,
		PermissionKBDeleteDoc: PermissionKBDeleteDoc,
		PermissionKBDelete:    PermissionKBDelete,
		"":                    PermNone,
		PermNone:              PermNone,
		"unknown_perm":        "",
	}
	for input, want := range tests {
		got := normalizePermission(ResourceTypeKB, input)
		if got != want {
			t.Fatalf("normalizePermission(KB, %q) = %q, want %q", input, got, want)
		}
	}

	perms := ownerPermissions(ResourceTypeKB)
	if len(perms) != 5 {
		t.Fatalf("ownerPermissions(KB): expected 5 perms, got %d", len(perms))
	}
	if perms[0] != PermissionKBRead || perms[1] != PermissionKBWrite {
		t.Fatalf("ownerPermissions(KB): unexpected order: %v", perms)
	}
}

// TestDatasetPermissionNormalization verifies that Dataset read/write/upload
// permissions normalize correctly.
func TestDatasetPermissionNormalization(t *testing.T) {
	tests := map[string]string{
		PermRead:                PermissionDatasetRead,
		PermWrite:               PermissionDatasetWrite,
		PermUpload:              PermissionDatasetUpload,
		PermissionDatasetRead:   PermissionDatasetRead,
		PermissionDatasetWrite:  PermissionDatasetWrite,
		PermissionDatasetUpload: PermissionDatasetUpload,
		"":                      PermNone,
	}
	for input, want := range tests {
		got := normalizePermission(ResourceTypeDB, input)
		if got != want {
			t.Fatalf("normalizePermission(DB, %q) = %q, want %q", input, got, want)
		}
	}

	perms := ownerPermissions(ResourceTypeDB)
	if len(perms) != 3 {
		t.Fatalf("ownerPermissions(DB): expected 3 perms, got %d", len(perms))
	}

	pub := publicPermissions(ResourceTypeDB)
	if len(pub) != 0 {
		t.Fatalf("publicPermissions(DB): expected empty, got %v", pub)
	}
}

// TestHasPermission checks that hasPermission correctly matches against a list.
func TestHasPermission(t *testing.T) {
	if hasPermission(nil, "") {
		t.Fatal("empty want should return false")
	}
	if hasPermission(nil, PermNone) {
		t.Fatal("none want should return false")
	}
	if !hasPermission([]string{"KB_READ", "KB_WRITE"}, "KB_READ") {
		t.Fatal("expected true for matching permission")
	}
	if hasPermission([]string{"KB_READ"}, "KB_WRITE") {
		t.Fatal("expected false for missing permission")
	}
}

// TestActionToPermission_KB maps read/write/create_doc/delete_doc/delete_kb actions.
func TestActionToPermission_KB(t *testing.T) {
	tests := map[string]string{
		PermRead:  PermissionKBRead,
		PermWrite: PermissionKBWrite,
		"unknown": "",
	}
	for action, want := range tests {
		got := actionToPermission(ResourceTypeKB, action)
		if got != want {
			t.Fatalf("actionToPermission(KB, %q) = %q, want %q", action, got, want)
		}
	}
}

// TestActionToPermission_Dataset maps read/write/upload actions.
func TestActionToPermission_Dataset(t *testing.T) {
	if got := actionToPermission(ResourceTypeDB, PermRead); got != PermissionDatasetRead {
		t.Fatalf("actionToPermission(DB, read) = %q, want %q", got, PermissionDatasetRead)
	}
	if got := actionToPermission(ResourceTypeDB, PermWrite); got != PermissionDatasetWrite {
		t.Fatalf("actionToPermission(DB, write) = %q, want %q", got, PermissionDatasetWrite)
	}
	if got := actionToPermission(ResourceTypeDB, PermUpload); got != PermissionDatasetUpload {
		t.Fatalf("actionToPermission(DB, upload) = %q, want %q", got, PermissionDatasetUpload)
	}
}

// TestPublicPermissions_KB returns KB_READ for KB, nil for other types.
func TestPublicPermissions_KB(t *testing.T) {
	perms := publicPermissions(ResourceTypeKB)
	if len(perms) != 1 || perms[0] != PermissionKBRead {
		t.Fatalf("publicPermissions(KB): got %v, want [KB_READ]", perms)
	}

	if perms := publicPermissions(ResourceTypeDB); perms != nil {
		t.Fatalf("publicPermissions(DB): expected nil, got %v", perms)
	}
	if perms := publicPermissions("unknown"); perms != nil {
		t.Fatalf("publicPermissions(unknown): expected nil, got %v", perms)
	}
}
