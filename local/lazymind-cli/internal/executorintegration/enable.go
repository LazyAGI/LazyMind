// Package executorintegration prepares provider features before committing the
// user's execution preference. Both desktop IPC and the HTTP bridge use it.
package executorintegration

import (
	"context"
	"lazymind/agentconnector/internal/adapters/codex"
	"lazymind/agentconnector/internal/executorpolicy"
	"lazymind/agentconnector/internal/mcpbridge"
	"strings"
)

func SetEnabled(ctx context.Context, policy *executorpolicy.Store, bridge *mcpbridge.Bridge, provider string, enabled bool) (executorpolicy.Status, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if _, err := policy.Enabled(provider); err != nil {
		return executorpolicy.Status{}, err
	}
	if provider == "codex" && enabled {
		adapter, err := codex.New("", "", bridge)
		if err != nil {
			return executorpolicy.Status{}, err
		}
		if err := adapter.PrepareCLI(ctx); err != nil {
			return executorpolicy.Status{}, err
		}
	}
	return policy.SetEnabled(provider, enabled)
}
