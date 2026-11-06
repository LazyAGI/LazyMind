//go:build linux

package main

import (
	"os"
	"strings"
	"testing"
)

func TestLinuxProcessStatHandlesSpacesAndParentheses(t *testing.T) {
	fields := []string{"S", "100", "200"}
	for len(fields) < 19 {
		fields = append(fields, "0")
	}
	fields = append(fields, "123456")
	parent, group, identity := parseLinuxProcessStat("123 (a worker (name)) " + strings.Join(fields, " "))
	if parent != 100 || group != 200 || identity != 123456 {
		t.Fatalf("stat = %d/%d/%d", parent, group, identity)
	}
	if _, _, identity := parseLinuxProcessStat("malformed"); identity != 0 {
		t.Fatal("malformed stat accepted")
	}
}

func TestSplitProcCmdlinePreservesArgumentBoundaries(t *testing.T) {
	got := splitProcCmdline([]byte("python\x00-m\x00module with spaces\x00--url\x00http://127.0.0.1:18000/a//b\x00"))
	want := []string{"python", "-m", "module with spaces", "--url", "http://127.0.0.1:18000/a//b"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("arg %d = %q, want %q: %#v", i, got[i], want[i], got)
		}
	}
}

func TestDedupeProcessRecordsExcludesCurrentProcessGroup(t *testing.T) {
	paths := RuntimePaths{RepoRoot: "/repo", RuntimeRoot: "/runtime"}
	records := dedupeProcessRecords([]LocalProcessRecord{{
		Service:     "local-runtime-orphan",
		PID:         os.Getppid(),
		PGID:        processGroupID(os.Getpid()),
		RepoRoot:    paths.RepoRoot,
		RuntimeRoot: paths.RuntimeRoot,
	}}, paths)
	if len(records) != 0 {
		t.Fatalf("current process group was retained: %#v", records)
	}
}
