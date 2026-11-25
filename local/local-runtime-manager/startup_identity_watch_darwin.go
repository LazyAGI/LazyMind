package main

/*
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <sys/attr.h>
#include <sys/xattr.h>
#include <unistd.h>

// getattrlist(2) guarantees equal nonzero generations for unchanged object
// data. A zero or missing count (including a writable mapping) is unusable.
static uint32_t startup_object_generation(const char *path) {
	struct attrlist query = {
		.bitmapcount = ATTR_BIT_MAP_COUNT,
		.commonattr = ATTR_CMN_RETURNED_ATTRS | ATTR_CMN_GEN_COUNT
	};
	struct {
		uint32_t length;
		attribute_set_t returned;
		uint32_t generation;
	} result = {0};
	if (getattrlist(path, &query, &result, sizeof(result), FSOPT_ATTR_CMN_EXTENDED | FSOPT_NOFOLLOW) != 0 ||
	    result.length != sizeof(result) || !(result.returned.commonattr & ATTR_CMN_GEN_COUNT) || !result.generation)
		return 0;

	// Detached signatures and resource forks live outside the data fork.
	// A long/unreadable attribute list also requires independent verification.
	char names[1024];
	ssize_t count = listxattr(path, names, sizeof(names), XATTR_NOFOLLOW);
	if (count < 0) return 0;
	for (const char *p = names; p < names + count; p += strlen(p) + 1) {
		if (strncmp(p, "com.apple.cs.", sizeof("com.apple.cs.") - 1) == 0 ||
		    strcmp(p, XATTR_RESOURCEFORK_NAME) == 0 || strcmp(p, XATTR_FINDERINFO_NAME) == 0)
			return 0;
	}
	return result.generation;
}
*/
import "C"

import (
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func startupObjectGeneration(path string) (uint32, error) {
	name := C.CString(path)
	defer C.free(unsafe.Pointer(name))
	generation := uint32(C.startup_object_generation(name))
	if generation == 0 {
		return 0, errStartupObservationUnavailable
	}
	return generation, nil
}

// A bundle can be moved away and back without its own data generation
// changing. Keep a kernel vnode notification for each directory in its path,
// including symlink objects and the resolved route to the same bundle.
func watchStartupIdentityInputs(paths RuntimePaths) (func() bool, func(), error) {
	resources := filepath.Dir(paths.ResourcesRoot)
	contents := filepath.Dir(resources)
	bundle := filepath.Dir(contents)
	if !filepath.IsAbs(bundle) || filepath.Ext(bundle) != ".app" ||
		filepath.Base(contents) != "Contents" || filepath.Base(resources) != "Resources" {
		return nil, nil, errStartupObservationUnavailable
	}
	resolved, err := filepath.EvalSymlinks(bundle)
	if err != nil {
		return nil, nil, errStartupObservationUnavailable
	}
	var fs unix.Statfs_t
	if err := unix.Statfs(resolved, &fs); err != nil || fs.Flags&unix.MNT_LOCAL == 0 {
		return nil, nil, errStartupObservationUnavailable
	}

	syscall.ForkLock.RLock()
	queue, err := unix.Kqueue()
	if err == nil {
		unix.CloseOnExec(queue)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, nil, errStartupObservationUnavailable
	}
	fds := make(map[string]int)
	var mu sync.Mutex
	valid := true
	release := func() {
		mu.Lock()
		defer mu.Unlock()
		if queue >= 0 {
			unix.Close(queue)
			queue = -1
			for _, fd := range fds {
				unix.Close(fd)
			}
		}
	}
	unchanged := func() bool {
		mu.Lock()
		defer mu.Unlock()
		if queue < 0 || !valid {
			return false
		}
		var changes [1]unix.Kevent_t
		n, err := unix.Kevent(queue, nil, changes[:], &unix.Timespec{})
		valid = err == nil && n == 0
		return valid
	}

	armed := false
	defer func() {
		if !armed {
			release()
		}
	}()
	routes := []string{bundle, resolved}
	for i := 0; i < len(routes); i++ {
		name := routes[i]
		for path := name; ; path = filepath.Dir(path) {
			if _, ok := fds[path]; ok {
				break
			}
			fd, err := unix.Open(path, unix.O_EVTONLY|unix.O_SYMLINK|unix.O_CLOEXEC, 0)
			if err != nil {
				return nil, nil, errStartupObservationUnavailable
			}
			fds[path] = fd
			change := unix.Kevent_t{Ident: uint64(fd), Filter: unix.EVFILT_VNODE, Flags: unix.EV_ADD | unix.EV_CLEAR,
				Fflags: unix.NOTE_DELETE | unix.NOTE_RENAME | unix.NOTE_REVOKE}
			if _, err := unix.Kevent(queue, []unix.Kevent_t{change}, nil, &unix.Timespec{}); err != nil {
				return nil, nil, errStartupObservationUnavailable
			}
			var pinned unix.Stat_t
			if unix.Fstat(fd, &pinned) != nil {
				return nil, nil, errStartupObservationUnavailable
			}
			if pinned.Mode&unix.S_IFMT == unix.S_IFLNK {
				// Pin each alias's destination, including ordinary /var and /tmp.
				// A target containing further links needs independent verification.
				dest, err := os.Readlink(path)
				if err != nil || filepath.Clean(dest) != dest {
					return nil, nil, errStartupObservationUnavailable
				}
				if !filepath.IsAbs(dest) {
					parent, err := filepath.EvalSymlinks(filepath.Dir(path))
					if err != nil {
						return nil, nil, errStartupObservationUnavailable
					}
					dest = filepath.Join(parent, dest)
				}
				if canonical, err := filepath.EvalSymlinks(dest); err != nil || canonical != dest {
					return nil, nil, errStartupObservationUnavailable
				}
				routes = append(routes, dest)
			}
		}
	}
	// All watches are now armed. Ensure that none of the paths changed between
	// opening its descriptor and registering the kernel notification.
	for path, fd := range fds {
		var pinned, current unix.Stat_t
		if unix.Fstat(fd, &pinned) != nil || unix.Lstat(path, &current) != nil ||
			pinned.Dev != current.Dev || pinned.Ino != current.Ino {
			return nil, nil, errStartupObservationUnavailable
		}
	}
	if current, err := filepath.EvalSymlinks(bundle); err != nil || current != resolved || !unchanged() {
		return nil, nil, errStartupObservationUnavailable
	}
	armed = true
	return unchanged, release, nil
}
