//go:build darwin && lazymind_unsigned_test

package desktopupdate

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// The updater journal owns the name of exactly one workspace on the volume
// containing this installation. Persist it before creating any target-volume
// files, then pin the filesystem object before placing payloads in it.
type unsignedWorkspaceLocation struct {
	Transaction, Application, Nonce string
	SourceSeal                      string
	SourceDevice, SourceInode       uint64
	Device, Inode                   uint64
}

type UnsignedWorkspace struct {
	Directory string
	stage     string
	location  unsignedWorkspaceLocation
	lock      *os.File
}

func (w *UnsignedWorkspace) Close() { releaseLock(w.lock) }

func workspaceLocation(stage, tx, app string, create bool) (unsignedWorkspaceLocation, string, error) {
	var loc unsignedWorkspaceLocation
	name := filepath.Join(stage, "workspace.json")
	if err := NoUnsignedPathAliases(name); err != nil {
		return loc, "", err
	}
	err := readMetadata(name, &loc)
	if os.IsNotExist(err) && create {
		var random [16]byte
		if _, err = rand.Read(random[:]); err != nil {
			return loc, "", err
		}
		loc = unsignedWorkspaceLocation{Transaction: tx, Application: app, Nonce: hex.EncodeToString(random[:])}
		raw, _ := json.Marshal(loc)
		err = atomicWrite(name, raw)
	}
	if err != nil {
		return loc, "", err
	}
	if loc.Transaction != tx || len(tx) != 32 || !digestPattern.MatchString(tx+tx) || len(loc.Nonce) != 32 ||
		!digestPattern.MatchString(loc.Nonce+loc.Nonce) || loc.Application != app || filepath.Base(app) != TestAppName+".app" || !filepath.IsAbs(app) || filepath.Clean(app) != app {
		return loc, "", failure("unsigned_test_workspace", nil)
	}
	dir := filepath.Join(filepath.Dir(app), ".lazymind-update-"+tx+"-"+loc.Nonce)
	if err := NoUnsignedPathAliases(dir); err != nil {
		return loc, "", err
	}
	return loc, dir, nil
}

func lockUnsignedWorkspace(stage string) (*os.File, error) {
	if err := NoUnsignedPathAliases(filepath.Join(stage, "workspace.lock")); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(stage, "workspace.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	ok, err := tryLock(f)
	if !ok || err != nil {
		f.Close()
		return nil, failure("update_transaction_busy", err)
	}
	return f, nil
}

func bindWorkspace(stage, dir string, loc unsignedWorkspaceLocation, create bool) (os.FileInfo, error) {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) && create && loc.Inode == 0 {
		if err = os.Mkdir(dir, 0700); err == nil {
			info, err = os.Lstat(dir)
		}
	}
	if err != nil {
		return nil, err
	}
	st := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || st.Uid != uint32(os.Getuid()) {
		return nil, failure("unsigned_test_workspace", nil)
	}
	if loc.Inode == 0 {
		entries, e := os.ReadDir(dir)
		if e != nil || len(entries) != 0 {
			return nil, failure("unsigned_test_workspace", e)
		}
		if !create {
			return info, nil
		}
		loc.Device, loc.Inode = uint64(st.Dev), st.Ino
		raw, _ := json.Marshal(loc)
		if err := atomicWrite(filepath.Join(stage, "workspace.json"), raw); err != nil {
			return nil, err
		}
	} else if uint64(st.Dev) != loc.Device || st.Ino != loc.Inode {
		return nil, failure("unsigned_test_workspace", nil)
	}
	marker := filepath.Join(dir, ".owner")
	if err := NoUnsignedPathAliases(marker); err != nil {
		return nil, err
	}
	want := []byte(loc.Transaction + "\n" + loc.Nonce + "\n")
	raw, err := os.ReadFile(marker)
	if os.IsNotExist(err) {
		entries, e := os.ReadDir(dir)
		if e != nil || len(entries) != 0 {
			return nil, failure("unsigned_test_workspace", e)
		}
		if !create {
			return info, nil
		}
		if e := atomicWrite(marker, want); e != nil {
			return nil, e
		}
	} else if err != nil || !bytes.Equal(raw, want) {
		return nil, failure("unsigned_test_workspace", err)
	}
	return info, nil
}

func OpenUnsignedWorkspace(p UnsignedTestPaths, tx string) (*UnsignedWorkspace, error) {
	if len(tx) != 32 || !digestPattern.MatchString(tx+tx) {
		return nil, failure("update_request_invalid", nil)
	}
	return openUnsignedWorkspace(p, tx, true)
}

func openUnsignedWorkspace(p UnsignedTestPaths, tx string, create bool) (*UnsignedWorkspace, error) {
	stage := filepath.Join(p.Updater, "installs", tx)
	if err := NoUnsignedPathAliases(stage); err != nil {
		return nil, err
	}
	if create {
		if err := ensureDirectory(stage); err != nil {
			return nil, err
		}
	}
	lock, err := lockUnsignedWorkspace(stage)
	if err != nil {
		return nil, err
	}
	loc, dir, err := workspaceLocation(stage, tx, p.Application, create)
	if err == nil {
		_, err = bindWorkspace(stage, dir, loc, create)
	}
	if err != nil {
		releaseLock(lock)
		return nil, err
	}
	// bindWorkspace may have just persisted the directory identity.
	if err := readMetadata(filepath.Join(stage, "workspace.json"), &loc); err != nil {
		releaseLock(lock)
		return nil, err
	}
	return &UnsignedWorkspace{Directory: dir, stage: stage, location: loc, lock: lock}, nil
}

// Bind the verified source before its process exits. A retry cannot replace
// this witness with a different app that happens to declare the same release.
func (w *UnsignedWorkspace) BindSource(seal string, info os.FileInfo) error {
	st := info.Sys().(*syscall.Stat_t)
	if w.location.SourceSeal != "" {
		if w.location.SourceSeal != seal || w.location.SourceDevice != uint64(st.Dev) || w.location.SourceInode != st.Ino {
			return failure("unsigned_test_source_changed", nil)
		}
		return nil
	}
	w.location.SourceSeal = seal
	w.location.SourceDevice = uint64(st.Dev)
	w.location.SourceInode = st.Ino
	raw, _ := json.Marshal(w.location)
	return atomicWrite(filepath.Join(w.stage, "workspace.json"), raw)
}

// Remove only the object whose device/inode and private marker match. Work
// through its opened root so replacing a pathname cannot redirect recursion.
func removeUnsignedWorkspace(stage, tx, app string) error {
	if _, err := os.Lstat(filepath.Join(stage, "workspace.json")); os.IsNotExist(err) {
		return nil
	}
	lock, err := lockUnsignedWorkspace(stage)
	if err != nil {
		return err
	}
	defer releaseLock(lock)
	loc, dir, err := workspaceLocation(stage, tx, app, false)
	if err != nil {
		return err
	}
	info, err := bindWorkspace(stage, dir, loc, false)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return failure("unsigned_test_workspace", err)
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	// If interrupted during cleanup, keep ownership proof until all payloads
	// are gone. Never remove a local undo copy while the app is missing.
	if _, e := os.Lstat(app); e != nil {
		return failure("unsigned_test_recovery_required", e)
	}
	for _, entry := range entries {
		if entry.Name() != ".owner" {
			if e := root.RemoveAll(entry.Name()); e != nil {
				return e
			}
		}
	}
	if e := root.Remove(".owner"); e != nil && !os.IsNotExist(e) {
		return e
	}
	current, err := os.Lstat(dir)
	if err != nil || !os.SameFile(info, current) {
		return failure("unsigned_test_workspace", err)
	}
	return os.Remove(dir) // an unexpected non-empty replacement is never removed
}

// Restore the exact moved source only into an absent canonical destination.
// RENAME_EXCL prevents overwriting a concurrent reinstall.
func RestoreUnsignedWorkspaceSource(w *UnsignedWorkspace, app string) error {
	if _, err := os.Lstat(app); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	undo := filepath.Join(w.Directory, "undo", TestAppName+".app")
	if err := NoUnsignedPathAliases(undo); err != nil {
		return err
	}
	return unix.RenamexNp(undo, app, unix.RENAME_EXCL)
}
