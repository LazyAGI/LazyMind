package chat

import (
	"context"
	"lazymind/core/common/orm"
	"lazymind/core/localworkspace"
	"testing"
)

func TestUnboundWorkspaceDraftContext(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	if !localworkspace.Enabled() {
		t.Fatal("local workspace runtime disabled")
	}
	db := orm.MigrateTestDB(t, &orm.UserChatSettings{})
	value, err := workspaceSnapshotForRequest(context.Background(), db.DB, "u1", map[string]any{})
	if err != nil || value == nil || value.WorkspaceID != "" || value.PermissionMode != localworkspace.PermissionAlwaysAsk {
		t.Fatalf("unbound draft context: %#v, %v", value, err)
	}
}
