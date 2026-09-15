package localworkspace

import (
	"encoding/json"
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

func TestBuildRequestQueryKeepsOriginalAndAddsWorkspace(t *testing.T) {
	original := "请把 alpha 改为 beta。\n保留原格式。"
	snapshot := &ContextSnapshot{WorkspaceID: "lws_one", Root: "/tmp/project one",
		WorkspaceVersion: 2, PermissionMode: PermissionAlwaysAsk, PermissionVersion: 3}
	got := BuildRequestQuery(original, snapshot)
	for _, want := range []string{original, "/tmp/project one", "always_ask", "权限由工作区策略决定"} {
		if !strings.Contains(got, want) {
			t.Fatalf("query missing %q: %s", want, got)
		}
	}
	if got == original {
		t.Fatal("query was not enhanced")
	}
}

func TestSnapshotDoesNotUseLocalFSSourceProtocol(t *testing.T) {
	snapshot := snapshot(orm.LocalWorkspace{ID: "grant", CanonicalPath: "/tmp/project", Version: 2}, PermissionAllowAll, 4)
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "sources") || strings.Contains(string(encoded), "local-workspace:") {
		t.Fatalf("workspace snapshot must not use the source protocol: %s", encoded)
	}
}
