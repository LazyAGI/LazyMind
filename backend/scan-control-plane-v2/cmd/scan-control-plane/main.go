package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/lazymind/scan_control_plane/internal/app"
	"github.com/lazymind/scan_control_plane/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config failed: %v\n", err)
		os.Exit(1)
	}

	application := app.New(cfg)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(os.Stdout, "scan-control-plane-v2 listening on %s\n", cfg.ListenAddr())
	if err := application.Run(ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "run app failed: %v\n", err)
		os.Exit(1)
	}
}
