package main

import (
	"context"
	"runtime"
)

func (m *RuntimeManager) prepareStartupGitHubCLI(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if cfg.Profile != "local" || (runtime.GOOS != "darwin" && runtime.GOOS != "windows") {
		return nil
	}
	// Prepare before the supervisor starts so download/build time does not consume
	// the auth-service health deadline. GitHub authorization is optional for Local.
	m.progressf("preparing Local GitHub authorization tool (download/build if needed)")
	if err := prepareLocalGitHubCLI(ctx, m.runner, paths); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		m.progressf("GitHub authorization tool unavailable; continuing Local startup and retrying next launch: %v", err)
	}
	return nil
}
