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
	cursoradapter "lazymind/agentconnector/internal/adapters/cursor"
	"lazymind/agentconnector/internal/adapters/mcpclient"
	workbuddyadapter "lazymind/agentconnector/internal/adapters/workbuddy"
	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/executorpolicy"
	"lazymind/agentconnector/internal/mcpbridge"
)

const (
	DefaultAddress    = "127.0.0.1:19091"
	agentLoginTimeout = 2 * time.Minute
)

type Server struct {
	address string
	bridge  *mcpbridge.Bridge
	store   *credentials.Store
	policy  *executorpolicy.Store
	mu      sync.Mutex
	stop    context.CancelFunc
	loginMu sync.Mutex
	logins  map[string]agentLogin
	loginID uint64

	loginOverride func(context.Context, string) error
}

type agentLogin struct {
	id     uint64
	cancel context.CancelFunc
}

func New(address string, bridge *mcpbridge.Bridge, store *credentials.Store, policy *executorpolicy.Store) (*Server, error) {
	if bridge == nil || store == nil || policy == nil {
		return nil, errors.New("MCP bridge, credential store, and execution policy are required")
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
	return &Server{
		address: address, bridge: bridge, store: store, policy: policy,
		logins: make(map[string]agentLogin),
	}, nil
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
	defer s.cancelAgentLogins()
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

func (s *Server) cancelAgentLogins() {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	for _, login := range s.logins {
		login.cancel()
	}
	clear(s.logins)
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "pid": os.Getpid(), "version": "v1"})
	})
	mux.HandleFunc("GET /v1/agents", s.handleAgentStatuses)
	mux.HandleFunc("GET /v1/agents/{agent}", s.handleAgentStatus)
	mux.HandleFunc("POST /v1/agents/{agent}/{action}", s.handleAgentAction)
	mux.HandleFunc("GET /v1/executors", s.handleExecutorPolicies)
	mux.HandleFunc("POST /v1/executors/{provider}/{action}", s.handleExecutorPolicyAction)
	mux.HandleFunc("GET /v1/bindings", s.handleExecutableBindings)
	mux.HandleFunc("PUT /v1/bindings/{target}", s.handleExecutableBinding)
	mux.HandleFunc("DELETE /v1/bindings/{target}", s.handleExecutableBinding)
	mux.HandleFunc("POST /v1/session", s.handleSession)
	mux.HandleFunc("DELETE /v1/session", s.handleSession)
	mux.HandleFunc("POST /v1/shutdown", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
		go s.stop()
	})
	return s.allowLocalBrowser(mux)
}

func (s *Server) handleExecutableBindings(writer http.ResponseWriter, _ *http.Request) {
	bindings, err := agentexec.ExecutableBindings()
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"bindings": bindings})
}

func (s *Server) handleExecutableBinding(writer http.ResponseWriter, request *http.Request) {
	target := agentexec.BindingTarget(request.PathValue("target"))
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.Method == http.MethodDelete {
		if err := agentexec.ClearExecutableBinding(target); err != nil {
			writeError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{
			"target": target, "configured": false, "path": "",
		})
		return
	}
	var input struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 4<<10)).Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid executable binding"})
		return
	}
	path, err := agentexec.SetExecutableBinding(target, input.Path)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"target": target, "configured": true, "path": path,
	})
}

func (s *Server) handleExecutorPolicies(writer http.ResponseWriter, _ *http.Request) {
	s.policy.Recheck()
	statuses, err := ExecutorStatuses(s.policy)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"executors": statuses})
}

func ExecutorStatuses(policy *executorpolicy.Store) (map[string]executorpolicy.Status, error) {
	statuses, err := policy.Statuses()
	if err != nil {
		return nil, err
	}
	probes := map[string]func(string) (bool, bool, string){
		"codex": codex.Probe, "cursor": cursoradapter.Probe, "workbuddy": workbuddyadapter.Probe,
	}
	for provider, probe := range probes {
		status := statuses[provider]
		status.Installed, status.Ready, status.UnavailableReason = probe("")
		statuses[provider] = status
	}
	return statuses, nil
}

func (s *Server) handleExecutorPolicyAction(writer http.ResponseWriter, request *http.Request) {
	action := strings.ToLower(strings.TrimSpace(request.PathValue("action")))
	if action != "enable" && action != "disable" {
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": "unsupported executor action"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	status, err := s.policy.SetEnabled(request.PathValue("provider"), action == "enable")
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func (s *Server) handleSession(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodDelete {
		if err := s.store.Clear(); err != nil {
			writeError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	var value credentials.Credentials
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20)).Decode(&value); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid LazyMind session"})
		return
	}
	if origin := strings.TrimSpace(request.Header.Get("Origin")); origin != "" && !sameOrigin(origin, value.ServerURL) {
		writeJSON(writer, http.StatusForbidden, map[string]string{"error": "LazyMind session server does not match the page origin"})
		return
	}
	if err := s.store.Save(value); err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAgentStatuses(writer http.ResponseWriter, request *http.Request) {
	statuses, err := Statuses(request.Context(), s.bridge)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"agents": statuses})
}

func Statuses(ctx context.Context, bridge *mcpbridge.Bridge) (map[string]agentintegration.Status, error) {
	statuses := make(map[string]agentintegration.Status, 6)
	codexAdapter, err := codex.New("", "", bridge)
	if err != nil {
		return nil, err
	}
	statuses["codex"] = codexAdapter.Status(ctx)
	for _, agent := range []string{
		string(mcpclient.Cursor), string(mcpclient.WorkBuddy), string(mcpclient.Raccoon),
		string(mcpclient.TRAEWork), string(mcpclient.DeepSeekHarness),
	} {
		adapter, err := newMCPClient(agent, bridge)
		if err != nil {
			return nil, err
		}
		statuses[agent] = adapter.Status(ctx)
	}
	return statuses, nil
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
	if action != "connect" && action != "disconnect" && action != "login" {
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

func (s *Server) agentStatus(ctx context.Context, agent string) (agentintegration.Status, error) {
	agent = strings.ToLower(strings.TrimSpace(agent))
	if agent == "codex" {
		adapter, err := codex.New("", "", s.bridge)
		if err != nil {
			return agentintegration.Status{}, err
		}
		return adapter.Status(ctx), nil
	}
	adapter, err := s.mcpClient(agent)
	if err != nil {
		return agentintegration.Status{}, err
	}
	return adapter.Status(ctx), nil
}

func (s *Server) agentAction(ctx context.Context, agent, action string) (agentintegration.Status, error) {
	agent = strings.ToLower(strings.TrimSpace(agent))
	if agent == "codex" {
		adapter, err := codex.New("", "", s.bridge)
		if err != nil {
			return agentintegration.Status{}, err
		}
		switch action {
		case "connect":
			return adapter.Connect(ctx), nil
		case "disconnect":
			return adapter.Disconnect(ctx), nil
		case "login":
			s.startAgentLogin(agent, func(loginCtx context.Context) error {
				status := adapter.Login(loginCtx)
				if status.State == agentintegration.Failed {
					return errors.New(status.Message)
				}
				return nil
			})
			return loginOpenedStatus(adapter.Status(ctx)), nil
		default:
			return agentintegration.Status{}, fmt.Errorf("unsupported Codex action %q", action)
		}
	}
	adapter, err := s.mcpClient(agent)
	if err != nil {
		return agentintegration.Status{}, err
	}
	switch action {
	case "connect":
		return adapter.Connect(ctx), nil
	case "disconnect":
		return adapter.Disconnect(ctx), nil
	case "login":
		if agent != string(mcpclient.Cursor) {
			return agentintegration.Status{}, fmt.Errorf("unsupported %s action %q", agent, action)
		}
		s.startAgentLogin(agent, func(loginCtx context.Context) error {
			return cursoradapter.Login(loginCtx, "")
		})
		return loginOpenedStatus(adapter.Status(ctx)), nil
	default:
		return agentintegration.Status{}, fmt.Errorf("unsupported %s action %q", agent, action)
	}
}

func loginOpenedStatus(status agentintegration.Status) agentintegration.Status {
	status.Message = "Login opened. Complete or close it, then return to LazyMind and check again."
	return status
}

func (s *Server) startAgentLogin(agent string, login func(context.Context) error) {
	s.loginMu.Lock()
	if current, ok := s.logins[agent]; ok {
		current.cancel()
	}
	loginCtx, cancel := context.WithTimeout(context.Background(), agentLoginTimeout)
	s.loginID++
	id := s.loginID
	s.logins[agent] = agentLogin{id: id, cancel: cancel}
	override := s.loginOverride
	s.loginMu.Unlock()

	go func() {
		defer cancel()
		defer s.policy.Recheck()
		if override != nil {
			_ = override(loginCtx, agent)
		} else {
			_ = login(loginCtx)
		}
		s.loginMu.Lock()
		if current, ok := s.logins[agent]; ok && current.id == id {
			delete(s.logins, agent)
		}
		s.loginMu.Unlock()
	}()
}

func (s *Server) mcpClient(agent string) (*mcpclient.Adapter, error) {
	return newMCPClient(agent, s.bridge)
}

func newMCPClient(agent string, bridge *mcpbridge.Bridge) (*mcpclient.Adapter, error) {
	kind := mcpclient.Kind(agent)
	switch kind {
	case mcpclient.Cursor, mcpclient.WorkBuddy, mcpclient.Raccoon, mcpclient.TRAEWork, mcpclient.DeepSeekHarness:
		return mcpclient.New(kind, "", bridge)
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
			writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
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

func sameOrigin(left, right string) bool {
	leftURL, leftErr := url.Parse(left)
	rightURL, rightErr := url.Parse(right)
	return leftErr == nil && rightErr == nil &&
		strings.EqualFold(leftURL.Scheme, rightURL.Scheme) && strings.EqualFold(leftURL.Host, rightURL.Host)
}

func writeError(writer http.ResponseWriter, err error) {
	writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
