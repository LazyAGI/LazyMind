package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	execPath, err := os.Executable()
	if err != nil {
		log.Printf("warning: could not resolve executable path: %v", err)
		execPath = os.Args[0]
	}
	cli := NewCLI(os.Stdout, os.Stderr, &ExecRunner{}, execPath)
	if err := cli.Run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type CLI struct {
	out        io.Writer
	errOut     io.Writer
	runner     CommandRunner
	execPath   string
	newManager func(CommandRunner, string) *RuntimeManager
}

func NewCLI(out, errOut io.Writer, runner CommandRunner, execPath string) *CLI {
	return &CLI{out: out, errOut: errOut, runner: runner, execPath: execPath, newManager: NewRuntimeManager}
}

func (c *CLI) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		c.usage()
		return fmt.Errorf("no command")
	}

	manager := c.newManager(c.runner, c.execPath)
	manager.SetOutput(c.out, c.errOut)

	switch args[0] {
	case "install-component":
		return c.installComponentCommand(ctx, args[1:])
	case "update-bootstrap":
		return c.updateBootstrapCommand(ctx, manager, args[1:])
	case "update-components-check":
		return c.updateComponentsCommand(ctx, args[1:])
	case "update-control":
		return c.desktopUpdateCommand(ctx, manager, args[1:])
	case "startup-guard":
		opts, err := parseCommonArgs("startup-guard", args[1:], c.errOut)
		if err != nil {
			return err
		}
		opts, err = nativeRuntimeOptions(ctx, opts)
		if err != nil {
			return err
		}
		cfg, paths, err := newRuntimeConfigWithOptions(opts, true)
		if err != nil {
			return err
		}
		if err = registerDesktopUpdateCoordinator(ctx, cfg, paths); err != nil {
			return err
		}
		return json.NewEncoder(c.out).Encode(map[string]string{"status": "allowed"})
	case "update-state":
		opts, err := parseCommonArgs("update-state", args[1:], c.errOut)
		if err != nil {
			return err
		}
		cfg, paths, err := newRuntimeConfigWithOptions(opts, true)
		if err != nil {
			return updateStateFailure("data_unknown")
		}
		state, err := measureDesktopUpdateState(ctx, cfg, paths)
		if err != nil {
			return err
		}
		return json.NewEncoder(c.out).Encode(state)
	case "install-rag":
		opts, err := parseCommonArgs("install-rag", args[1:], c.errOut)
		if err != nil {
			return err
		}
		cfg, paths, err := NewRuntimeConfigWithOptions(opts)
		if err != nil {
			return err
		}
		lease, err := registerRuntimeUpdateLease(ctx, cfg, paths)
		if err != nil {
			return err
		}
		defer lease.Close()
		if cfg.Profile != "local" {
			return fmt.Errorf("source RAG installation requires local profile")
		}
		root, err := resolveRuntimeRecoveryRoot()
		if err != nil {
			return err
		}
		writer, err := desktopupdate.BeginIndependentComponentWrite(ctx, root, paths.RuntimeRoot)
		if err != nil {
			return err
		}
		defer desktopupdate.ReleaseNativeLock(writer)
		release, err := acquireAlgorithmPythonLock(ctx, paths)
		if err != nil {
			return err
		}
		defer release()
		// Do not recreate a live virtualenv if dependency installation fails.
		if err := NewAlgorithmServiceManager(c.runner).installAlgorithmPythonDeps(ctx, paths, cfg.Algorithm.EnableEvo, true); err != nil {
			return err
		}
		marker := filepath.Join(paths.RuntimeRoot, "deps", "python-components", "rag", "source.ready")
		if err := os.MkdirAll(filepath.Dir(marker), 0755); err != nil {
			return err
		}
		return os.WriteFile(marker, []byte(paths.AlgorithmPython), 0644)
	case "shell":
		return runProcessComposeShell(ctx, args[1:], c.out, c.errOut)
	case "up":
		opts, err := parseCommonArgs("up", args[1:], c.errOut)
		if err != nil {
			return err
		}
		opts, err = nativeRuntimeOptions(ctx, opts)
		if err != nil {
			return err
		}
		cfg, paths, err := NewRuntimeConfigWithOptions(opts)
		if err != nil {
			return err
		}
		if err := bindNativeStartup(ctx, &cfg, &paths, opts.UpdateBootstrapTx, opts.UpdateBootstrapOwner); err != nil {
			return err
		}
		return withRuntimeUpdateLease(ctx, cfg, paths, func() error { return manager.Up(ctx, cfg, paths) })
	case "warmup":
		opts, err := parseCommonArgs("warmup", args[1:], c.errOut)
		if err != nil {
			return err
		}
		if opts.MaintenanceMode != installerWarmupMaintenanceMode {
			return fmt.Errorf("warmup requires --maintenance %s", installerWarmupMaintenanceMode)
		}
		cfg, paths, err := NewRuntimeConfigWithOptions(opts)
		if err != nil {
			return err
		}
		return withRuntimeUpdateLease(ctx, cfg, paths, func() error { return manager.Warmup(ctx, cfg, paths) })
	case "down":
		opts, err := parseCommonArgs("down", args[1:], c.errOut)
		if err != nil {
			return err
		}
		cfg, paths, err := newRuntimeConfigWithOptions(opts, true)
		if err != nil {
			return err
		}
		return withRuntimeUpdateLease(ctx, cfg, paths, func() error { return manager.Down(ctx, cfg, paths) })
	case "status":
		asJSON, opts, err := parseStatusArgs(args[1:], c.errOut)
		if err != nil {
			return err
		}
		cfg, paths, err := newRuntimeConfigWithOptions(opts, true)
		if err != nil {
			return err
		}
		resp, err := manager.Status(ctx, cfg, paths, asJSON)
		if err != nil {
			return err
		}
		_, _ = io.WriteString(c.out, resp)
		if asJSON && len(resp) > 0 && resp[len(resp)-1] != '\n' {
			_, _ = io.WriteString(c.out, "\n")
		}
		return nil
	case "reset":
		scope, opts, err := parseResetArgs(args[1:], c.errOut)
		if err != nil {
			return err
		}
		cfg, paths, err := NewRuntimeConfigWithOptions(opts)
		if err != nil {
			return err
		}
		return withRuntimeUpdateLease(ctx, cfg, paths, func() error { return manager.Reset(ctx, cfg, paths, scope) })
	case "service":
		service, action, opts, err := parseServiceArgs(args[1:], c.errOut)
		if err != nil {
			return err
		}
		cfg, paths, err := NewRuntimeConfigWithOptions(opts)
		if err != nil {
			return err
		}
		return withRuntimeUpdateLease(ctx, cfg, paths, func() error { return manager.RunServiceAction(ctx, cfg, paths, service, action) })
	case "guard":
		ownerPID, opts, err := parseGuardArgs(args[1:], c.errOut)
		if err != nil {
			return err
		}
		cfg, paths, err := NewRuntimeConfigWithOptions(opts)
		if err != nil {
			return err
		}
		err = withRuntimeUpdateLease(ctx, cfg, paths, func() error {
			return runRuntimeGuard(ctx, cfg, paths, ownerPID, defaultGuardPollInterval, ownerProcessAlive, manager.Down)
		})
		return err
	case "internal":
		return c.runInternal(ctx, manager, args[1:])
	default:
		c.usage()
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func (c *CLI) runInternal(ctx context.Context, manager *RuntimeManager, args []string) error {
	if len(args) == 0 {
		c.usage()
		return fmt.Errorf("internal command required")
	}

	sub := args[0]
	subArgs := args[1:]
	if sub == "algorithm-run" || sub == "algorithm-down" {
		service, opts, err := parseAlgorithmInternalArgs(sub, subArgs, c.errOut)
		if err != nil {
			return err
		}
		cfg, paths, err := NewRuntimeConfigWithOptions(opts)
		if err != nil {
			return err
		}
		if sub == "algorithm-run" {
			if err := inheritCommittedRuntimeStorage(&cfg, &paths); err != nil {
				return err
			}
			if err := bindNativeStartup(ctx, &cfg, &paths, bootstrapTransaction(cfg), cfg.OwnerToken); err != nil {
				return err
			}
			return withRuntimeUpdateLease(ctx, cfg, paths, func() error { return manager.algorithm.Run(ctx, cfg, paths, service) })
		}
		return withRuntimeUpdateLease(ctx, cfg, paths, func() error { return manager.algorithm.Down(ctx, paths, service) })
	}
	opts, err := parseCommonArgs("internal", subArgs, c.errOut)
	if err != nil {
		return err
	}
	cfg, paths, err := NewRuntimeConfigWithOptions(opts)
	if err != nil {
		return err
	}
	if strings.HasSuffix(sub, "-run") {
		if err := inheritCommittedRuntimeStorage(&cfg, &paths); err != nil {
			return err
		}
		if err := bindNativeStartup(ctx, &cfg, &paths, bootstrapTransaction(cfg), cfg.OwnerToken); err != nil {
			return err
		}
	}

	lease, err := registerRuntimeUpdateLease(ctx, cfg, paths)
	if err != nil {
		return err
	}
	defer lease.Close()
	switch sub {
	case "local-proxy-run":
		return manager.localProxy.Run(ctx, cfg, paths)
	case "local-proxy-down":
		return manager.localProxy.Down(ctx, cfg, paths)
	case "auth-service-run":
		return manager.authService.Run(ctx, cfg, paths)
	case "auth-service-down":
		return manager.authService.Down(ctx, cfg, paths)
	case "channel-gateway-run":
		return manager.channelGateway.Run(ctx, cfg, paths)
	case "channel-gateway-down":
		return manager.channelGateway.Down(ctx, paths)
	case "core-run":
		return manager.coreService.Run(ctx, cfg, paths)
	case "core-down":
		return manager.coreService.Down(ctx, cfg, paths)
	case "scan-control-plane-run":
		return manager.scanControl.Run(ctx, cfg, paths)
	case "scan-control-plane-down":
		return manager.scanControl.Down(ctx, paths)
	case "file-watcher-run":
		return manager.fileWatcher.Run(ctx, cfg, paths)
	case "file-watcher-down":
		return manager.fileWatcher.Down(ctx, paths)
	case "frontend-run":
		return manager.frontend.Run(ctx, cfg, paths)
	case "frontend-down":
		return manager.frontend.Down(ctx, cfg, paths)
	case "milvus-lite-run":
		return manager.milvusLite.Run(ctx, cfg, paths)
	case "milvus-lite-down":
		return manager.milvusLite.Down(ctx, paths)
	case "sqlite-server-run":
		return manager.sqliteServer.Run(ctx, cfg, paths)
	case "sqlite-server-down":
		return manager.sqliteServer.Down(ctx, cfg, paths)
	default:
		return fmt.Errorf("unknown internal command: %s", sub)
	}
}

func parseAlgorithmInternalArgs(name string, args []string, out io.Writer) (string, RuntimeConfigOptions, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	service := fs.String("service", "", "")
	opts := addRuntimeFlags(fs)
	if err := fs.Parse(args); err != nil {
		return "", RuntimeConfigOptions{}, err
	}
	if len(fs.Args()) != 0 {
		return "", RuntimeConfigOptions{}, fmt.Errorf("unexpected positional args: %v", fs.Args())
	}
	if *service == "" {
		return "", RuntimeConfigOptions{}, fmt.Errorf("--service is required")
	}
	return *service, opts(), nil
}

func parseCommonArgs(name string, args []string, out io.Writer) (RuntimeConfigOptions, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	opts := addRuntimeFlags(fs)
	if err := fs.Parse(args); err != nil {
		return RuntimeConfigOptions{}, err
	}
	if len(fs.Args()) != 0 {
		return RuntimeConfigOptions{}, fmt.Errorf("unexpected positional args: %v", fs.Args())
	}
	return opts(), nil
}

func parseStatusArgs(args []string, out io.Writer) (bool, RuntimeConfigOptions, error) {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(out)
	asJSON := fs.Bool("json", false, "")
	opts := addRuntimeFlags(fs)
	if err := fs.Parse(args); err != nil {
		return false, RuntimeConfigOptions{}, err
	}
	if len(fs.Args()) != 0 {
		return false, RuntimeConfigOptions{}, fmt.Errorf("unexpected positional args: %v", fs.Args())
	}
	return *asJSON, opts(), nil
}

func parseResetArgs(args []string, out io.Writer) (ResetScope, RuntimeConfigOptions, error) {
	fs := flag.NewFlagSet("reset", flag.ContinueOnError)
	fs.SetOutput(out)
	scopeText := fs.String("scope", string(ResetScopeKB), "")
	opts := addRuntimeFlags(fs)
	if err := fs.Parse(args); err != nil {
		return "", RuntimeConfigOptions{}, err
	}
	if len(fs.Args()) != 0 {
		return "", RuntimeConfigOptions{}, fmt.Errorf("unexpected positional args: %v", fs.Args())
	}
	scope, err := parseResetScope(*scopeText)
	if err != nil {
		return "", RuntimeConfigOptions{}, err
	}
	return scope, opts(), nil
}

func parseServiceArgs(args []string, out io.Writer) (string, string, RuntimeConfigOptions, error) {
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	fs.SetOutput(out)
	service := fs.String("name", "", "")
	action := fs.String("action", "", "")
	opts := addRuntimeFlags(fs)
	if err := fs.Parse(args); err != nil {
		return "", "", RuntimeConfigOptions{}, err
	}
	if len(fs.Args()) != 0 {
		return "", "", RuntimeConfigOptions{}, fmt.Errorf("unexpected positional args: %v", fs.Args())
	}
	if strings.TrimSpace(*service) == "" {
		return "", "", RuntimeConfigOptions{}, fmt.Errorf("--name is required")
	}
	if strings.TrimSpace(*action) == "" {
		return "", "", RuntimeConfigOptions{}, fmt.Errorf("--action is required")
	}
	return *service, *action, opts(), nil
}

func parseGuardArgs(args []string, out io.Writer) (int, RuntimeConfigOptions, error) {
	fs := flag.NewFlagSet("guard", flag.ContinueOnError)
	fs.SetOutput(out)
	ownerPID := fs.Int("owner-pid", 0, "")
	opts := addRuntimeFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 0, RuntimeConfigOptions{}, err
	}
	if len(fs.Args()) != 0 {
		return 0, RuntimeConfigOptions{}, fmt.Errorf("unexpected positional args: %v", fs.Args())
	}
	if *ownerPID <= 0 {
		return 0, RuntimeConfigOptions{}, fmt.Errorf("--owner-pid must be positive")
	}
	return *ownerPID, opts(), nil
}

func addRuntimeFlags(fs *flag.FlagSet) func() RuntimeConfigOptions {
	repoRoot := fs.String("repo-root", "", "")
	profile := fs.String("profile", "", "")
	maintenanceMode := fs.String("maintenance", "", "")
	ownerToken := fs.String("owner-token", "", "")
	runtimeRoot := fs.String("runtime-root", "", "")
	buildRoot := fs.String("build-root", "", "")
	resourcesRoot := fs.String("resources-root", "", "")
	bootstrapTx := fs.String("update-bootstrap", "", "")
	bootstrapOwner := fs.String("bootstrap-owner", "", "")
	return func() RuntimeConfigOptions {
		return RuntimeConfigOptions{
			UpdateBootstrapTx: *bootstrapTx, UpdateBootstrapOwner: *bootstrapOwner,
			Profile:         *profile,
			MaintenanceMode: *maintenanceMode,
			OwnerToken:      *ownerToken,
			RepoRoot:        *repoRoot,
			RuntimeRoot:     *runtimeRoot,
			BuildRoot:       *buildRoot,
			ResourcesRoot:   *resourcesRoot,
		}
	}
}

func (c *CLI) usage() {
	_, _ = io.WriteString(c.out, "Usage:\n")
	_, _ = io.WriteString(c.out, "  local-runtime-manager up\n")
	_, _ = io.WriteString(c.out, "  local-runtime-manager warmup --maintenance installer-warmup\n")
	_, _ = io.WriteString(c.out, "  local-runtime-manager down\n")
	_, _ = io.WriteString(c.out, "  local-runtime-manager status --json\n")
	_, _ = io.WriteString(c.out, "  local-runtime-manager reset --scope kb|all\n")
	_, _ = io.WriteString(c.out, "  local-runtime-manager service --name file-watcher --action build|start|stop\n")
	_, _ = io.WriteString(c.out, "  local-runtime-manager guard --owner-pid <pid>\n")
	_, _ = io.WriteString(c.out, "  local-runtime-manager internal sqlite-server-run|sqlite-server-down|local-proxy-run|local-proxy-down|auth-service-run|auth-service-down|channel-gateway-run|channel-gateway-down|core-run|core-down|scan-control-plane-run|scan-control-plane-down|file-watcher-run|file-watcher-down|frontend-run|frontend-down|milvus-lite-run|milvus-lite-down|algorithm-run|algorithm-down\n")
}
