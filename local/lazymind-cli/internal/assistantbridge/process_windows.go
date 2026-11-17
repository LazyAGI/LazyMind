//go:build windows

package assistantbridge

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

func detachedProcessAttributes() *syscall.SysProcAttr {
	flags := uint32(syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008)
	// The launcher owns its foreground MCP process, while this shared service
	// must survive the end of that MCP session.
	if os.Getenv("LAZYMIND_CONNECTOR_LAUNCHER_JOB") == "1" {
		flags |= windows.CREATE_BREAKAWAY_FROM_JOB
	}
	return &syscall.SysProcAttr{
		CreationFlags: flags,
		HideWindow:    true,
	}
}
