package assistantbridge

import (
	"context"
	"errors"
	"fmt"
	"os"

	"lazymind/agentconnector/internal/adapters/codex"
	"lazymind/agentconnector/internal/adapters/mcpclient"
	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/connectorlauncher"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/mcpbridge"
)

// ReconcileInstallations publishes the current installation once at startup,
// then relocates existing, owned registrations without connecting or restarting
// any external Agent. It needs neither a signed-in account nor a running Core.
func ReconcileInstallations(ctx context.Context) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	home, err := agentexec.LazyMindHome()
	if err != nil {
		return err
	}
	if _, err := connectorlauncher.Publish(ctx, home, self); err != nil {
		return err
	}
	store, err := credentials.NewStore(home, "")
	if err != nil {
		return err
	}
	bridge, err := mcpbridge.New(store)
	if err != nil {
		return err
	}

	var failures []error
	adapter, err := codex.New("", self, bridge)
	if err == nil {
		err = adapter.Reconcile(ctx)
	}
	if err != nil {
		failures = append(failures, fmt.Errorf("codex: %w", err))
	}
	for _, kind := range []mcpclient.Kind{mcpclient.Cursor, mcpclient.WorkBuddy, mcpclient.Raccoon, mcpclient.TRAEWork, mcpclient.DeepSeekHarness} {
		adapter, err := mcpclient.New(kind, self, bridge)
		if err == nil {
			err = adapter.Reconcile(ctx)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", kind, err))
		}
	}
	return errors.Join(failures...)
}
