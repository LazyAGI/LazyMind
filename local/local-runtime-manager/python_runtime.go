package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Check resolved directories before uv can install through a stale link.
func validateLocalPythonPaths(paths RuntimePaths, venvs ...string) error {
	buildRoot, err := resolveExistingPathSymlinks(paths.BuildRoot)
	if err != nil {
		return err
	}
	platform := runtime.GOOS + "-" + runtime.GOARCH
	entries := []struct{ path, root string }{
		{paths.PythonRuntimeDir, filepath.Join(buildRoot, "runtimes", "python", platform)},
	}
	for _, venv := range venvs {
		entries = append(entries, struct{ path, root string }{venv, filepath.Join(buildRoot, "deps", "python", platform)})
	}
	for _, entry := range entries {
		resolved, err := resolveExistingPathSymlinks(entry.path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(entry.root, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("Python directory %s resolves to %s outside %s; use a dedicated %s environment (existing files were preserved)", entry.path, resolved, entry.root, platform)
		}
	}
	return nil
}

const pythonEnvironmentProbe = `import platform, ssl, struct, sys, sysconfig
from pathlib import Path

expected_os, expected_arch, venv, runtime_root, certificates = sys.argv[1:]
actual_os = {'win32': 'windows', 'darwin': 'darwin', 'linux': 'linux'}.get(sys.platform, sys.platform)
machine = platform.machine().lower()
actual_arch = {'x86_64': 'amd64', 'amd64': 'amd64', 'aarch64': 'arm64', 'arm64': 'arm64'}.get(machine, machine)
if actual_os == 'windows':
    build = sysconfig.get_platform()
    actual_arch = {'win-amd64': 'amd64', 'win-arm64': 'arm64', 'win32': '386'}.get(build, build)
if (actual_os, actual_arch) != (expected_os, expected_arch):
    raise RuntimeError(f'Python platform {actual_os}/{actual_arch} does not match {expected_os}/{expected_arch}')
if expected_arch in ('amd64', 'arm64') and struct.calcsize('P') != 8:
    raise RuntimeError('Python platform requires a 64-bit interpreter')
prefix = Path(sys.prefix).resolve()
if sys.prefix == sys.base_prefix:
    raise RuntimeError(f'Python {sys.executable} is not running in a virtual environment')
if prefix != Path(venv).resolve():
    raise RuntimeError(f'Python prefix {prefix} does not match venv {venv}')
if not Path(sys.base_prefix).resolve().is_relative_to(Path(runtime_root).resolve()):
    raise RuntimeError(f'Python base {sys.base_prefix} is outside runtime {runtime_root}')
if certificates == 'true':
    import certifi
    certificate = Path(certifi.where()).resolve()
    if not certificate.is_relative_to(prefix):
        raise RuntimeError(f'Certificate {certificate} is outside venv {prefix}')
    ssl.create_default_context(cafile=str(certificate))
    import httpx
    with httpx.Client():
        pass
`

func validatePythonEnvironment(ctx context.Context, runner CommandRunner, paths RuntimePaths, venv string, certificates bool) error {
	if err := validateLocalPythonPaths(paths, venv); err != nil {
		return err
	}
	python := venvExecutable(venv, "python")
	result, err := runner.Run(ctx, Command{
		Name: python,
		Args: []string{"-I", "-c", pythonEnvironmentProbe, runtime.GOOS, runtime.GOARCH, venv, paths.PythonRuntimeDir, strconv.FormatBool(certificates)},
		Dir:  paths.RepoRoot,
	})
	if err != nil {
		return fmt.Errorf("Python environment check failed for %s; prepare a dedicated %s/%s environment (existing files were preserved): %w (%s)", python, runtime.GOOS, runtime.GOARCH, err, strings.TrimSpace(result.Stderr))
	}
	return nil
}

func ensureLocalPythonRuntime(ctx context.Context, runner CommandRunner, paths RuntimePaths, version string) (string, error) {
	if err := validateLocalPythonPaths(paths); err != nil {
		return "", err
	}
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
		if err := ensurePathUnderRoot(python, paths.BuildRoot); err != nil {
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
	for _, configured := range []string{
		strings.TrimSpace(os.Getenv(authServiceUVEnvVar)),
		strings.TrimSpace(os.Getenv("UV")),
	} {
		if configured == "" {
			continue
		}
		if strings.ContainsRune(configured, os.PathSeparator) || filepath.IsAbs(configured) {
			if info, err := os.Stat(configured); err == nil && !info.IsDir() {
				return configured, true
			}
			return "", false
		}
		if _, err := exec.LookPath(configured); err == nil {
			return configured, true
		}
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
