//go:build windows

package main

import (
	"os"
	"strconv"
	"testing"
)

func TestWindowsInventoryContainsCurrentIdentity(t *testing.T) {
	// Owner exclusions belong to the common selector, so inventory can still
	// verify registered services beneath the Desktop process.
	t.Setenv(desktopOwnerPIDEnvVar, strconv.Itoa(os.Getpid()))
	inventory, err := scanLocalRuntimeProcesses(RuntimePaths{})
	if err != nil {
		t.Fatal(err)
	}
	for _, process := range inventory {
		if process.PID == os.Getpid() {
			if process.StartID == 0 || process.StartID != processStartIdentity(os.Getpid()) || process.Executable == "" {
				t.Fatalf("incomplete process observation: %+v", process)
			}
			return
		}
	}
	t.Fatal("current process omitted from raw inventory")
}
