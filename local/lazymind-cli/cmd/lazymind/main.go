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
	"time"

	"lazymind/agentconnector/internal/adapters/codex"
	"lazymind/agentconnector/internal/adapters/cursor"
	"lazymind/agentconnector/internal/adapters/mcpclient"
	"lazymind/agentconnector/internal/adapters/workbuddy"
	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/assistantbridge"
	"lazymind/agentconnector/internal/chatagent"
	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/executorpolicy"
	"lazymind/agentconnector/internal/mcpbridge"
)

const agentDiscoveryRetryDelay = 2 * time.Second

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
	case "assistant":
		return runAssistant(ctx, args[1:], stdout, stderr)
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
	if len(args) > 0 && args[0] == "binding" {
		return runInternalBinding(args[1:], stdout, stderr)
	}
	if len(args) < 3 {
		return errors.New("invalid internal command")
	}
	if args[0] == "executor" {
		return runInternalExecutor(args[1:], stdout)
	}
	if args[0] != "agent" {
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
	if agent == "all" {
		if action != "status" {
			return fmt.Errorf("unsupported all action %q", action)
		}
		statuses, err := assistantbridge.Statuses(ctx, bridge)
		if err != nil {
			return err
		}
		return printJSON(stdout, map[string]any{"agents": statuses})
	}
	switch agent {
	case "codex":
		return runInternalCodex(ctx, action, *agentBinary, bridge, stdout)
	case string(mcpclient.Cursor), string(mcpclient.WorkBuddy), string(mcpclient.Raccoon), string(mcpclient.TRAEWork), string(mcpclient.DeepSeekHarness):
		if action == "login" {
			if agent != string(mcpclient.Cursor) {
				return fmt.Errorf("unsupported %s action %q", agent, action)
			}
			if err := cursor.Login(ctx, *agentBinary); err != nil {
				return err
			}
		}
		adapter, err := mcpclient.New(mcpclient.Kind(agent), "", bridge)
		if err != nil {
			return err
		}
		var status agentintegration.Status
		switch action {
		case "connect":
			status = adapter.Connect(ctx)
		case "status":
			status = adapter.Status(ctx)
		case "disconnect":
			status = adapter.Disconnect(ctx)
		case "login":
			status = adapter.Status(ctx)
		default:
			return fmt.Errorf("unsupported %s action %q", agent, action)
		}
		return printJSON(stdout, status)
	default:
		return fmt.Errorf("unsupported external Agent %q", agent)
	}
}

func runInternalBinding(args []string, stdout, stderr io.Writer) error {
	if len(args) < 2 {
		return errors.New("invalid internal binding command")
	}
	target := agentexec.BindingTarget(strings.ToLower(args[0]))
	action := strings.ToLower(args[1])
	flags := flag.NewFlagSet("internal binding "+string(target)+" "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("path", "", "external Agent executable path")
	if err := flags.Parse(args[2:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected internal binding arguments")
	}
	if target == "all" {
		if action != "status" {
			return fmt.Errorf("unsupported all binding action %q", action)
		}
		bindings, err := agentexec.ExecutableBindings()
		if err != nil {
			return err
		}
		return printJSON(stdout, map[string]any{"bindings": bindings})
	}
	switch action {
	case "status":
		value, configured, err := agentexec.ExecutableBinding(target)
		if err != nil {
			return err
		}
		return printJSON(stdout, map[string]any{
			"target": target, "configured": configured, "path": value,
		})
	case "set":
		if strings.TrimSpace(*path) == "" {
			return errors.New("--path is required")
		}
		value, err := agentexec.SetExecutableBinding(target, *path)
		if err != nil {
			return err
		}
		return printJSON(stdout, map[string]any{
			"target": target, "configured": true, "path": value,
		})
	case "clear":
		if err := agentexec.ClearExecutableBinding(target); err != nil {
			return err
		}
		return printJSON(stdout, map[string]any{
			"target": target, "configured": false, "path": "",
		})
	default:
		return fmt.Errorf("unsupported %s binding action %q", target, action)
	}
}

func runInternalExecutor(args []string, stdout io.Writer) error {
	if len(args) != 2 {
		return errors.New("invalid internal executor command")
	}
	provider := strings.ToLower(args[0])
	action := strings.ToLower(args[1])
	policy, err := newExecutorPolicy()
	if err != nil {
		return err
	}
	if provider == "all" {
		if action != "status" {
			return fmt.Errorf("unsupported all executor action %q", action)
		}
		statuses, err := policy.Statuses()
		if err != nil {
			return err
		}
		return printJSON(stdout, map[string]any{"executors": statuses})
	}
	var enabled bool
	switch action {
	case "enable":
		enabled = true
	case "disable":
		enabled = false
	case "status":
		enabled, err = policy.Enabled(provider)
		if err != nil {
			return err
		}
		return printJSON(stdout, executorpolicy.Status{Provider: provider, Enabled: enabled})
	default:
		return fmt.Errorf("unsupported %s executor action %q", provider, action)
	}
	status, err := policy.SetEnabled(provider, enabled)
	if err != nil {
		return err
	}
	return printJSON(stdout, status)
}

func newExecutorPolicy() (*executorpolicy.Store, error) {
	home, err := agentexec.LazyMindHome()
	if err != nil {
		return nil, err
	}
	return executorpolicy.New(home)
}

func runInternalCodex(ctx context.Context, action, binary string, bridge *mcpbridge.Bridge, stdout io.Writer) error {
	adapter, err := codex.New(binary, "", bridge)
	if err != nil {
		return err
	}
	var status agentintegration.Status
	switch action {
	case "connect":
		status = adapter.Connect(ctx)
	case "status":
		status = adapter.Status(ctx)
	case "disconnect":
		status = adapter.Disconnect(ctx)
	case "login":
		status = adapter.Login(ctx)
	default:
		return fmt.Errorf("unsupported Codex action %q", action)
	}
	return printJSON(stdout, status)
}

func runAssistant(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: lazymind assistant <start|serve|status|stop>")
	}
	action := strings.ToLower(args[0])
	flags := flag.NewFlagSet("assistant "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	address := flags.String("listen", assistantbridge.DefaultAddress, "Assistant Bridge loopback address")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected Assistant Bridge arguments")
	}
	switch action {
	case "serve":
		return serveAssistant(ctx, *address, stderr)
	case "start":
		status, err := assistantbridge.Start(ctx, *address)
		if err != nil {
			return err
		}
		return printJSON(stdout, status)
	case "status":
		status, err := assistantbridge.Health(ctx, *address)
		if err != nil {
			return err
		}
		return printJSON(stdout, status)
	case "stop":
		if err := assistantbridge.Stop(ctx, *address); err != nil {
			return err
		}
		return printJSON(stdout, map[string]any{"ok": true, "running": false})
	default:
		return fmt.Errorf("unsupported Assistant Bridge action %q", action)
	}
}

func serveAssistant(ctx context.Context, address string, stderr io.Writer) error {
	store, err := credentials.NewStore("", "")
	if err != nil {
		return err
	}
	bridge, err := mcpbridge.New(store)
	if err != nil {
		return err
	}
	policy, err := newExecutorPolicy()
	if err != nil {
		return err
	}
	server, err := assistantbridge.New(address, bridge, store, policy)
	if err != nil {
		return err
	}
	api, err := coreapi.New(store)
	if err != nil {
		return err
	}
	go func() {
		if hostErr := runAgentHosts(ctx, api, policy, []string{"codex", "cursor", "workbuddy"}, "", stderr); hostErr != nil && ctx.Err() == nil {
			_, _ = fmt.Fprintln(stderr, "LazyMind Assistant host stopped:", hostErr)
		}
	}()
	_, _ = fmt.Fprintln(stderr, "LazyMind Assistant Bridge listening on", address)
	return server.Serve(ctx)
}

func runAgent(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) >= 2 && args[0] == "host" {
		action := strings.ToLower(args[1])
		if action != "run" && action != "status" {
			return errors.New("usage: lazymind agent host <run|status>")
		}
		flags := flag.NewFlagSet("agent host "+action, flag.ContinueOnError)
		flags.SetOutput(stderr)
		provider := flags.String("provider", "codex", "Chat Agent provider: codex, cursor, workbuddy, or all")
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
				if err := api.DoJSON(ctx, "GET", "/external-chat/hosts/"+name+"/status", nil, &status); err != nil {
					return err
				}
				statuses[name] = status
			}
			if len(providers) == 1 {
				return printJSON(stdout, statuses[providers[0]])
			}
			return printJSON(stdout, map[string]any{"hosts": statuses})
		}
		policy, err := newExecutorPolicy()
		if err != nil {
			return err
		}
		return runAgentHosts(ctx, api, policy, providers, *agentBinary, stderr)
	}
	return errors.New("usage: lazymind agent host <run|status>")
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

func runAgentHosts(ctx context.Context, api *coreapi.Client, policy *executorpolicy.Store, providers []string, binary string, stderr io.Writer) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, len(providers))
	for _, provider := range providers {
		go func() { results <- runAgentProvider(runCtx, api, policy, provider, binary, stderr) }()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-results:
		return err
	}
}

func runAgentProvider(ctx context.Context, api *coreapi.Client, policy *executorpolicy.Store, provider, binary string, stderr io.Writer) error {
	lastDiscoveryError := ""
	for ctx.Err() == nil {
		runner, discoveryErr := newAgentRunner(provider, binary)
		if discoveryErr != nil {
			if message := discoveryErr.Error(); message != lastDiscoveryError {
				_, _ = fmt.Fprintf(stderr, "LazyMind %s Agent unavailable: %v\n", provider, discoveryErr)
				lastDiscoveryError = message
			}
			host, err := chatagent.NewUnavailableHost(api, policy, provider, discoveryErr)
			if err != nil {
				return err
			}
			reportCtx, cancel := context.WithTimeout(ctx, agentDiscoveryRetryDelay)
			_ = host.Run(reportCtx)
			cancel()
			continue
		}

		lastDiscoveryError = ""
		host, err := chatagent.NewHost(api, runner, policy, provider)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stderr, "LazyMind external Agent host ready:", host)
		if err := host.Run(ctx); ctx.Err() != nil {
			return ctx.Err()
		} else if err != nil {
			_, _ = fmt.Fprintf(stderr, "LazyMind %s Agent host stopped: %v\n", provider, err)
		}
		if !waitAgentDiscovery(ctx) {
			return ctx.Err()
		}
	}
	return ctx.Err()
}

func waitAgentDiscovery(ctx context.Context) bool {
	timer := time.NewTimer(agentDiscoveryRetryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
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
	_, _ = fmt.Fprint(out, `LazyMind external Agent connector

Usage:
  lazymind mcp proxy
  lazymind assistant <start|serve|status|stop>
  lazymind agent host <run|status> [--provider codex|cursor|workbuddy|all]

LazyMind Desktop and the Docker Assistant Bridge both expose one-click managed
connections in Settings -> Assistants. The bridge also hosts installed Codex,
Cursor, and CodeBuddy Code CLIs. Raccoon, TRAE Work, and DeepSeek Harness
remain MCP clients rather than Chat executors.
Internal Adapter commands are not a public CLI.
`)
}
