package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

var resolveRuntimeRecoveryRoot = defaultRuntimeRecoveryRoot

func defaultRuntimeRecoveryRoot() (string, error) {
	return desktopupdate.UpdaterRoot()
}

func runtimeUpdateScope(cfg RuntimeConfig, paths RuntimePaths) (desktopupdate.UpdateScope, error) {
	scope := desktopupdate.UpdateScope{}
	if cfg.Profile == "desktop" {
		scope.Installation = paths.ResourcesRoot
	}
	roots := map[string]bool{}
	for _, path := range []string{paths.RuntimeRoot, paths.DataDir, paths.UploadRoot, paths.LazyLLMHome, cfg.ModeProfile.VectorStore.DBPath, paths.FileWatcherBaseRoot, paths.SubagentDataDir, paths.EvoDataDir} {
		if path != "" {
			if !filepath.IsAbs(path) {
				return scope, updateStateFailure("scope_invalid")
			}
			roots[filepath.Clean(path)] = true
		}
	}
	if cfg.UpdateStorage == nil {
		return scope, updateStateFailure("scope_invalid")
	}
	for family, path := range cfg.UpdateStorage.Databases {
		// Admission protects local files even for a runtime whose external
		// storage prevents automatic recovery. The separate update-state
		// measurement rejects unsupported layouts before offering an upgrade.
		if family == "auth" && path == "" {
			if updateAuthUsesExternalDatabase(cfg.AuthService.DatabaseURL) {
				continue
			}
			return scope, updateStateFailure("scope_invalid")
		}
		if family == "segments" && !strings.EqualFold(cfg.UpdateStorage.SegmentStore, "SQLiteStore") {
			continue
		}
		if !filepath.IsAbs(path) {
			return scope, updateStateFailure("scope_invalid")
		}
		for _, suffix := range []string{"", "-wal", "-shm"} {
			roots[filepath.Clean(path)+suffix] = true
		}
	}
	for path := range roots {
		scope.DataRoots = append(scope.DataRoots, path)
	}
	sort.Strings(scope.DataRoots)
	return scope, nil
}
func registerRuntimeUpdateLease(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) (*desktopupdate.StartupLease, error) {
	root, err := resolveRuntimeRecoveryRoot()
	if err != nil {
		return nil, err
	}
	scope, err := runtimeUpdateScope(cfg, paths)
	if err != nil {
		return nil, err
	}
	actor, err := desktopupdate.CurrentProcessIdentity()
	if err != nil {
		return nil, err
	}
	return desktopupdate.BeginStartup(ctx, root, scope, cfg.OwnerToken, "writer", actor)
}
func registerDesktopUpdateCoordinator(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if err := nativeShellStartup(ctx, cfg, paths); err != nil {
		return err
	}
	root, err := resolveRuntimeRecoveryRoot()
	if err != nil {
		return err
	}
	scope, err := runtimeUpdateScope(cfg, paths)
	if err != nil {
		return err
	}
	actor, err := desktopupdate.IdentifyProcess(os.Getppid())
	if err != nil {
		return err
	}
	_, err = desktopupdate.BeginStartup(ctx, root, scope, cfg.OwnerToken, "coordinator", actor)
	return err
}

func withRuntimeUpdateLease(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths, action func() error) error {
	lease, err := registerRuntimeUpdateLease(ctx, cfg, paths)
	if err != nil {
		return err
	}
	defer lease.Close()
	return action()
}
