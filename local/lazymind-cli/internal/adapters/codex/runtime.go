package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"lazymind/agentconnector/internal/agentexec"
)

// RuntimeStatus describes CLI execution, independently of desktop/MCP setup.
type RuntimeStatus struct {
	Installed bool
	Ready     bool
	Path      string
	Source    string
	Reason    string
}

type runtimeCandidate struct{ path, source string }

func runtimeCandidates(configured string) ([]runtimeCandidate, error) {
	explicit := strings.TrimSpace(configured)
	if explicit == "" {
		explicit = strings.TrimSpace(os.Getenv("LAZYMIND_CODEX_BIN"))
	}
	if explicit == "" {
		bound, found, err := agentexec.ExecutableBinding(agentexec.CodexCLI)
		if err != nil {
			return nil, err
		}
		if found {
			explicit = bound
		}
	}
	if explicit != "" {
		return []runtimeCandidate{{explicit, "explicit"}}, nil
	}
	var candidates []runtimeCandidate
	desktops, _ := agentexec.FindDesktopApplications(codexDesktopApplication())
	var bundled []string
	for _, desktop := range desktops {
		if strings.EqualFold(filepath.Ext(desktop), ".app") {
			for _, directory := range []string{"Resources/codex-cli/bin", "Resources", "MacOS"} {
				bundled = append(bundled, filepath.Join(desktop, "Contents", directory, "codex"))
			}
		} else if strings.EqualFold(filepath.Ext(desktop), ".exe") {
			bundled = append(bundled, filepath.Join(filepath.Dir(desktop), "resources", "codex.exe"))
		}
	}
	for _, path := range bundled {
		if resolved, err := agentexec.ResolveExecutable(path); err == nil {
			candidates = append(candidates, runtimeCandidate{resolved, "desktop"})
		}
	}
	excluded := append(desktops, bundled...)
	for _, path := range agentexec.FindExecutables([]string{"codex"}, excluded...) {
		candidates = append(candidates, runtimeCandidate{path, "path"})
	}
	return candidates, nil
}

// Metadata discovery must not start executables merely to render an Assistant card.
func findBinary(configured string) (string, error) {
	candidates, err := runtimeCandidates(configured)
	if err != nil {
		return "", err
	}
	for _, candidate := range candidates {
		resolved, err := agentexec.ResolveExecutable(candidate.path)
		if err == nil {
			return resolved, nil
		}
		if candidate.source == "explicit" {
			return "", fmt.Errorf("Codex CLI is unavailable at %s: %w", candidate.path, err)
		}
	}
	return "", errors.New("Codex CLI is not installed")
}

func resolveRuntime(ctx context.Context, configured string) (runtimeCandidate, error) {
	candidates, err := runtimeCandidates(configured)
	if err != nil {
		return runtimeCandidate{}, err
	}
	var first runtimeCandidate
	var failure error
	for _, candidate := range candidates {
		resolved, err := agentexec.ResolveExecutable(candidate.path)
		if err == nil {
			candidate.path = resolved
			probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_, err = agentexec.Run(probeCtx, resolved, "--version")
			cancel()
		}
		if err == nil {
			return candidate, nil
		}
		if failure == nil {
			first = candidate
			failure = fmt.Errorf("Codex CLI cannot start (%s): %w", candidate.path, err)
		}
		if candidate.source == "explicit" || ctx.Err() != nil {
			break
		}
	}
	if failure != nil {
		return first, failure
	}
	return runtimeCandidate{}, errors.New("Codex CLI is not installed")
}

func FindBinary(configured string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	candidate, err := resolveRuntime(ctx, configured)
	if err != nil {
		return "", err
	}
	return candidate.path, nil
}

func (a *Adapter) prepareRuntime(ctx context.Context) error {
	candidate, err := resolveRuntime(ctx, a.configured)
	if err == nil {
		a.binary = candidate.path
		a.discoveryError = nil
	}
	return err
}

func Inspect(binary string) RuntimeStatus {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	candidate, err := resolveRuntime(ctx, binary)
	result := RuntimeStatus{Path: candidate.path, Source: candidate.source}
	if candidate.path != "" {
		_, statErr := os.Stat(candidate.path)
		result.Installed = statErr == nil
	}
	if err != nil {
		result.Reason = err.Error()
		return result
	}
	result.Installed = true
	result.Ready, result.Reason = authenticationStatus(ctx, candidate.path)
	return result
}

func Probe(binary string) (bool, bool, string) {
	result := Inspect(binary)
	return result.Installed, result.Ready, result.Reason
}

func availability(binary string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	return authenticationStatus(ctx, binary)
}

func authenticationStatus(ctx context.Context, binary string) (bool, string) {
	_, err := agentexec.Run(ctx, binary, "login", "status")
	if err == nil {
		return true, ""
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		switch strings.ToLower(strings.TrimSpace(err.Error())) {
		case "not logged in", "not logged in.", "not signed in", "not signed in.":
			return false, "Codex CLI is not signed in; run `codex login`"
		}
	}
	return false, fmt.Sprintf("Codex CLI login check failed (%s): %v", binary, err)
}
