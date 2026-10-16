package codexruntime

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"lazymind/agentconnector/internal/localfile"
)

//go:embed assets/*
var runtimeAssets embed.FS

// Include native code in the release identity: installed helpers are immutable copies.
//
//go:embed platform.go platform_unix.go platform_windows.go coordinator.go listener_unix.go listener_windows.go layout.go shared.go launch.go status.go
var nativeSources embed.FS

// BuildID makes a changed runtime an upgrade, without overwriting live files.
func BuildID() string {
	hash := sha256.New()
	_ = fs.WalkDir(runtimeAssets, "assets", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := runtimeAssets.ReadFile(name)
		if err != nil {
			return err
		}
		hash.Write([]byte(name))
		hash.Write([]byte{0})
		hash.Write(data)
		return nil
	})
	_ = fs.WalkDir(nativeSources, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := nativeSources.ReadFile(name)
		if err != nil {
			return err
		}
		hash.Write([]byte(name))
		hash.Write([]byte{0})
		hash.Write(data)
		return nil
	})
	return hex.EncodeToString(hash.Sum(nil))
}

// WithInstallation prepares shared mode before touching the plugin, and activates
// only after plugin installation succeeds. Other platforms keep the queue path.
func WithInstallation(ctx context.Context, binary, profile string, installPlugin func() error) error {
	if !Supported() {
		return installPlugin()
	}
	app, node, err := desktopRuntime(binary)
	if err != nil {
		return err
	}
	return withInstaller(ctx, node, func(i *installer) error {
		raw, err := i.run(ctx, "prepare", "--profile", profile, "--app", app, "--build", BuildID())
		if err != nil {
			return err
		}
		var plan struct {
			Config   string `json:"config"`
			Previous string `json:"previous"`
		}
		if err := json.Unmarshal(raw, &plan); err != nil || !filepath.IsAbs(plan.Config) {
			return errors.New("invalid shared installation plan")
		}
		if err := installPlugin(); err != nil {
			return err
		}
		_, err = i.run(ctx, "activate", "--profile", profile, "--config", plan.Config, "--previous", plan.Previous, "--current-cli", binary)
		return err
	})
}

// Disable restores startup settings before the caller removes the plugin.
// It never stops an App, backend or user task, or deletes an active release.
func Disable(ctx context.Context, binary, profile string) error {
	if !Supported() {
		return nil
	}
	if _, err := Read(profile); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(profile, RegistrationName)); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	_, node, err := desktopRuntime(binary)
	if err != nil {
		node, err = exec.LookPath("node")
		if err != nil {
			return errors.New("Node is required to restore shared Desktop settings; restore Codex or install Node and retry")
		}
	}
	return withInstaller(ctx, node, func(i *installer) error {
		_, err := i.run(ctx, "disable", "--profile", profile, "--current-cli", binary)
		return err
	})
}

func desktopRuntime(binary string) (string, string, error) {
	if runtime.GOOS == "windows" {
		return windowsDesktopLayout(binary)
	}
	cli, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return "", "", err
	}
	for dir := filepath.Dir(cli); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		expected := filepath.Join(dir, "Contents", "Resources", "codex-cli", "CodexCLI.app", "Contents", "MacOS", "codex")
		if strings.HasSuffix(dir, ".app") && cli == expected {
			node := filepath.Join(dir, "Contents", "Resources", "cua_node", "bin", "node")
			if info, err := os.Stat(node); err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
				return dir, node, nil
			}
		}
	}
	return "", "", errors.New("共享模式需要受支持的 Codex Desktop 内置 CLI；请在 LazyMind 中选择桌面版 Codex 后重试")
}

type installer struct{ root, bundle, node string }

func withInstaller(ctx context.Context, node string, action func(*installer) error) error {
	user, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	root := filepath.Join(user, "Library", "Application Support", "LazyMind", "codex-desktop")
	if runtime.GOOS == "windows" {
		base, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		root = filepath.Join(base, "LazyMind", "codex-desktop")
	}
	if err := privateDirectory(root); err != nil {
		return err
	}
	unlock, err := localfile.Lock(filepath.Join(root, "installation.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	bundle, err := materialize(root)
	if err != nil {
		return err
	}
	return action(&installer{root: root, bundle: bundle, node: node})
}

func materialize(root string) (string, error) {
	destination := filepath.Join(root, "bundle-"+BuildID()[:20])
	if _, err := os.Lstat(destination); err == nil {
		if err := verifyBundle(destination); err != nil {
			return "", err
		}
		return destination, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	temporary, err := os.MkdirTemp(root, ".bundle-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(temporary)
	if err := makePrivate(temporary); err != nil {
		return "", err
	}
	entries, err := fs.ReadDir(runtimeAssets, "assets")
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		data, err := runtimeAssets.ReadFile("assets/" + entry.Name())
		if err != nil {
			return "", err
		}
		file := filepath.Join(temporary, entry.Name())
		if err := os.WriteFile(file, data, 0600); err != nil {
			return "", err
		}
		if err := makePrivate(file); err != nil {
			return "", err
		}
	}
	if err := os.Rename(temporary, destination); err != nil {
		return "", err
	}
	return destination, nil
}

func verifyBundle(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || checkPrivate(directory) != nil {
		return errors.New("unsafe shared runtime bundle directory")
	}
	entries, err := fs.ReadDir(runtimeAssets, "assets")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := filepath.Join(directory, entry.Name())
		info, err := os.Lstat(name)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || checkPrivate(name) != nil {
			return fmt.Errorf("unsafe shared runtime asset: %s", entry.Name())
		}
		actual, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		expected, _ := runtimeAssets.ReadFile("assets/" + entry.Name())
		if sha256.Sum256(actual) != sha256.Sum256(expected) {
			return fmt.Errorf("shared runtime asset changed: %s", entry.Name())
		}
	}
	return nil
}

func (i *installer) run(ctx context.Context, action string, args ...string) ([]byte, error) {
	operation, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	command := exec.CommandContext(operation, i.node, append([]string{filepath.Join(i.bundle, "install.mjs"), action, "--root", i.root}, args...)...)
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	command.Env = append(os.Environ(), "LAZYMIND_RUNTIME_HELPER="+self)
	output, err := command.Output()
	if err != nil {
		if failure, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("configure shared Codex: %s", strings.TrimSpace(string(failure.Stderr)))
		}
		return nil, fmt.Errorf("configure shared Codex: %w", err)
	}
	return output, nil
}
