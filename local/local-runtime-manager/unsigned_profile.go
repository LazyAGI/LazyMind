package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

var resolveUnsignedProfile = desktopupdate.UnsignedPaths

func initializeUnsignedRuntime() error {
	if !desktopupdate.UnsignedTestBuild {
		return nil
	}
	p, err := resolveUnsignedProfile()
	if err != nil {
		return err
	}
	for key, value := range map[string]string{
		"LAZYMIND_HOST_HOME": p.Home, "LAZYMIND_HOME": p.CLI,
		"LAZYMIND_RUNTIME_ROOT": p.Runtime, "LAZYMIND_LOCAL_NETWORK_PROFILE": "localhost",
		"LAZYMIND_LOCAL_AUTO_LOGIN_ALLOW_LAN": "false",
	} {
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return nil
}

func validateUnsignedRuntime(cfg RuntimeConfig, paths RuntimePaths) error {
	if !desktopupdate.UnsignedTestBuild {
		return nil
	}
	p, err := resolveUnsignedProfile()
	if err != nil {
		return err
	}
	resources := filepath.Join(p.Application, "Contents", "Resources", "runtime")
	if cfg.Profile != "desktop" || paths.RuntimeRoot != p.Runtime || paths.ResourcesRoot != resources || paths.RepoRoot != filepath.Join(resources, "app") || paths.BuildRoot != resources || cfg.NetworkProfile != "localhost" {
		return fmt.Errorf("unsigned_test_runtime_scope")
	}
	if cfg.AuthService.DatabaseURL != sqliteURL(paths.AuthServiceDBPath) || cfg.UpdateStorage == nil {
		return fmt.Errorf("unsigned_test_storage_scope")
	}
	for _, file := range cfg.UpdateStorage.Databases {
		if !pathIsUnderRoot(file, p.Runtime) || desktopupdate.NoSymlinkAncestors(file) != nil {
			return fmt.Errorf("unsigned_test_storage_scope")
		}
	}
	for _, file := range []string{paths.FileWatcherBaseRoot, cfg.FileWatcher.WatchHostDir, cfg.ModeProfile.VectorStore.DBPath} {
		if !(pathIsUnderRoot(file, p.Runtime) || pathIsUnderRoot(file, p.Documents)) || desktopupdate.NoSymlinkAncestors(file) != nil {
			return fmt.Errorf("unsigned_test_storage_scope")
		}
	}
	for _, file := range cfg.FileWatcher.AllowedRoots {
		if !(pathIsUnderRoot(file, p.Runtime) || pathIsUnderRoot(file, p.Documents)) || desktopupdate.NoSymlinkAncestors(file) != nil {
			return fmt.Errorf("unsigned_test_storage_scope")
		}
	}
	return nil
}
