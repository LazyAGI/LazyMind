package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"path/filepath"

	"lazymind/agentconnector/internal/assistantbridge"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/mcpbridge"
	"lazymind/agentconnector/internal/workflowhost"
)

// Explicit opt-in; this neither starts Codex nor changes its MCP configuration.
func runCodexWorkflowPair(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("internal codex-workflow-pair", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profile := flags.String("codex-home", "", "absolute Codex desktop profile directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !filepath.IsAbs(*profile) || flags.NArg() != 0 {
		return errors.New("provide --codex-home with an absolute Codex desktop profile directory")
	}
	store, err := credentials.NewStore("", "")
	if err != nil {
		return err
	}
	account, err := store.AccountScope()
	if err != nil {
		return err
	}
	pair, err := workflowhost.EnsureForProvider(store.Directory(), "codex", *profile, account)
	if err != nil {
		return err
	}
	return printJSON(stdout, map[string]string{
		"pairing_file": filepath.Join(store.Directory(), "workflow-hosts", pair.ConnectorID+".json"),
		"connector_id": pair.ConnectorID,
	})
}

// Assistant Bridge owns the CDP worker; stdout remains exclusively MCP JSON-RPC.
func runCodexWorkflowMCP(ctx context.Context) error {
	store, err := credentials.NewStore("", "")
	if err != nil {
		return err
	}
	address, err := connectorconfig.BridgeAddress()
	if err != nil {
		return err
	}
	if _, err := assistantbridge.Start(ctx, address); err != nil {
		return err
	}
	bridge, err := mcpbridge.New(store)
	if err != nil {
		return err
	}
	return bridge.RunStdio(ctx)
}
