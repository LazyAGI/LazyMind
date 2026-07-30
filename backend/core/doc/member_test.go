package doc

import (
	"testing"

	"lazymind/core/acl"
)

// TestNormalizeDatasetGranteeType maps tenant to group, leaves others unchanged.
func TestNormalizeDatasetGranteeType(t *testing.T) {
	if got := normalizeDatasetGranteeType(acl.GranteeTenant); got != acl.GranteeGroup {
		t.Fatalf("tenant: got %q, want %q", got, acl.GranteeGroup)
	}
	if got := normalizeDatasetGranteeType(acl.GranteeUser); got != acl.GranteeUser {
		t.Fatalf("user: got %q, want %q", got, acl.GranteeUser)
	}
	if got := normalizeDatasetGranteeType(acl.GranteeGroup); got != acl.GranteeGroup {
		t.Fatalf("group: got %q, want %q", got, acl.GranteeGroup)
	}
}

// TestCompareDatasetPermissionPriority assigns higher priority to higher roles.
func TestCompareDatasetPermissionPriority(t *testing.T) {
	// Owner > maintainer.
	if compareDatasetPermissionPriority("dataset_owner", "dataset_maintainer") <= 0 {
		t.Fatal("owner should rank higher than maintainer")
	}
	// Maintainer > uploader.
	if compareDatasetPermissionPriority("dataset_maintainer", "dataset_uploader") <= 0 {
		t.Fatal("maintainer should rank higher than uploader")
	}
	// Uploader > user.
	if compareDatasetPermissionPriority("dataset_uploader", "dataset_user") <= 0 {
		t.Fatal("uploader should rank higher than user")
	}
	// Same role returns 0.
	if compareDatasetPermissionPriority("dataset_user", "dataset_user") != 0 {
		t.Fatal("same role should return 0")
	}
	// Unknown role returns 0 (equal).
	if compareDatasetPermissionPriority("unknown", "dataset_user") != -1 {
		t.Fatal("unknown vs user: expected negative priority diff")
	}
}

// TestRoleToPermissions maps human roles to ACL permission sets.
func TestRoleToPermissions(t *testing.T) {
	// User role: read-only.
	perms := roleToPermissions("dataset_user")
	if len(perms) != 1 || perms[0] != acl.PermissionDatasetRead {
		t.Fatalf("user: got %v", perms)
	}
	// Uploader role: upload.
	perms = roleToPermissions("dataset_uploader")
	if len(perms) != 1 || perms[0] != acl.PermissionDatasetUpload {
		t.Fatalf("uploader: got %v", perms)
	}
	// Maintainer role: read + upload + write.
	perms = roleToPermissions("dataset_maintainer")
	if len(perms) != 3 {
		t.Fatalf("maintainer: expected 3 perms, got %d", len(perms))
	}
	// Owner role: same as maintainer.
	perms = roleToPermissions("dataset_owner")
	if len(perms) != 3 {
		t.Fatalf("owner: expected 3 perms, got %d", len(perms))
	}
	// Unknown role: nil.
	if perms := roleToPermissions("unknown"); perms != nil {
		t.Fatalf("unknown: expected nil, got %v", perms)
	}
}

// TestPermissionToRole maps ACL permissions back to human roles with i18n labels.
func TestPermissionToRole(t *testing.T) {
	role, label := permissionToRole(acl.PermissionDatasetRead)
	if role != "dataset_user" || label != "只读者" {
		t.Fatalf("read: role=%q label=%q", role, label)
	}
	role, label = permissionToRole(acl.PermissionDatasetUpload)
	if role != "dataset_uploader" || label != "上传者" {
		t.Fatalf("upload: role=%q label=%q", role, label)
	}
	role, label = permissionToRole(acl.PermissionDatasetWrite)
	if role != "dataset_maintainer" || label != "管理者" {
		t.Fatalf("write: role=%q label=%q", role, label)
	}
	role, label = permissionToRole("unknown")
	if role != "" || label != "" {
		t.Fatalf("unknown: role=%q label=%q", role, label)
	}
}
