package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
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
	out      io.Writer
	errOut   io.Writer
	runner   CommandRunner
	execPath string
}

func NewCLI(out, errOut io.Writer, runner CommandRunner, execPath string) *CLI {
	return &CLI{out: out, errOut: errOut, runner: runner, execPath: execPath}
}

func (c *CLI) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		c.usage()
		return fmt.Errorf("no command")
	}

	manager := NewRuntimeManager(c.runner, c.execPath)
	manager.SetOutput(c.out, c.errOut)

	switch args[0] {
	case "up":
		repoRoot, err := parseCommonArgs("up", args[1:], c.errOut)
		if err != nil {
			return err
		}
		cfg, paths, err := NewRuntimeConfig("", repoRoot)
		if err != nil {
			return err
		}
		return manager.Up(ctx, cfg, paths)
	case "down":
		repoRoot, err := parseCommonArgs("down", args[1:], c.errOut)
		if err != nil {
			return err
		}
		cfg, paths, err := NewRuntimeConfig("", repoRoot)
		if err != nil {
			return err
		}
		return manager.Down(ctx, cfg, paths)
	case "status":
		asJSON, repoRoot, err := parseStatusArgs(args[1:], c.errOut)
		if err != nil {
			return err
		}
		cfg, paths, err := NewRuntimeConfig("", repoRoot)
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
		scope, repoRoot, err := parseResetArgs(args[1:], c.errOut)
		if err != nil {
			return err
		}
		cfg, paths, err := NewRuntimeConfig("", repoRoot)
		if err != nil {
			return err
		}
		return manager.Reset(ctx, cfg, paths, scope)
	case "service":
		service, action, repoRoot, err := parseServiceArgs(args[1:], c.errOut)
		if err != nil {
			return err
		}
		cfg, paths, err := NewRuntimeConfig("", repoRoot)
		if err != nil {
			return err
		}
		return manager.RunServiceAction(ctx, cfg, paths, service, action)
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
		service, repoRoot, err := parseAlgorithmInternalArgs(sub, subArgs, c.errOut)
		if err != nil {
			return err
		}
		cfg, paths, err := NewRuntimeConfig("", repoRoot)
		if err != nil {
			return err
		}
		if sub == "algorithm-run" {
			return manager.algorithm.Run(ctx, cfg, paths, service)
		}
		return manager.algorithm.Down(ctx, paths, service)
	}
	repoRoot, err := parseCommonArgs("internal", subArgs, c.errOut)
	if err != nil {
		return err
	}
	cfg, paths, err := NewRuntimeConfig("", repoRoot)
	if err != nil {
		return err
	}

	switch sub {
	case "local-proxy-run":
		return manager.localProxy.Run(ctx, cfg, paths)
	case "local-proxy-down":
		return manager.localProxy.Down(ctx, cfg, paths)
	case "auth-service-run":
		return manager.authService.Run(ctx, cfg, paths)
	case "auth-service-down":
		return manager.authService.Down(ctx, cfg, paths)
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
	default:
		return fmt.Errorf("unknown internal command: %s", sub)
	}
}

func parseAlgorithmInternalArgs(name string, args []string, out io.Writer) (string, string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	service := fs.String("service", "", "")
	repoRoot := fs.String("repo-root", "", "")
	if err := fs.Parse(args); err != nil {
		return "", "", err
	}
	if len(fs.Args()) != 0 {
		return "", "", fmt.Errorf("unexpected positional args: %v", fs.Args())
	}
	if *service == "" {
		return "", "", fmt.Errorf("--service is required")
	}
	return *service, *repoRoot, nil
}

func parseCommonArgs(name string, args []string, out io.Writer) (string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	repoRoot := fs.String("repo-root", "", "")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if len(fs.Args()) != 0 {
		return "", fmt.Errorf("unexpected positional args: %v", fs.Args())
	}
	return *repoRoot, nil
}

func parseStatusArgs(args []string, out io.Writer) (bool, string, error) {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(out)
	asJSON := fs.Bool("json", false, "")
	repoRoot := fs.String("repo-root", "", "")
	if err := fs.Parse(args); err != nil {
		return false, "", err
	}
	if len(fs.Args()) != 0 {
		return false, "", fmt.Errorf("unexpected positional args: %v", fs.Args())
	}
	return *asJSON, *repoRoot, nil
}

func parseResetArgs(args []string, out io.Writer) (ResetScope, string, error) {
	fs := flag.NewFlagSet("reset", flag.ContinueOnError)
	fs.SetOutput(out)
	scopeText := fs.String("scope", string(ResetScopeKB), "")
	repoRoot := fs.String("repo-root", "", "")
	if err := fs.Parse(args); err != nil {
		return "", "", err
	}
	if len(fs.Args()) != 0 {
		return "", "", fmt.Errorf("unexpected positional args: %v", fs.Args())
	}
	scope, err := parseResetScope(*scopeText)
	if err != nil {
		return "", "", err
	}
	return scope, *repoRoot, nil
}

func parseServiceArgs(args []string, out io.Writer) (string, string, string, error) {
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	fs.SetOutput(out)
	service := fs.String("name", "", "")
	action := fs.String("action", "", "")
	repoRoot := fs.String("repo-root", "", "")
	if err := fs.Parse(args); err != nil {
		return "", "", "", err
	}
	if len(fs.Args()) != 0 {
		return "", "", "", fmt.Errorf("unexpected positional args: %v", fs.Args())
	}
	if strings.TrimSpace(*service) == "" {
		return "", "", "", fmt.Errorf("--name is required")
	}
	if strings.TrimSpace(*action) == "" {
		return "", "", "", fmt.Errorf("--action is required")
	}
	return *service, *action, *repoRoot, nil
}

func (c *CLI) usage() {
	_, _ = io.WriteString(c.out, "Usage:\n")
	_, _ = io.WriteString(c.out, "  local-runtime-manager up\n")
	_, _ = io.WriteString(c.out, "  local-runtime-manager down\n")
	_, _ = io.WriteString(c.out, "  local-runtime-manager status --json\n")
	_, _ = io.WriteString(c.out, "  local-runtime-manager reset --scope kb|all\n")
	_, _ = io.WriteString(c.out, "  local-runtime-manager service --name file-watcher --action build|start|stop\n")
	_, _ = io.WriteString(c.out, "  local-runtime-manager internal local-proxy-run|local-proxy-down|auth-service-run|auth-service-down|core-run|core-down|scan-control-plane-run|scan-control-plane-down|file-watcher-run|file-watcher-down|frontend-run|frontend-down|milvus-lite-run|milvus-lite-down|algorithm-run|algorithm-down\n")
}
