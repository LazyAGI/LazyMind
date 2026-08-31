package agentexec

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"lazymind/agentconnector/internal/localfile"
)

type BindingTarget string

const (
	CodexCLI         BindingTarget = "codex-cli"
	CodexDesktop     BindingTarget = "codex-desktop"
	CursorCLI        BindingTarget = "cursor-cli"
	CodeBuddyCLI     BindingTarget = "codebuddy-cli"
	CursorDesktop    BindingTarget = "cursor-desktop"
	WorkBuddyDesktop BindingTarget = "workbuddy-desktop"
	RaccoonDesktop   BindingTarget = "raccoon-desktop"
	TRAEWorkDesktop  BindingTarget = "traework-desktop"
	bindingsVersion                = 1
)

var (
	bindingTargets = map[BindingTarget]bool{
		CodexCLI: true, CursorCLI: true, CodeBuddyCLI: true, CodexDesktop: true,
		CursorDesktop: true, WorkBuddyDesktop: true, RaccoonDesktop: true,
		TRAEWorkDesktop: true,
	}
	bindingsMu sync.Mutex
)

type bindingsDocument struct {
	Version int                      `json:"version"`
	Paths   map[BindingTarget]string `json:"paths"`
}

func ExecutableBindings() (map[BindingTarget]string, error) {
	bindingsMu.Lock()
	defer bindingsMu.Unlock()
	document, err := readBindings()
	if err != nil {
		return nil, err
	}
	result := make(map[BindingTarget]string, len(document.Paths))
	for target, path := range document.Paths {
		result[target] = path
	}
	return result, nil
}

func ExecutableBinding(target BindingTarget) (string, bool, error) {
	if err := validateBindingTarget(target); err != nil {
		return "", false, err
	}
	bindings, err := ExecutableBindings()
	if err != nil {
		return "", false, err
	}
	path, found := bindings[target]
	return path, found, nil
}

func SetExecutableBinding(target BindingTarget, path string) (string, error) {
	if err := validateBindingTarget(target); err != nil {
		return "", err
	}
	resolved, err := resolveBindingExecutable(target, path)
	if err != nil {
		return "", fmt.Errorf("resolve %s binding: %w", target, err)
	}
	bindingsMu.Lock()
	defer bindingsMu.Unlock()
	unlock, err := lockBindings()
	if err != nil {
		return "", err
	}
	defer unlock()
	document, err := readBindings()
	if err != nil {
		return "", err
	}
	document.Paths[target] = resolved
	if err := writeBindings(document); err != nil {
		return "", err
	}
	return resolved, nil
}

func resolveBindingExecutable(target BindingTarget, path string) (string, error) {
	switch target {
	case CodexCLI, CursorCLI, CodeBuddyCLI:
		return ResolveRunnable(path)
	default:
		return ResolveExecutable(path)
	}
}

func ClearExecutableBinding(target BindingTarget) error {
	if err := validateBindingTarget(target); err != nil {
		return err
	}
	bindingsMu.Lock()
	defer bindingsMu.Unlock()
	unlock, err := lockBindings()
	if err != nil {
		return err
	}
	defer unlock()
	document, err := readBindings()
	if err != nil {
		return err
	}
	if _, found := document.Paths[target]; !found {
		return nil
	}
	delete(document.Paths, target)
	return writeBindings(document)
}

func configuredExecutable(explicit, environment string, target BindingTarget) (string, error) {
	if value := strings.TrimSpace(explicit); value != "" {
		return value, nil
	}
	if environment != "" {
		if value := strings.TrimSpace(os.Getenv(environment)); value != "" {
			return value, nil
		}
	}
	value, _, err := ExecutableBinding(target)
	return value, err
}

func bindingsPath() (string, error) {
	home, err := LazyMindHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "agent-bindings.json"), nil
}

func lockBindings() (func(), error) {
	path, err := bindingsPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return localfile.Lock(path + ".lock")
}

func readBindings() (bindingsDocument, error) {
	document := bindingsDocument{Version: bindingsVersion, Paths: map[BindingTarget]string{}}
	path, err := bindingsPath()
	if err != nil {
		return document, err
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return document, nil
	}
	if err != nil {
		return document, err
	}
	if err := json.Unmarshal(body, &document); err != nil {
		return document, fmt.Errorf("decode Agent bindings: %w", err)
	}
	if document.Version != bindingsVersion {
		return document, fmt.Errorf("unsupported Agent bindings version %d", document.Version)
	}
	if document.Paths == nil {
		document.Paths = map[BindingTarget]string{}
	}
	for target := range document.Paths {
		if err := validateBindingTarget(target); err != nil {
			return document, err
		}
	}
	return document, nil
}

func writeBindings(document bindingsDocument) error {
	path, err := bindingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "agent-bindings.*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(body, '\n')); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := localfile.Replace(temporaryPath, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func validateBindingTarget(target BindingTarget) error {
	if !bindingTargets[target] {
		return fmt.Errorf("unsupported Agent binding target %q", target)
	}
	return nil
}
