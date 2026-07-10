//go:build linux

package main

import "testing"

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
