package agentexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Desktop owns its profile while the application is open. The bundled CLI
// checks package-writer locks, but does not itself reject a running Desktop.
func DSHDesktopRunning(ctx context.Context, cli string) (bool, error) {
	if runtime.GOOS != "darwin" || cli == "" {
		return false, nil
	}
	contents := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(cli)))))
	entries, err := os.ReadDir(filepath.Join(contents, "MacOS"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	output, err := Run(ctx, "/bin/ps", "-axo", "command=")
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		binary := filepath.Join(contents, "MacOS", entry.Name())
		for _, line := range strings.Split(output, "\n") {
			line = strings.TrimSpace(line)
			if (line == binary || strings.HasPrefix(line, binary+" ")) && !strings.Contains(line, "--expose-internals") {
				return true, nil
			}
		}
	}
	return false, nil
}

// FindDSHDesktopCLI resolves the launcher shipped by Desktop, never a similarly
// named npm executable from PATH. The override also accepts the application bundle.
func FindDSHDesktopCLI() (string, error) {
	if value := strings.TrimSpace(os.Getenv("LAZYMIND_DSH_DESKTOP_PATH")); value != "" {
		return ResolveDSHDesktopCLI(value)
	}
	if value := strings.TrimSpace(os.Getenv("LAZYMIND_DSH_PATH")); value != "" {
		if path, err := ResolveDSHDesktopCLI(value); err == nil {
			return path, nil
		}
	}
	if value, found, err := ExecutableBinding(DeepSeekHarnessCLI); err != nil {
		return "", err
	} else if found {
		if path, err := ResolveDSHDesktopCLI(value); err == nil {
			return path, nil
		}
	}
	for _, candidate := range platformDSHDesktopCandidates() {
		if path, err := ResolveDSHDesktopCLI(candidate); err == nil {
			return path, nil
		}
	}
	if runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		for _, directory := range []string{"/Applications", filepath.Join(home, "Applications")} {
			entries, _ := os.ReadDir(directory)
			for _, entry := range entries {
				if entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".app") {
					if path, err := ResolveDSHDesktopCLI(filepath.Join(directory, entry.Name())); err == nil {
						return path, nil
					}
				}
			}
		}
	}
	return "", errors.New("DSH Desktop bundled CLI was not found; install DeepSeek Harness Desktop or locate its bundled CLI")
}

func ResolveDSHDesktopCLI(value string) (string, error) {
	path, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	if strings.EqualFold(filepath.Ext(path), ".app") {
		path = filepath.Join(path, "Contents", "Resources", "runtime", "cli", "bin", "dsh")
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	resources := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(path))))
	if filepath.Base(filepath.Dir(path)) != "bin" ||
		filepath.Base(filepath.Dir(filepath.Dir(path))) != "cli" ||
		filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(path)))) != "runtime" ||
		(filepath.Base(path) != "dsh" && filepath.Base(path) != "dsh.cmd") {
		return "", errors.New("select the DSH Desktop bundled runtime/cli/bin/dsh launcher")
	}
	if info, err := os.Stat(filepath.Join(resources, "app.asar")); err != nil || info.IsDir() {
		return "", errors.New("DSH Desktop application resources are missing")
	}
	return ResolveExecutable(path)
}
