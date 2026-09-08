package localworkspace

import (
	"strings"
	"testing"
)

func TestBuildRequestQueryKeepsOriginalAndAddsWorkspace(t *testing.T) {
	original := "请把 alpha 改为 beta。\n保留原格式。"
	snapshot := &ContextSnapshot{WorkspaceID: "lws_one", Root: "/tmp/project one",
		WorkspaceVersion: 2, PermissionMode: PermissionAlwaysAsk, PermissionVersion: 3}
	got := BuildRequestQuery(original, snapshot)
	for _, want := range []string{original, "/tmp/project one", "always_ask", "ask_user"} {
		if !strings.Contains(got, want) {
			t.Fatalf("query missing %q: %s", want, got)
		}
	}
	if got == original {
		t.Fatal("query was not enhanced")
	}
}

func TestSnapshotUsesExistingLocalFSContract(t *testing.T) {
	snapshot := snapshotForValues("grant", "/tmp/project", 2, PermissionAllowAll, 4)
	if len(snapshot.Sources) != 1 {
		t.Fatalf("sources=%v", snapshot.Sources)
	}
	source := snapshot.Sources[0]
	if source["source_id"] != "local-workspace:grant" {
		t.Fatalf("source=%v", source)
	}
	paths, ok := source["paths"].([]string)
	if !ok || len(paths) != 1 || paths[0] != "/tmp/project" {
		t.Fatalf("paths=%T %v", source["paths"], source["paths"])
	}
	exts, ok := source["file_extensions"].([]string)
	if !ok || len(exts) == 0 {
		t.Fatalf("extensions=%T %v", source["file_extensions"], source["file_extensions"])
	}
	for i, ext := range exts {
		if strings.HasPrefix(ext, "*") || (i > 0 && exts[i-1] >= ext) {
			t.Fatalf("extensions not sorted/plain: %v", exts)
		}
	}
}
