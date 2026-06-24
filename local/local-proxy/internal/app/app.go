package app

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/lazyagi/lazymind/local_proxy/internal/config"
	"github.com/lazyagi/lazymind/local_proxy/internal/server"
)

type App struct {
	server *http.Server
}

func New(cfg config.Config) *App {
	return &App{
		server: server.NewServer(cfg),
	}
}

func (a *App) Run(ctx context.Context) error {
	serverErr := make(chan error, 1)
	go func() {
		if err := a.server.ListenAndServe(); err != nil {
			serverErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return ctx.Err()
	case err := <-serverErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
