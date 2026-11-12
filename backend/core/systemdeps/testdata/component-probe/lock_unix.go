//go:build !windows

package main

import (
	"os"
	"syscall"
)

func canLock(filename string) bool {
	return withLock(filename, func() {})
}

func withLock(filename string, fn func()) bool {
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return false
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return false
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	fn()
	return true
}
