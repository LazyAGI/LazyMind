package assistantbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"lazymind/agentconnector/internal/adapters/codex"
	"lazymind/agentconnector/internal/adapters/mcpclient"
	"lazymind/agentconnector/internal/codexcontrol"
	"lazymind/agentconnector/internal/mcpbridge"
)

const DefaultAddress = codexcontrol.DefaultAddress

type Server struct {
	address string
	bridge  *mcpbridge.Bridge
	control *codexcontrol.Controller
	mu      sync.Mutex
	stop    context.CancelFunc
}

func New(address string, bridge *mcpbridge.Bridge) (*Server, error) {
	if bridge == nil {
		return nil, errors.New("MCP bridge is required")
	}
	address = strings.TrimSpace(address)
	if address == "" {
		address = DefaultAddress
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid Assistant Bridge address: %w", err)
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, errors.New("Assistant Bridge must listen on the loopback interface")
	}
	control, err := codexcontrol.New()
	if err != nil {
		return nil, err
	}
	return &Server{address: address, bridge: bridge, control: control}, nil
}

func Start(ctx context.Context, address string) (map[string]any, error) {
	if status, err := Health(ctx, address); err == nil {
		return status, nil
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	home, err := assistantHome()
	if err != nil {
		return nil, err
	}
	logDir := filepath.Join(home, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, err
	}
	logPath := filepath.Join(logDir, "assistant-bridge.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	command := exec.Command(self, "assistant", "serve", "--listen", address)
	command.SysProcAttr = detachedProcessAttributes()
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return nil, err
	}
	_ = command.Process.Release()
	_ = logFile.Close()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, healthErr := Health(ctx, address)
		if healthErr == nil {
			return status, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("Assistant Bridge did not start; inspect %s", logPath)
}

func Stop(ctx context.Context, address string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+address+"/v1/shutdown", nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		if _, healthErr := Health(ctx, address); healthErr != nil {
			return nil
		}
		return err
	}
	_ = response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Assistant Bridge stop returned HTTP %d", response.StatusCode)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := Health(ctx, address); err != nil {
			return nil
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return errors.New("Assistant Bridge did not stop before the deadline")
}

func Health(ctx context.Context, address string) (map[string]any, error) {
	requestCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, "http://"+address+"/v1/health", nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Assistant Bridge health returned HTTP %d", response.StatusCode)
	}
	var status map[string]any
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		return nil, err
	}
	status["running"] = true
	return status, nil
}

func assistantHome() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("LAZYMIND_HOME")); configured != "" {
		return filepath.Abs(configured)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lazymind"), nil
}

func (s *Server) Serve(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	s.stop = cancel
	defer cancel()
	httpServer := &http.Server{
		Addr:              s.address,
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-runCtx.Done():
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = httpServer.Shutdown(shutdownCtx)
			shutdownCancel()
		case <-done:
		}
	}()
	err := httpServer.ListenAndServe()
	close(done)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "pid": os.Getpid(), "version": "v1"})
	})
	mux.HandleFunc("GET /v1/agents", s.handleAgentStatuses)
	mux.HandleFunc("GET /v1/agents/{agent}", s.handleAgentStatus)
	mux.HandleFunc("POST /v1/agents/{agent}/{action}", s.handleAgentAction)
	mux.HandleFunc("POST /v1/shutdown", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
		go s.stop()
	})
	s.control.RegisterRoutes(mux)
	return s.allowLocalBrowser(mux)
}

func (s *Server) CodexControl() *codexcontrol.Controller { return s.control }

func (s *Server) handleAgentStatuses(writer http.ResponseWriter, request *http.Request) {
	probe, probeErr := s.bridge.Probe(request.Context())
	statuses := make(map[string]any, 5)
	codexAdapter, err := codex.NewWithControl("", "", s.bridge, s.address, s.control)
	if err != nil {
		writeError(writer, err)
		return
	}
	statuses["codex"], err = codexAdapter.StatusWithProbe(request.Context(), probe, probeErr)
	if err != nil {
		writeError(writer, err)
		return
	}
	for _, agent := range []string{string(mcpclient.Cursor), string(mcpclient.WorkBuddy), string(mcpclient.TRAEWork), string(mcpclient.DeepSeekHarness)} {
		adapter, adapterErr := s.mcpClient(agent)
		if adapterErr != nil {
			writeError(writer, adapterErr)
			return
		}
		statuses[agent] = adapter.StatusWithProbe(probe, probeErr)
	}
	writeJSON(writer, http.StatusOK, map[string]any{"agents": statuses})
}

func (s *Server) handleAgentStatus(writer http.ResponseWriter, request *http.Request) {
	status, err := s.agentStatus(request.Context(), request.PathValue("agent"))
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func (s *Server) handleAgentAction(writer http.ResponseWriter, request *http.Request) {
	action := strings.ToLower(strings.TrimSpace(request.PathValue("action")))
	if action != "connect" && action != "disconnect" {
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": "unsupported Assistant action"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	status, err := s.agentAction(request.Context(), request.PathValue("agent"), action)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func (s *Server) agentStatus(ctx context.Context, agent string) (any, error) {
	agent = strings.ToLower(strings.TrimSpace(agent))
	if agent == "codex" {
		adapter, err := codex.NewWithControl("", "", s.bridge, s.address, s.control)
		if err != nil {
			return nil, err
		}
		return adapter.Status(ctx)
	}
	adapter, err := s.mcpClient(agent)
	if err != nil {
		return nil, err
	}
	return adapter.Status(ctx), nil
}

func (s *Server) agentAction(ctx context.Context, agent, action string) (any, error) {
	agent = strings.ToLower(strings.TrimSpace(agent))
	if agent == "codex" {
		adapter, err := codex.NewWithControl("", "", s.bridge, s.address, s.control)
		if err != nil {
			return nil, err
		}
		if action == "connect" {
			return adapter.Connect(ctx)
		}
		return adapter.Disconnect(ctx)
	}
	adapter, err := s.mcpClient(agent)
	if err != nil {
		return nil, err
	}
	if action == "connect" {
		return adapter.Connect(ctx)
	}
	return adapter.Disconnect(ctx)
}

func (s *Server) mcpClient(agent string) (*mcpclient.Adapter, error) {
	kind := mcpclient.Kind(agent)
	switch kind {
	case mcpclient.Cursor, mcpclient.WorkBuddy, mcpclient.TRAEWork, mcpclient.DeepSeekHarness:
		return mcpclient.New(kind, "", "", s.bridge)
	default:
		return nil, fmt.Errorf("unsupported Assistant %q", agent)
	}
}

func (s *Server) allowLocalBrowser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := strings.TrimSpace(request.Header.Get("Origin"))
		if origin != "" && !localOrigin(origin) {
			writeJSON(writer, http.StatusForbidden, map[string]string{"error": "Assistant Bridge only accepts local LazyMind pages"})
			return
		}
		if origin != "" {
			writer.Header().Set("Access-Control-Allow-Origin", origin)
			writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			writer.Header().Set("Vary", "Origin")
		}
		if request.Method == http.MethodOptions {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func localOrigin(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func writeError(writer http.ResponseWriter, err error) {
	writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
