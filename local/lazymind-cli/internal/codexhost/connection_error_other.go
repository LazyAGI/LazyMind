//go:build !windows

package codexhost

import "syscall"

const connectionRefused = syscall.ECONNREFUSED
