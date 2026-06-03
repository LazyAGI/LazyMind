package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lazymind/scan_control_plane/internal/app"
	"github.com/lazymind/scan_control_plane/internal/config"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := healthcheck(); err != nil {
			fmt.Fprintf(os.Stderr, "healthcheck failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config failed: %v\n", err)
		os.Exit(1)
	}

	application := app.New(cfg)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(os.Stdout, "scan-control-plane listening on %s\n", cfg.ListenAddr())
	if err := application.Run(ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "run app failed: %v\n", err)
		os.Exit(1)
	}
}

func healthcheck() error {
	address := os.Getenv("LAZYMIND_SCAN_CONTROL_PLANE_ADDRESS")
	if address == "" || address == "0.0.0.0" {
		address = "127.0.0.1"
	}
	port := os.Getenv("LAZYMIND_SCAN_CONTROL_PLANE_PORT")
	if port == "" {
		port = "18080"
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + address + ":" + port + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}
