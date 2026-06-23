package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lazyagi/lazymind/local_proxy/internal/app"
	"github.com/lazyagi/lazymind/local_proxy/internal/config"
)

const (
	defaultAddress = "127.0.0.1"
	defaultPort    = 5024
)

func main() {
	flagSet := flag.NewFlagSet("local-proxy", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	cfgPath := flagSet.String("config", "", "path to config file")
	if err := flagSet.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "invalid args: %v\n", err)
		os.Exit(1)
	}
	if len(flagSet.Args()) > 1 {
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", flagSet.Args()[0])
		os.Exit(1)
	}
	if len(flagSet.Args()) == 1 && flagSet.Args()[0] == "healthcheck" {
		if err := healthcheck(); err != nil {
			fmt.Fprintf(os.Stderr, "healthcheck failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if len(flagSet.Args()) == 1 {
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", flagSet.Args()[0])
		os.Exit(1)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config failed: %v\n", err)
		os.Exit(1)
	}

	application := app.New(cfg)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(os.Stdout, "local-proxy listening on %s\n", cfg.ListenAddr())
	if err := application.Run(ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "run app failed: %v\n", err)
		os.Exit(1)
	}
}

func healthcheck() error {
	port := os.Getenv("LAZYMIND_LOCAL_PROXY_PORT")
	if port == "" {
		port = fmt.Sprint(defaultPort)
	}
	addr := os.Getenv("LAZYMIND_LOCAL_PROXY_ADDRESS")
	if addr == "" {
		addr = defaultAddress
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + ":" + port + "/_local/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}
	if value, ok := payload["status"]; !ok || value != "ok" {
		return fmt.Errorf("unexpected payload %q", string(body))
	}
	return nil
}
