package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func identityPeerPID(fd uintptr) (int, error) {
	cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return 0, err
	}
	return int(cred.Pid), nil
}

func identityPeerParent(pid int) (int, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	// The parenthesized command can itself contain spaces and parentheses.
	end := strings.LastIndexByte(string(raw), ')')
	fields := strings.Fields(string(raw[end+1:]))
	if end < 0 || len(fields) < 2 || fields[0] == "Z" {
		return 0, os.ErrInvalid
	}
	return strconv.Atoi(fields[1])
}

func identityPeerImagePath(pid int) (string, error) {
	// Stat the mapped executable even when its former pathname was replaced.
	return fmt.Sprintf("/proc/%d/exe", pid), nil
}
