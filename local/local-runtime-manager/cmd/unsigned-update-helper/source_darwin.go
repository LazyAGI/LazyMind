//go:build darwin && lazymind_unsigned_test

package main

/*
#include <libproc.h>
*/
import "C"

import (
	"errors"
	"path/filepath"
	"unsafe"

	update "github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

// The executable being run is the cached installer. Its parent is the source
// Electron process, whose OS process incarnation is also bound by the receipt.
func unsignedSourceApplication(paths update.UnsignedTestPaths, systemApplications string, actor update.ProcessIdentity) (string, error) {
	invalid := errors.New("unsigned_test_identity")
	alive, err := update.SameProcess(actor)
	if err != nil || !alive {
		return "", invalid
	}
	var buf [C.PROC_PIDPATHINFO_MAXSIZE]C.char
	if C.proc_pidpath(C.int(actor.PID), unsafe.Pointer(&buf[0]), C.uint32_t(len(buf))) <= 0 {
		return "", invalid
	}
	exe := C.GoString(&buf[0])
	app := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	alive, err = update.SameProcess(actor)
	if err != nil || !alive || !update.IsUnsignedApplication(app, paths, systemApplications) ||
		exe != filepath.Join(app, "Contents", "MacOS", "LazyMindApp") || update.NoUnsignedPathAliases(exe) != nil {
		return "", invalid
	}
	return app, nil
}
