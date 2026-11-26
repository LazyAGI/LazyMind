//go:build darwin && lazymind_unsigned_test

package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	update "github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
	"golang.org/x/sys/unix"
)

func writableInstallTarget(app string) error {
	if err := update.NoUnsignedPathAliases(app); err != nil {
		return err
	}
	// Darwin's extended access(2) flag _DELETE_OK checks removal rights on
	// the actual application, including its ACL, in addition to parent writes.
	const deleteOK = 1 << 12
	if err := unix.Access(app, deleteOK); err != nil {
		return err
	}
	return unix.Access(filepath.Dir(app), unix.W_OK|unix.X_OK)
}

func syncInstallDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// Create an actual private working directory beside the installation and
// exercise the filesystem operation that will replace the application.
func localInstallDirectory(p update.UnsignedTestPaths, tx string) (*update.UnsignedWorkspace, bool, error) {
	if err := writableInstallTarget(p.Application); err != nil {
		return nil, false, err
	}
	w, err := update.OpenUnsignedWorkspace(p, tx)
	if err != nil {
		return nil, false, err
	}
	probe, err := os.MkdirTemp(w.Directory, ".probe-")
	if err != nil {
		w.Close()
		return nil, false, err
	}
	defer os.RemoveAll(probe)
	a, b := filepath.Join(probe, "a"), filepath.Join(probe, "b")
	if err = os.Mkdir(a, 0700); err == nil {
		err = os.Mkdir(b, 0700)
	}
	if err == nil {
		err = unix.RenamexNp(a, b, unix.RENAME_SWAP)
	}
	if errors.Is(err, unix.ENOTSUP) {
		// HFS+ has exclusive rename but cannot exchange two directories.
		// Check the fallback on this actual volume before asking A to exit.
		err = unix.RenamexNp(a, filepath.Join(probe, "c"), unix.RENAME_EXCL)
		if err == nil {
			return w, false, nil
		}
	}
	if err != nil {
		w.Close()
		return nil, false, err
	}
	return w, true, nil
}

func sourceSeal(ctx context.Context, app string, cfg update.Config) (string, error) {
	if err := update.VerifyUnsignedMacBundle(ctx, app, cfg); err != nil {
		return "", err
	}
	raw, err := exec.CommandContext(ctx, "/usr/bin/codesign", "-dv", "--verbose=4", app).CombinedOutput()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if digest, ok := strings.CutPrefix(line, "CDHash="); ok {
			if bytes, e := hex.DecodeString(digest); e == nil && len(bytes) >= 20 {
				return digest, nil
			}
		}
	}
	return "", errors.New("unsigned_test_source_identity")
}

func saveSourceBackup(ctx context.Context, p update.UnsignedTestPaths, cfg update.Config, r *update.NativeRecord, seal string) error {
	stage := stagePath(p, r)
	previous := filepath.Join(stage, "previous")
	if err := update.NoUnsignedPathAliases(previous); err != nil {
		return err
	}
	verify := func(dir string) error {
		backup := filepath.Join(dir, update.TestAppName+".app")
		got, err := sourceSeal(ctx, backup, cfg)
		if err != nil {
			return err
		}
		if got != seal {
			return errors.New("unsigned_test_source_changed")
		}
		return nil
	}
	if _, err := os.Lstat(previous); err == nil {
		return verify(previous)
	} else if !os.IsNotExist(err) {
		return err
	}
	temp, err := os.MkdirTemp(stage, ".backup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	if err := exec.CommandContext(ctx, "/usr/bin/ditto", p.Application, filepath.Join(temp, update.TestAppName+".app")).Run(); err != nil {
		return err
	}
	if err := verify(temp); err != nil {
		return err
	}
	if err := os.Rename(temp, previous); err != nil {
		return err
	}
	return syncInstallDirectory(stage)
}

type localReplacement struct {
	workspace  *update.UnsignedWorkspace
	swap       bool
	sourceSeal string
	source     os.FileInfo
}

func (r *localReplacement) close() { r.workspace.Close() }

func prepareInstall(ctx context.Context, p update.UnsignedTestPaths, cfg update.Config, r *update.NativeRecord) (*localReplacement, error) {
	w, swap, err := localInstallDirectory(p, r.TransactionID)
	if err != nil {
		return nil, err
	}
	seal, err := sourceSeal(ctx, p.Application, cfg)
	if err != nil {
		w.Close()
		return nil, err
	}
	info, err := os.Stat(p.Application)
	if err != nil {
		w.Close()
		return nil, err
	}
	if err := w.BindSource(seal, info); err != nil {
		w.Close()
		return nil, err
	}
	plan := &localReplacement{workspace: w, swap: swap, sourceSeal: seal, source: info}
	if _, err = candidate(ctx, p, cfg, r, filepath.Join(w.Directory, "target")); err == nil {
		err = saveSourceBackup(ctx, p, cfg, r, seal)
	}
	if err != nil {
		plan.close()
		return nil, err
	}
	return plan, nil
}

func install(ctx context.Context, p update.UnsignedTestPaths, cfg update.Config, r *update.NativeRecord, parent update.ProcessIdentity, out io.Writer) error {
	plan, err := prepareInstall(ctx, p, cfg, r)
	if err != nil {
		return err
	}
	defer plan.close()
	return plan.dispatch(ctx, p, cfg, r, parent, out, nil)
}

func (plan *localReplacement) dispatch(ctx context.Context, p update.UnsignedTestPaths, cfg update.Config, r *update.NativeRecord, parent update.ProcessIdentity, out io.Writer, revalidate func() error) error {
	if err := writableInstallTarget(p.Application); err != nil {
		return err
	}
	if err := json.NewEncoder(out).Encode(map[string]string{"status": "accepted"}); err != nil {
		return err
	}
	for {
		alive, err := update.SameProcess(parent)
		if err != nil {
			return err
		}
		if !alive {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	// Re-extract the signed bytes after source exit. Never install mutable
	// pre-ACK staging. The final swap is on the app volume, including when
	// recovery data and the already verified backup live on another volume.
	app, err := candidate(ctx, p, cfg, r, filepath.Join(plan.workspace.Directory, "target"))
	if err != nil {
		return err
	}
	info, err := os.Stat(p.Application)
	if err != nil {
		return err
	}
	if !os.SameFile(plan.source, info) {
		return errors.New("unsigned_test_source_changed")
	}
	seal, err := sourceSeal(ctx, p.Application, cfg)
	if err != nil {
		return err
	}
	if seal != plan.sourceSeal {
		return errors.New("unsigned_test_source_changed")
	}
	backup := filepath.Join(stagePath(p, r), "previous", update.TestAppName+".app")
	saved, err := sourceSeal(ctx, backup, cfg)
	if err != nil {
		return err
	}
	if saved != seal {
		return errors.New("unsigned_test_source_changed")
	}
	if revalidate != nil {
		if err := revalidate(); err != nil {
			return err
		}
	}
	if err := writableInstallTarget(p.Application); err != nil {
		return err
	}
	if plan.swap {
		if err := unix.RenamexNp(app, p.Application, unix.RENAME_SWAP); err != nil {
			return err
		}
	} else {
		undoDir := filepath.Join(plan.workspace.Directory, "undo")
		if err := update.NoUnsignedPathAliases(undoDir); err != nil {
			return err
		}
		if err := os.MkdirAll(undoDir, 0700); err != nil {
			return err
		}
		undo := filepath.Join(undoDir, update.TestAppName+".app")
		if revalidate != nil {
			// The guard is a short-lived child of this cached installer.
			// Unexpected process exit closes its pipe and enables recovery.
			finishGuard, e := watchInterruptedReplacement(r.TransactionID)
			if e != nil {
				return e
			}
			defer func() {
				_, e := os.Lstat(p.Application)
				finishGuard(!os.IsNotExist(e))
			}()
		}
		if err := unix.RenamexNp(p.Application, undo, unix.RENAME_EXCL); err != nil {
			return err
		}
		// From this point on, the transaction locator directly identifies the
		// original signed directory, even if this helper is interrupted.
		err = syncInstallDirectory(undoDir)
		if err == nil {
			err = syncInstallDirectory(filepath.Dir(p.Application))
		}
		if err == nil {
			err = unix.RenamexNp(app, p.Application, unix.RENAME_EXCL)
		}
		if err != nil {
			return errors.Join(err, update.RestoreUnsignedWorkspaceSource(plan.workspace, p.Application))
		}
	}
	return syncInstallDirectory(filepath.Dir(p.Application))
}

func watchInterruptedReplacement(tx string) (func(bool), error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, "recover-on-exit", tx)
	cmd.Stdin = reader
	err = cmd.Start()
	_ = reader.Close()
	if err != nil {
		writer.Close()
		return nil, err
	}
	go func() { _ = cmd.Wait() }()
	// A byte (rather than EOF) disarms normal completion. Involuntary
	// termination closes the descriptor without that byte.
	return func(disarm bool) {
		if disarm {
			_, _ = writer.Write([]byte{1})
		}
		_ = writer.Close()
	}, nil
}
