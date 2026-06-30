package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
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
		profile, repoRoot, err := parseCommonArgs("up", args[1:], c.errOut)
		if err != nil {
			return err
		}
		cfg, paths, err := NewRuntimeConfig(profile, repoRoot)
		if err != nil {
			return err
		}
		return manager.Up(ctx, cfg, paths)
	case "down":
		profile, repoRoot, err := parseCommonArgs("down", args[1:], c.errOut)
		if err != nil {
			return err
		}
		cfg, paths, err := NewRuntimeConfig(profile, repoRoot)
		if err != nil {
			return err
		}
		return manager.Down(ctx, cfg, paths)
	case "status":
		asJSON, profile, repoRoot, err := parseStatusArgs(args[1:], c.errOut)
		if err != nil {
			return err
		}
		cfg, paths, err := NewRuntimeConfig(profile, repoRoot)
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
		service, profile, repoRoot, err := parseAlgorithmInternalArgs(sub, subArgs, c.errOut)
		if err != nil {
			return err
		}
		cfg, paths, err := NewRuntimeConfig(profile, repoRoot)
		if err != nil {
			return err
		}
		if sub == "algorithm-run" {
			return manager.algorithm.Run(ctx, cfg, paths, service)
		}
		return manager.algorithm.Down(ctx, paths, service)
	}
	profile, repoRoot, err := parseCommonArgs("internal", subArgs, c.errOut)
	if err != nil {
		return err
	}
	cfg, paths, err := NewRuntimeConfig(profile, repoRoot)
	if err != nil {
		return err
	}

	switch sub {
	case "compose-up":
		return manager.compose.ComposeUp(ctx, cfg, paths)
	case "compose-down":
		return manager.compose.ComposeDown(ctx, paths.RepoRoot, cfg.Profile)
	case "compose-services":
		services, err := manager.compose.ComposeServices(ctx, paths.RepoRoot)
		if err != nil {
			return err
		}
		for _, svc := range services {
			_, _ = io.WriteString(c.out, svc+"\n")
		}
		return nil
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
	case "frontend-run":
		return manager.frontend.Run(ctx, cfg, paths)
	case "frontend-down":
		return manager.frontend.Down(ctx, cfg, paths)
	default:
		return fmt.Errorf("unknown internal command: %s", sub)
	}
}

func parseAlgorithmInternalArgs(name string, args []string, out io.Writer) (string, string, string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	service := fs.String("service", "", "")
	profile := fs.String("profile", defaultProfileValue(), "")
	repoRoot := fs.String("repo-root", "", "")
	if err := fs.Parse(args); err != nil {
		return "", "", "", err
	}
	if len(fs.Args()) != 0 {
		return "", "", "", fmt.Errorf("unexpected positional args: %v", fs.Args())
	}
	if *service == "" {
		return "", "", "", fmt.Errorf("--service is required")
	}
	return *service, *profile, *repoRoot, nil
}

func parseCommonArgs(name string, args []string, out io.Writer) (string, string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	profile := fs.String("profile", defaultProfileValue(), "")
	repoRoot := fs.String("repo-root", "", "")
	if err := fs.Parse(args); err != nil {
		return "", "", err
	}
	if len(fs.Args()) != 0 {
		return "", "", fmt.Errorf("unexpected positional args: %v", fs.Args())
	}
	return *profile, *repoRoot, nil
}

func parseStatusArgs(args []string, out io.Writer) (bool, string, string, error) {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(out)
	asJSON := fs.Bool("json", false, "")
	profile := fs.String("profile", defaultProfileValue(), "")
	repoRoot := fs.String("repo-root", "", "")
	if err := fs.Parse(args); err != nil {
		return false, "", "", err
	}
	if len(fs.Args()) != 0 {
		return false, "", "", fmt.Errorf("unexpected positional args: %v", fs.Args())
	}
	return *asJSON, *profile, *repoRoot, nil
}

func (c *CLI) usage() {
	_, _ = io.WriteString(c.out, "Usage:\n")
	_, _ = io.WriteString(c.out, "  lazymind-local up --profile <profile>\n")
	_, _ = io.WriteString(c.out, "  lazymind-local down --profile <profile>\n")
	_, _ = io.WriteString(c.out, "  lazymind-local status --json\n")
	_, _ = io.WriteString(c.out, "  lazymind-local internal compose-up|compose-down|compose-services|local-proxy-run|local-proxy-down|auth-service-run|auth-service-down|core-run|core-down|frontend-run|frontend-down|algorithm-run|algorithm-down --profile <profile>\n")
}
