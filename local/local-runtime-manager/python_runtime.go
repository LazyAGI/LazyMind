package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func ensureLocalPythonRuntime(ctx context.Context, runner CommandRunner, paths RuntimePaths, version string) (string, error) {
	uv, ok := uvCommand()
	if !ok {
		return "", fmt.Errorf("uv is required to provision local Python runtime; install uv or set %s", authServiceUVEnvVar)
	}
	var python string
	err := withFileLock(filepath.Join(paths.RunDir, "python-runtime.lock"), func() error {
		env := pythonRuntimeEnv(paths)
		install := Command{
			Name: uv,
			Args: []string{"python", "install", "--install-dir", paths.PythonRuntimeDir, version},
			Dir:  paths.RepoRoot,
			Env:  env,
		}
		if res, err := runner.Run(ctx, install); err != nil {
			return fmt.Errorf("install local Python %s failed: %w (%s)", version, err, strings.TrimSpace(res.Stderr))
		}
		find := Command{
			Name: uv,
			Args: []string{"python", "find", "--managed-python", "--no-python-downloads", "--resolve-links", version},
			Dir:  paths.RepoRoot,
			Env:  env,
		}
		res, err := runner.Run(ctx, find)
		if err != nil {
			return fmt.Errorf("find local Python %s failed: %w (%s)", version, err, strings.TrimSpace(res.Stderr))
		}
		python = strings.TrimSpace(res.Stdout)
		if python == "" {
			return fmt.Errorf("find local Python %s returned an empty path", version)
		}
		if err := ensurePathUnderRoot(python, paths.RuntimeRoot); err != nil {
			return fmt.Errorf("local Python %s is not self-contained: %w", version, err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return python, nil
}

func localPythonVenvArgs(python string, clear bool, venvDir string) []string {
	args := []string{
		"venv",
		"--managed-python",
		"--no-python-downloads",
		"--relocatable",
		"--seed",
		"--link-mode", "copy",
		"--python", python,
	}
	if clear {
		args = append(args, "--clear")
	}
	return append(args, venvDir)
}

func localPythonPipInstallArgs(python string, args ...string) []string {
	out := []string{"pip", "install", "--python", python, "--link-mode", "copy", "--strict"}
	return append(out, args...)
}

func pythonRuntimeEnv(paths RuntimePaths) []string {
	env := pythonDependencyCacheEnv(paths)
	env = append(env,
		"UV_PYTHON_INSTALL_DIR="+paths.PythonRuntimeDir,
		"UV_MANAGED_PYTHON=true",
	)
	return env
}

func uvCommand() (string, bool) {
	if uv := strings.TrimSpace(os.Getenv(authServiceUVEnvVar)); uv != "" {
		return uv, true
	}
	if uv := strings.TrimSpace(os.Getenv("UV")); uv != "" {
		return uv, true
	}
	if uv, err := exec.LookPath("uv"); err == nil {
		return uv, true
	}
	userUV := filepath.Join(hostHomeDir(), ".local", "bin", "uv")
	if info, err := os.Stat(userUV); err == nil && !info.IsDir() {
		return userUV, true
	}
	return "", false
}

func ensurePathUnderRoot(path string, root string) error {
	absPath, err := resolveExistingPathSymlinks(path)
	if err != nil {
		return err
	}
	absRoot, err := resolveExistingPathSymlinks(root)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return err
	}
	if rel == "." || (!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != "..") {
		return nil
	}
	return fmt.Errorf("%s is outside %s", absPath, absRoot)
}

func resolveExistingPathSymlinks(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved, nil
	}
	missing := []string{}
	current := abs
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			parts := append([]string{resolved}, reverseStrings(missing)...)
			return filepath.Join(parts...), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return abs, nil
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func reverseStrings(values []string) []string {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
	return values
}
