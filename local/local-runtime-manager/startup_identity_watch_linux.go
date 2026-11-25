package main

import (
	"io/fs"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// The portable regression host uses byte snapshots plus inotify.
func startupObjectGeneration(string) (uint32, error) { return 0, nil }

// A pair of stat/content samples cannot detect a file moved away and back
// within one filesystem timestamp tick. Leave all events queued; any change
// (including a move, watch removal, or overflow) invalidates this one check.
func watchStartupIdentityInputs(paths RuntimePaths) (func() bool, func(), error) {
	bundle := filepath.Dir(filepath.Dir(filepath.Dir(paths.ResourcesRoot)))
	if !filepath.IsAbs(bundle) || filepath.Ext(bundle) != ".app" {
		return nil, nil, fs.ErrInvalid
	}
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, nil, err
	}
	const events = unix.IN_MODIFY | unix.IN_ATTRIB | unix.IN_CLOSE_WRITE |
		unix.IN_MOVED_FROM | unix.IN_MOVED_TO | unix.IN_CREATE | unix.IN_DELETE |
		unix.IN_DELETE_SELF | unix.IN_MOVE_SELF
	err = filepath.WalkDir(bundle, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return err
		}
		_, err = unix.InotifyAddWatch(fd, path, events|unix.IN_DONT_FOLLOW|unix.IN_ONLYDIR)
		return err
	})
	if err != nil {
		unix.Close(fd)
		return nil, nil, err
	}
	unchanged := func() bool {
		events := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(events, 0)
		return n == 0 && err == nil
	}
	return unchanged, func() { unix.Close(fd) }, nil
}
