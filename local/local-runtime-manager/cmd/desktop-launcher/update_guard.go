package main

import (
	"context"
	"path/filepath"
	"runtime"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

func checkPendingDesktopUpdate(launcherPath string) error {
	root, err := desktopupdate.UpdaterRoot()
	if err != nil {
		return err
	}
	resources := filepath.Join(filepath.Dir(launcherPath), "resources", "runtime")
	if runtime.GOOS == "darwin" {
		resources = filepath.Join(filepath.Dir(filepath.Dir(launcherPath)), "Resources", "runtime")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// The public entry checks the installation before starting Electron. The
	// internal entry then atomically registers its complete resolved data scope.
	return desktopupdate.CheckStartup(ctx, root, desktopupdate.UpdateScope{Installation: resources})
}
