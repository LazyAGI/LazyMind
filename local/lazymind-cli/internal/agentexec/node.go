package agentexec

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// CompatibleNode validates automatic candidates independently: a stale bundled
// path must not mask a working runtime later in the discovery list.
func CompatibleNode(ctx context.Context, candidates ...string) (string, error) {
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		node, err := ResolveExecutable(candidate)
		if err != nil {
			continue
		}
		// The dispatcher starts Node directly; a batch shim may pass Run's
		// version probe through PowerShell but cannot be launched that way.
		if runtime.GOOS == "windows" {
			ext := strings.ToLower(filepath.Ext(node))
			if ext == ".cmd" || ext == ".bat" {
				continue
			}
		}
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		version, err := Run(probe, node, "--version")
		cancel()
		var major, minor int
		if err == nil {
			if _, err = fmt.Sscanf(strings.TrimSpace(version), "v%d.%d", &major, &minor); err == nil && (major > 22 || major == 22 && minor >= 19) {
				return node, nil
			}
		}
	}
	return "", errors.New("Workflow requires an available Node.js 22.19 or later")
}
