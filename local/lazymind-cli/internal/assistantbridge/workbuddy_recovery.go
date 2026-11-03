package assistantbridge

import (
	"context"
	"fmt"
	"io"
	"time"

	"lazymind/agentconnector/internal/codexhost"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workbuddyhost"
)

// KeepDesktopAgentsAvailable runs under Desktop's existing supervised Agent Host.
// It restores the Bridge, which already supervises the Connector, only while
// this account has enabled a CDP host. It never launches or restarts that app.
func KeepDesktopAgentsAvailable(ctx context.Context, store *credentials.Store, stderr io.Writer) {
	address, err := connectorconfig.BridgeAddress()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return
	}
	keepWorkBuddyAvailable(ctx, 5*time.Second, func() bool {
		account, err := store.AccountScope()
		if err != nil {
			return false
		}
		_, pair, err := workbuddyhost.Load(store.Directory(), account)
		if err == nil && pair.Enabled {
			return true
		}
		_, codex, err := codexhost.Load(store.Directory(), account)
		return err == nil && codex.Enabled
	}, func(ctx context.Context) error {
		_, err := Start(ctx, address)
		return err
	}, stderr)
}

func keepWorkBuddyAvailable(ctx context.Context, interval time.Duration, enabled func() bool, ensure func(context.Context) error, stderr io.Writer) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	lastError := ""
	for ctx.Err() == nil {
		if enabled() {
			attempt, cancel := context.WithTimeout(ctx, 15*time.Second)
			err := ensure(attempt)
			cancel()
			if err != nil && ctx.Err() == nil {
				if err.Error() != lastError {
					fmt.Fprintln(stderr, "Desktop Agent bridge recovery:", err)
				}
				lastError = err.Error()
			} else {
				lastError = ""
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
