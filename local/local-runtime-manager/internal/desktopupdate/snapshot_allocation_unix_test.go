//go:build !windows

package desktopupdate

import (
	"os"
	"syscall"
	"testing"
)

func fixtureAllocatedBytes(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("allocation metadata unavailable")
	}
	return int64(stat.Blocks) * 512
}
