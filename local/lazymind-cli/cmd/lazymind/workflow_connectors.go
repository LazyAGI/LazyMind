package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"lazymind/agentconnector/internal/codexhost"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workbuddyhost"
	"lazymind/agentconnector/internal/workflowconnector"
)

func runWorkflowConnectors(ctx context.Context, args []string, out io.Writer) error {
	if len(args) != 2 || args[0] != "reload" || (args[1] != "codex" && args[1] != "workbuddy") {
		return errors.New("usage: lazymind internal workflow-connectors reload <codex|workbuddy>")
	}
	host := args[1]
	store, err := credentials.NewStore("", "")
	if err != nil {
		return err
	}
	home := store.Directory()
	installed, err := workflowconnector.Resolve(home, host)
	if err != nil {
		return err
	}
	account, err := store.AccountScope()
	if err != nil {
		return err
	}
	var heartbeat func() (workflowconnector.Runtime, error)
	if host == "codex" {
		if _, _, err := codexhost.Load(home, account); err != nil {
			return err
		}
		heartbeat = func() (workflowconnector.Runtime, error) {
			status, err := codexhost.ReadStatus(home, account)
			return status.Runtime, err
		}
	} else {
		config, pair, err := workbuddyhost.Load(home, account)
		if err != nil {
			return err
		}
		heartbeat = func() (workflowconnector.Runtime, error) {
			status, err := workbuddyhost.ReadStatus(config, pair.ConnectorID)
			return status.Runtime, err
		}
	}
	request, err := workflowconnector.RequestReload(home, host)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		running, err := heartbeat()
		if err == nil && installed.Matches(running) && running.ConnectorReload == request {
			return printJSON(out, map[string]any{"ok": true, "host": host, "connector": running})
		}
		if workflowconnector.ReloadRequest(home, host) != request {
			return fmt.Errorf("%s Connector reload was superseded by another reload", host)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s Connector at %s did not confirm reload. Check the Assistant Bridge and Connector logs: %w", host, installed.EntryPath(), ctx.Err())
		case <-ticker.C:
		}
	}
}
