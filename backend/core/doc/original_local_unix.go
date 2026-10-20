//go:build !windows

package doc

import (
	"os"
	"syscall"
)

func openLocalOriginal(path string) (*os.File, error) {
	// Nonblocking open prevents a substituted FIFO from hanging the MCP server.
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
