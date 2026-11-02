//go:build lazymind_unsigned_test

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/unsignedupdate"
)

var embeddedConfig string

func run() error {
	p, err := desktopupdate.UnsignedPaths()
	if err != nil {
		return err
	}
	raw, err := base64.StdEncoding.DecodeString(embeddedConfig)
	if err != nil {
		return err
	}
	var c desktopupdate.Config
	if json.Unmarshal(raw, &c) != nil || c.Mode != "unsigned-test" {
		return fmt.Errorf("unsigned_test_disabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	i := unsignedupdate.Installer{Config: c, Paths: p, Verify: unsignedupdate.VerifyMacBundle, Inspect: unsignedupdate.InspectMacBundle, SameProcess: desktopupdate.SameProcess,
		Launch: func(ctx context.Context, app string) error {
			return exec.CommandContext(ctx, "/usr/bin/open", "-n", app).Run()
		}}
	if len(os.Args) == 2 && os.Args[1] == "prepare" {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 16385))
		if err != nil || len(b) > 16384 {
			return fmt.Errorf("unsigned_test_request")
		}
		var request unsignedupdate.Request
		if json.Unmarshal(b, &request) != nil {
			return fmt.Errorf("unsigned_test_request")
		}
		parent, err := desktopupdate.IdentifyProcess(os.Getppid())
		if err != nil {
			return err
		}
		id, err := i.Prepare(ctx, request, parent)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "prepared", "id": id})
	}
	if len(os.Args) == 3 && os.Args[1] == "install" {
		caller, err := desktopupdate.IdentifyProcess(os.Getppid())
		if err != nil {
			return err
		}
		return i.Install(ctx, os.Args[2], caller, func() error { return json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "accepted"}) })
	}
	if len(os.Args) == 3 && os.Args[1] == "discard" {
		if err := i.Discard(os.Args[2]); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "discarded"})
	}
	return fmt.Errorf("unsigned_test_command")
}
func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "unsigned_test_failed")
		os.Exit(1)
	}
}
