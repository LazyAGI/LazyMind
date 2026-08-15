package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"lazymind/agentconnector/internal/adapters/codex"
	"lazymind/agentconnector/internal/adapters/cursor"
	"lazymind/agentconnector/internal/adapters/editor"
	"lazymind/agentconnector/internal/adapters/workbuddy"
	"lazymind/agentconnector/internal/chatagent"
	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/mcpbridge"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		usage(stderr)
		return errors.New("a command is required")
	}
	switch args[0] {
	case "mcp":
		return runMCP(ctx, args[1:])
	case "agent":
		return runAgent(ctx, args[1:], stdout, stderr)
	case "internal":
		return runInternal(ctx, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return nil
	default:
		return fmt.Errorf("unknown Go CLI command %q", args[0])
	}
}

func runInternal(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) < 3 || args[0] != "agent" {
		return errors.New("invalid internal command")
	}
	agent := strings.ToLower(args[1])
	action := strings.ToLower(args[2])
	flags := flag.NewFlagSet("internal agent "+agent+" "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	agentBinary := flags.String("agent-bin", "", "external Agent CLI executable")
	if err := flags.Parse(args[3:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected internal command arguments")
	}
	store, err := credentials.NewStore("", "")
	if err != nil {
		return err
	}
	bridge, err := mcpbridge.New(store)
	if err != nil {
		return err
	}
	switch agent {
	case "codex":
		return runInternalCodex(ctx, action, *agentBinary, bridge, stdout)
	case string(editor.Cursor), string(editor.WorkBuddy):
		if action != "status" {
			return fmt.Errorf("%s uses manual setup; only status is supported", agent)
		}
		adapter, err := editor.New(editor.Kind(agent), *agentBinary, "", bridge)
		if err != nil {
			return err
		}
		return printJSON(stdout, adapter.Status(ctx))
	default:
		return fmt.Errorf("unsupported external Agent %q", agent)
	}
}

func runInternalCodex(ctx context.Context, action, binary string, bridge *mcpbridge.Bridge, stdout io.Writer) error {
	adapter, err := codex.New(binary, "", bridge)
	if err != nil {
		return err
	}
	var status codex.Status
	switch action {
	case "connect":
		status, err = adapter.Connect(ctx)
	case "status":
		status, err = adapter.Status(ctx)
	case "disconnect":
		status, err = adapter.Disconnect(ctx)
	default:
		return fmt.Errorf("unsupported Codex action %q", action)
	}
	if err != nil {
		return err
	}
	return printJSON(stdout, status)
}

func runAgent(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) >= 2 && args[0] == "host" {
		action := strings.ToLower(args[1])
		if action != "run" && action != "status" {
			return errors.New("usage: lazymind agent host <run|status>")
		}
		flags := flag.NewFlagSet("agent host "+action, flag.ContinueOnError)
		flags.SetOutput(stderr)
		provider := flags.String("provider", "codex", "Agent provider: codex, cursor, workbuddy, or all")
		agentBinary := flags.String("agent-bin", "", "selected external Agent CLI executable")
		if err := flags.Parse(args[2:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("unexpected agent host arguments")
		}
		credentialStore, err := credentials.NewStore("", "")
		if err != nil {
			return err
		}
		api, err := coreapi.New(credentialStore)
		if err != nil {
			return err
		}
		providers, err := hostProviders(*provider)
		if err != nil {
			return err
		}
		if len(providers) > 1 && strings.TrimSpace(*agentBinary) != "" {
			return errors.New("--agent-bin requires one explicit --provider")
		}
		if action == "status" {
			statuses := make(map[string]any, len(providers))
			for _, name := range providers {
				var status map[string]any
				if err := api.DoJSON(ctx, "GET", "/external-chat/hosts/"+name+":status", nil, &status); err != nil {
					return err
				}
				statuses[name] = status
			}
			if len(providers) == 1 {
				return printJSON(stdout, statuses[providers[0]])
			}
			return printJSON(stdout, map[string]any{"hosts": statuses})
		}
		return runAgentHosts(ctx, api, providers, *agentBinary, stderr)
	}
	if len(args) < 2 || args[0] != "guide" {
		return errors.New("usage: lazymind agent host run | lazymind agent guide <cursor|workbuddy>")
	}
	kind := editor.Kind(strings.ToLower(args[1]))
	flags := flag.NewFlagSet("agent guide "+string(kind), flag.ContinueOnError)
	flags.SetOutput(stderr)
	agentBinary := flags.String("agent-bin", "", "external Agent CLI executable")
	if err := flags.Parse(args[2:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected agent guide arguments")
	}
	store, err := credentials.NewStore("", "")
	if err != nil {
		return err
	}
	bridge, err := mcpbridge.New(store)
	if err != nil {
		return err
	}
	adapter, err := editor.New(kind, *agentBinary, "", bridge)
	if err != nil {
		return err
	}
	status := adapter.Status(ctx)
	if !status.Installed || status.Setup == nil {
		return errors.New(status.ReadinessError)
	}
	_, _ = fmt.Fprintf(stdout, "%s %s detected.\n\n", status.DisplayName, status.Version)
	switch status.Setup.Method {
	case editor.SetupCursorInstall:
		_, _ = fmt.Fprintf(stdout, "Open this official Cursor MCP install link and approve the installation:\n\n%s\n\n", status.Setup.URL)
		_, _ = fmt.Fprintf(stdout, "If the install page cannot open Cursor, merge this entry into %s without removing existing mcpServers:\n\n%s\n\n", status.Setup.ConfigPath, status.Setup.Configuration)
	case editor.SetupConfigFile:
		_, _ = fmt.Fprintf(stdout, "In WorkBuddy, open MCP -> Configure MCP and merge this entry into %s without removing existing mcpServers:\n\n%s\n\n", status.Setup.ConfigPath, status.Setup.Configuration)
	default:
		return fmt.Errorf("unsupported %s setup method %q", status.DisplayName, status.Setup.Method)
	}
	if status.ServiceReady {
		_, _ = fmt.Fprintf(stdout, "LazyMind MCP is ready with %d tools. Start a new %s session after configuration.\n", len(status.Tools), status.DisplayName)
	} else {
		_, _ = fmt.Fprintf(stdout, "The setup instructions are ready, but LazyMind MCP is currently unavailable: %s\n", status.ReadinessError)
	}
	return nil
}

func hostProviders(value string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "codex":
		return []string{"codex"}, nil
	case "cursor":
		return []string{"cursor"}, nil
	case "workbuddy":
		return []string{"workbuddy"}, nil
	case "all":
		return []string{"codex", "cursor", "workbuddy"}, nil
	default:
		return nil, fmt.Errorf("unsupported external Agent provider %q", value)
	}
}

func newAgentRunner(provider, binary string) (chatagent.Runner, error) {
	switch provider {
	case "codex":
		return codex.NewChatRunner(binary)
	case "cursor":
		return cursor.NewChatRunner(binary)
	case "workbuddy":
		return workbuddy.NewChatRunner(binary)
	default:
		return nil, fmt.Errorf("unsupported external Agent provider %q", provider)
	}
}

func runAgentHosts(ctx context.Context, api *coreapi.Client, providers []string, binary string, stderr io.Writer) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	hosts := make([]*chatagent.Host, 0, len(providers))
	for _, provider := range providers {
		runner, err := newAgentRunner(provider, binary)
		if err != nil {
			if len(providers) == 1 {
				return err
			}
			host, hostErr := chatagent.NewUnavailableHost(api, provider, err)
			if hostErr != nil {
				return hostErr
			}
			hosts = append(hosts, host)
			_, _ = fmt.Fprintf(stderr, "LazyMind %s Agent unavailable: %v\n", provider, err)
			continue
		}
		host, err := chatagent.NewHost(api, runner, provider)
		if err != nil {
			return err
		}
		hosts = append(hosts, host)
		_, _ = fmt.Fprintln(stderr, "LazyMind external Agent host ready:", host)
	}
	results := make(chan error, len(hosts))
	for _, host := range hosts {
		go func() { results <- host.Run(runCtx) }()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-results:
		return err
	}
}

func runMCP(ctx context.Context, args []string) error {
	if len(args) != 1 || args[0] != "proxy" {
		return errors.New("usage: lazymind mcp proxy")
	}
	store, err := credentials.NewStore("", "")
	if err != nil {
		return err
	}
	bridge, err := mcpbridge.New(store)
	if err != nil {
		return err
	}
	return bridge.RunStdio(ctx)
}

func printJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func usage(out io.Writer) {
	_, _ = fmt.Fprintln(out, `LazyMind external Agent connector

Usage:
  lazymind mcp proxy
  lazymind agent host <run|status> [--provider codex|cursor|workbuddy|all]
  lazymind agent guide <cursor|workbuddy>

Codex can connect with its native 'codex mcp add' command or from
LazyMind Desktop settings. LazyMind Desktop hosts all installed Agent CLIs;
the public command defaults to Codex for backward compatibility. Cursor and
WorkBuddy guides only print native MCP setup and never modify Agent config.
Internal Adapter commands are not a public CLI.`)
}
