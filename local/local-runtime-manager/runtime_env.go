package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const localHostHomeEnvVar = "LAZYMIND_HOST_HOME"

func scopedRuntimeEnv(paths RuntimePaths, home string) []string {
	return []string{
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + paths.ConfigDir,
		"XDG_CACHE_HOME=" + paths.XDGCacheDir,
		"XDG_STATE_HOME=" + paths.XDGStateDir,
		localHostHomeEnvVar + "=" + hostHomeDir(),
	}
}

func processComposeRuntimeEnv(paths RuntimePaths) []string {
	return scopedRuntimeEnv(paths, paths.ProcessComposeHome)
}

func serviceRuntimeEnv(paths RuntimePaths) []string {
	return scopedRuntimeEnv(paths, paths.ServiceHome)
}

func goToolEnv(paths RuntimePaths) []string {
	home := hostHomeDir()
	goCache := cleanHostCacheEnv("GOCACHE", paths, defaultGoBuildCache(home))
	goModCache := cleanHostCacheEnv("GOMODCACHE", paths, filepath.Join(home, "go", "pkg", "mod"))
	return append(hostToolEnv(paths),
		"GOCACHE="+goCache,
		"GOMODCACHE="+goModCache,
	)
}

func hostToolEnv(paths RuntimePaths) []string {
	home := hostHomeDir()
	return []string{
		localHostHomeEnvVar + "=" + home,
		"HOME=" + home,
		"XDG_CACHE_HOME=" + defaultHostCacheDir(home),
	}
}

func hostHomeDir() string {
	for _, key := range []string{localHostHomeEnvVar, "HOME", "USERPROFILE"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return filepath.Clean(value)
		}
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Clean(home)
	}
	return "."
}

func cleanHostCacheEnv(key string, paths RuntimePaths, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return filepath.Clean(fallback)
	}
	value = filepath.Clean(value)
	if pathIsUnderRoot(value, paths.RuntimeRoot) {
		return filepath.Clean(fallback)
	}
	return value
}

func defaultGoBuildCache(home string) string {
	return filepath.Join(defaultHostCacheDir(home), "go-build")
}

func defaultHostCacheDir(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Caches")
	case "windows":
		if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" && !pathIsUnderRoot(localAppData, home) {
			return filepath.Clean(localAppData)
		}
		return filepath.Join(home, "AppData", "Local")
	default:
		return filepath.Join(home, ".cache")
	}
}

func pathIsUnderRoot(path string, root string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != "..")
}
