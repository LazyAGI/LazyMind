package agentexec

import (
	"context"
	"errors"
	"fmt"
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
