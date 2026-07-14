//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func directoryLinkTarget(path string) (string, bool) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", false
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil || attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
		return "", false
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	return filepath.Clean(target), true
}

func createDirectoryLink(target, link string) error {
	cmd := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", link, target)
	configureChildProcess(cmd, false)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mklink /J failed: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}
