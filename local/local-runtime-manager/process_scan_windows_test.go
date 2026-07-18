//go:build windows

package main

import "testing"

func TestExcludeProcessTreeExcludesDesktopOwnerDescendantsOnly(t *testing.T) {
	parents := map[int]int{
		100: 1,
		110: 100,
		111: 110,
		120: 100,
		200: 1,
		210: 200,
	}
	excluded := map[int]bool{}
	excludeProcessTree(excluded, parents, 100)
	for _, pid := range []int{100, 110, 111, 120} {
		if !excluded[pid] {
			t.Fatalf("owner process tree PID %d was not excluded", pid)
		}
	}
	for _, pid := range []int{1, 200, 210} {
		if excluded[pid] {
			t.Fatalf("unrelated PID %d was excluded", pid)
		}
	}
}
