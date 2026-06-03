package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/lazymind/file_watcher/internal/config"
)

// NewServer creates and configures the HTTP server.
func NewServer(cfg *config.Config, handler *Handler, log *zap.Logger) *http.Server {
	mux := http.NewServeMux()
	registerRoutes(mux, handler, cfg.AgentToken, log)

	return &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      accessLogMiddleware(log, mux),
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
	}
}

func registerRoutes(mux *http.ServeMux, h *Handler, token string, log *zap.Logger) {
	auth := bearerAuth(token, log)

	mux.HandleFunc("/healthz", h.Healthz)
	mux.HandleFunc("/api/v1/fs/browse", auth(h.Browse))
	mux.HandleFunc("/api/v1/fs/tree", auth(h.Tree))
	mux.HandleFunc("/api/v1/fs/validate", auth(h.ValidatePath))
	mux.HandleFunc("/api/v1/fs/stat", auth(h.StatFile))
	mux.HandleFunc("/api/v1/fs/stage", auth(h.StageFile))
	mux.HandleFunc("/api/v1/agents/fs/validate", auth(h.AgentValidatePath))
	mux.HandleFunc("/api/v1/agents/fs/list", auth(h.AgentListDir))
	mux.HandleFunc("/api/v1/agents/fs/stat", auth(h.AgentStatPath))
	mux.HandleFunc("/api/v1/agents/fs/export", auth(h.AgentExportFile))
	mux.HandleFunc("/api/v1/sources/start", auth(h.StartSource))
	mux.HandleFunc("/api/v1/sources/stop", auth(h.StopSource))
}

// bearerAuth is Bearer Token authentication middleware.
func bearerAuth(token string, log *zap.Logger) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != token {
				http.Error(w, `{"code":"UNAUTHORIZED","message":"invalid token"}`, http.StatusUnauthorized)
				log.Warn("unauthorized request", zap.String("path", r.URL.Path), zap.String("remote", r.RemoteAddr))
				return
			}
			next(w, r)
		}
	}
}

// GracefulShutdown gracefully shuts down the HTTP server.
func GracefulShutdown(srv *http.Server, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return srv.Shutdown(ctx)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func accessLogMiddleware(log *zap.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		reqSize := r.ContentLength
		if reqSize < 0 {
			reqSize = 0
		}
		log.Info("http access",
			zap.String("path", r.URL.Path),
			zap.String("method", r.Method),
			zap.Int("status", rec.status),
			zap.Duration("latency", time.Since(startedAt)),
			zap.Int64("request_size", reqSize),
		)
	})
}
