//go:build !windows

package connectorlauncher

import (
	"os"
	"syscall"
)

func forward(executable string, args []string) (int, error) {
	// Exec preserves the MCP process identity and its signal/stdio lifecycle.
	return 1, syscall.Exec(executable, append([]string{executable}, args...), os.Environ())
}
