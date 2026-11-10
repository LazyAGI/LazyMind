package assistantbridge

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/localfile"
)

const bridgeProtocol = "v9"
const bridgeProduct = "lazymind-assistant-bridge"
const managementHeader = "X-LazyMind-Bridge-Control"

type ClientOptions struct {
	ID        string `json:"id"`
	Home      string `json:"home"`
	OwnerPID  int    `json:"owner_pid,omitempty"`
	ServerURL string `json:"server_url,omitempty"`
}

type sharedBridge struct {
	mu       sync.Mutex
	server   *Server
	root     string
	token    string
	clients  map[string]ClientOptions
	children map[string]string
	stopping bool
}

func currentClient(options ClientOptions) (ClientOptions, string, error) {
	home, err := connectorconfig.Home()
	if err != nil {
		return options, "", err
	}
	root, err := connectorconfig.RootHome(home)
	if err != nil {
		return options, "", err
	}
	if options.ServerURL != "" {
		identity, err := connectorconfig.OriginIdentity(options.ServerURL)
		if err != nil {
			return options, "", err
		}
		home, err = connectorconfig.EnvironmentHome(root, identity)
		if err != nil {
			return options, "", err
		}
	}
	options.Home = home
	if options.ID == "" {
		if owner := os.Getenv("LAZYMIND_DESKTOP_OWNER_PID"); owner != "" {
			options.OwnerPID, err = strconv.Atoi(owner)
			if err != nil || options.OwnerPID <= 0 {
				return options, "", errors.New("invalid Desktop owner PID")
			}
			options.ID = "desktop:" + owner + ":" + os.Getenv("LAZYMIND_RUNTIME_OWNER_TOKEN")
		} else {
			options.ID = "manual:" + home
		}
	}
	return options, root, nil
}

func bridgeControlToken(root string) (string, error) {
	path := filepath.Join(root, "bridge-control.token")
	body, err := os.ReadFile(path)
	if err == nil {
		if len(body) != 64 {
			return "", errors.New("invalid Assistant Bridge control token")
		}
		return string(body), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	if err := localfile.Write(path, []byte(token), 0600); err != nil {
		return "", err
	}
	return token, nil
}

// Immutable copies let an application upgrade leave an in-use shared daemon intact.
func stageBridgeExecutable(root string) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	source, err := os.Open(self)
	if err != nil {
		return "", err
	}
	defer source.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, source); err != nil {
		return "", err
	}
	path := filepath.Join(root, "bridge", "bin", hex.EncodeToString(hash.Sum(nil)), filepath.Base(self))
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".bridge-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(temp.Name())
	if _, err := io.Copy(temp, source); err != nil {
		_ = temp.Close()
		return "", err
	}
	if err := temp.Chmod(0700); err != nil {
		_ = temp.Close()
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	if err := localfile.Replace(temp.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}

func bridgeEnvironment(home string, isolated bool) []string {
	env := []string{}
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if key == connectorconfig.EnvironmentKey || key == "LAZYMIND_HOME" || key == "LAZYMIND_RUNTIME_PROFILE" || key == "LAZYMIND_RUNTIME_ROOT" || key == "LAZYMIND_RUNTIME_OWNER_TOKEN" ||
			key == "LAZYMIND_BRIDGE_ISOLATED" || strings.HasPrefix(key, "LAZYMIND_AGENT_") ||
			strings.HasPrefix(key, "LAZYMIND_WORKFLOW_") || strings.HasPrefix(key, "LAZYMIND_DESKTOP_OWNER_") {
			continue
		}
		env = append(env, item)
	}
	return append(env, "LAZYMIND_HOME="+home, "LAZYMIND_RUNTIME_PROFILE=bridge", "LAZYMIND_BRIDGE_ISOLATED="+strconv.FormatBool(isolated))
}

func compatibleBridge(status map[string]any, home string, isolated bool) bool {
	return status["service"] == bridgeProduct && status["version"] == bridgeProtocol &&
		status["home"] == home && status["isolated"] == isolated && status["platform"] == runtime.GOOS
}

func launchBridge(ctx context.Context, executable, home, address string, isolated bool) (map[string]any, error) {
	logPath := filepath.Join(home, "logs", "assistant-bridge.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0700); err != nil {
		return nil, err
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer log.Close()
	command := exec.Command(executable, "assistant", "serve", "--listen", address)
	command.Env = bridgeEnvironment(home, isolated)
	command.SysProcAttr = detachedProcessAttributes()
	command.Stdout, command.Stderr = log, log
	done, kill, err := startBridgeProcess(command)
	if err != nil {
		return nil, err
	}
	ticker := time.NewTicker(75 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	for {
		if status, err := Health(ctx, address); err == nil && compatibleBridge(status, home, isolated) {
			return status, nil
		}
		select {
		case err := <-done:
			return nil, fmt.Errorf("Assistant Bridge exited (%v); inspect %s", err, logPath)
		case <-ctx.Done():
			kill()
			return nil, ctx.Err()
		case <-timeout.C:
			kill()
			return nil, fmt.Errorf("Assistant Bridge startup timed out; inspect %s", logPath)
		case <-ticker.C:
		}
	}
}

func StartWithClient(ctx context.Context, address string, options ClientOptions) (map[string]any, error) {
	client, root, err := currentClient(options)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	unlock, err := localfile.Lock(filepath.Join(root, "bridge-start.lock"))
	if err != nil {
		return nil, err
	}
	defer unlock()
	token, err := bridgeControlToken(root)
	if err != nil {
		return nil, err
	}
	candidates := []string{}
	if record, err := connectorconfig.ReadBridgeEndpoint(root); err == nil {
		u, _ := url.Parse(record.Broker)
		candidates = append(candidates, u.Host)
	}
	// A context endpoint is never used as the shared broker's listening address.
	if record, err := connectorconfig.ReadBridgeEndpoint(client.Home); err == nil && record.URL == "http://"+address {
		u, _ := url.Parse(record.Broker)
		address = u.Host
	}
	if address == "" {
		address = DefaultAddress
	}
	if err := connectorconfig.ValidateURL("LAZYMIND_ASSISTANT_BRIDGE_URL", "http://"+address); err != nil {
		return nil, err
	}
	candidates = append(candidates, address)
	if address == DefaultAddress || strings.HasPrefix(address, "127.0.0.1:1909") {
		for port := 19091; port <= 19100; port++ {
			candidates = append(candidates, "127.0.0.1:"+strconv.Itoa(port))
		}
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		status, healthErr := Health(ctx, candidate)
		if healthErr == nil && !compatibleBridge(status, root, false) {
			// Old bridges have no scoped sessions. A second listener sharing their
			// legacy credential directory would not be safe isolation.
			executable, _ := status["executable"].(string)
			if (status["version"] == "v8" && executable != "") || (status["service"] == bridgeProduct && status["home"] == root) {
				return nil, errors.New("an incompatible Assistant Bridge is using this environment; it was preserved. Stop its owning deployment before upgrading the shared bridge, then retry")
			}
			continue
		}
		if healthErr != nil {
			listener, err := net.Listen("tcp", candidate)
			if err != nil {
				continue
			}
			_ = listener.Close()
			executable, err := stageBridgeExecutable(root)
			if err != nil {
				return nil, err
			}
			status, err = launchBridge(ctx, executable, root, candidate, false)
			if err != nil {
				return nil, err
			}
		}
		endpoint := connectorconfig.BridgeEndpoint{URL: "http://" + candidate, Broker: "http://" + candidate}
		if err := connectorconfig.WriteBridgeEndpoint(root, endpoint); err != nil {
			return nil, err
		}
		var registered struct {
			URL string `json:"url"`
		}
		if err := manageBridge(ctx, candidate, token, "acquire", client, &registered); err != nil {
			return nil, err
		}
		endpoint.URL = registered.URL
		if err := connectorconfig.WriteBridgeEndpoint(client.Home, endpoint); err != nil {
			return nil, err
		}
		status["bridge_url"] = registered.URL
		return status, nil
	}
	return nil, errors.New("Assistant Bridge unavailable: existing services are incompatible or ports are occupied; existing services were preserved. Close the obsolete bridge or configure a free loopback address and retry")
}

func manageBridge(ctx context.Context, address, token, action string, client ClientOptions, output any) error {
	body, _ := json.Marshal(client)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+address+"/v1/clients/"+action, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set(managementHeader, token)
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Assistant Bridge %s failed: %s", action, strings.TrimSpace(string(body)))
	}
	if output != nil {
		return json.NewDecoder(response.Body).Decode(output)
	}
	return nil
}

func ReleaseClient(ctx context.Context, options ClientOptions) error {
	client, root, err := currentClient(options)
	if err != nil {
		return err
	}
	endpoint, err := connectorconfig.ReadBridgeEndpoint(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	u, _ := url.Parse(endpoint.Broker)
	status, err := Health(ctx, u.Host)
	if err != nil {
		if listenerAbsent(u.Host) {
			return nil
		}
		return fmt.Errorf("cannot verify Assistant Bridge before release; service was preserved: %w", err)
	}
	if !compatibleBridge(status, root, false) {
		return errors.New("existing Assistant Bridge is not managed by this installation; it was preserved")
	}
	token, err := bridgeControlToken(root)
	if err != nil {
		return err
	}
	var released struct {
		Running bool `json:"running"`
	}
	if err := manageBridge(ctx, u.Host, token, "release", client, &released); err != nil {
		return err
	}
	if released.Running {
		return nil
	}
	pid, _ := status["pid"].(float64)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		if status, err := Health(ctx, u.Host); (err != nil || !compatibleBridge(status, root, false)) && (pid <= 0 || ownerExited(int(pid))) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("Assistant Bridge shutdown timed out; inspect the bridge log")
		case <-ticker.C:
		}
	}
}

func newSharedBridge(server *Server) (*sharedBridge, error) {
	root := server.store.Directory()
	token, err := bridgeControlToken(root)
	if err != nil {
		return nil, err
	}
	shared := &sharedBridge{server: server, root: root, token: token, clients: map[string]ClientOptions{}, children: map[string]string{}}
	body, err := os.ReadFile(filepath.Join(root, "bridge-clients.json"))
	if err == nil {
		if err := json.Unmarshal(body, &shared.clients); err != nil {
			return nil, err
		}
		if shared.clients == nil {
			return nil, errors.New("invalid Assistant Bridge client registry")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, client := range shared.clients {
		if endpoint, err := connectorconfig.ReadBridgeEndpoint(client.Home); err == nil && client.Home != root {
			shared.children[client.Home] = endpoint.URL
		}
	}
	return shared, nil
}

func (b *sharedBridge) saveClients() error {
	body, err := json.Marshal(b.clients)
	if err != nil {
		return err
	}
	return localfile.Write(filepath.Join(b.root, "bridge-clients.json"), body, 0600)
}

func (b *sharedBridge) watchOwners(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	pendingCleanup := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		b.mu.Lock()
		removed := map[string]bool{}
		for id, client := range b.clients {
			if client.OwnerPID > 0 && ownerExited(client.OwnerPID) {
				delete(b.clients, id)
				removed[client.Home] = true
			}
		}
		if len(removed) > 0 {
			pendingCleanup = true
			for _, client := range b.clients {
				if !strings.HasPrefix(client.ID, "browser:") {
					delete(removed, client.Home)
				}
			}
			for id, client := range b.clients {
				if strings.HasPrefix(id, "browser:") && removed[client.Home] {
					delete(b.clients, id)
				}
			}
		}
		if pendingCleanup && b.saveClients() == nil {
			pendingCleanup = false
			if len(b.clients) == 0 && !b.stopping {
				shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				if b.stopChildren(shutdownCtx) == nil {
					b.stopping = true
					if b.server.stop != nil {
						b.server.stop()
					}
				} else {
					pendingCleanup = true
				}
				cancel()
			}
		}
		b.mu.Unlock()
	}
}

func (b *sharedBridge) endpoint(ctx context.Context, home string) (string, error) {
	if home == b.root {
		return "http://" + b.server.address, nil
	}
	root, err := connectorconfig.RootHome(home)
	if err != nil || root != b.root {
		return "", errors.New("environment does not belong to this bridge")
	}
	previous, previousErr := connectorconfig.ReadBridgeEndpoint(home)
	if previousErr == nil {
		u, _ := url.Parse(previous.URL)
		if status, err := Health(ctx, u.Host); err == nil && compatibleBridge(status, home, true) {
			b.children[home] = previous.URL
			return previous.URL, nil
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	address := listener.Addr().String()
	_ = listener.Close()
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	if _, err := launchBridge(ctx, executable, home, address, true); err != nil {
		return "", err
	}
	endpoint := connectorconfig.BridgeEndpoint{URL: "http://" + address, Broker: "http://" + b.server.address}
	if err := connectorconfig.WriteBridgeEndpoint(home, endpoint); err != nil {
		return "", err
	}
	b.children[home] = endpoint.URL
	return endpoint.URL, nil
}

func (b *sharedBridge) handleManagement(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.Header.Get("Origin") != "" ||
		subtle.ConstantTimeCompare([]byte(r.Header.Get(managementHeader)), []byte(b.token)) != 1 {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Assistant Bridge lifecycle credentials required"})
		return
	}
	var client ClientOptions
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&client) != nil || client.ID == "" || len(client.ID) > 1024 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid bridge client"})
		return
	}
	root, err := connectorconfig.RootHome(client.Home)
	if err != nil || root != b.root {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "unknown bridge environment"})
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopping {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "bridge is stopping; retry startup"})
		return
	}
	switch r.URL.Path {
	case "/v1/clients/acquire":
		if old, ok := b.clients[client.ID]; ok && old.Home != client.Home {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "client is already bound to another deployment"})
			return
		}
		endpoint, err := b.endpoint(r.Context(), client.Home)
		if err != nil {
			writeError(w, err)
			return
		}
		b.clients[client.ID] = client
		if err := b.saveClients(); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"url": endpoint})
	case "/v1/clients/release":
		if _, ok := b.clients[client.ID]; !ok {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "running": true})
			return
		}
		if existing, ok := b.clients[client.ID]; ok && existing.Home != client.Home {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "bridge client ownership mismatch"})
			return
		}
		releasedClient := b.clients[client.ID]
		delete(b.clients, client.ID)
		// Browser leases belong to the deployment being explicitly stopped, not to other deployments.
		for id, consumer := range b.clients {
			if (strings.HasPrefix(id, "browser:") && consumer.Home == client.Home) || (consumer.OwnerPID > 0 && ownerExited(consumer.OwnerPID)) {
				delete(b.clients, id)
			}
		}
		if err := b.saveClients(); err != nil {
			writeError(w, err)
			return
		}
		if len(b.clients) == 0 {
			if err := b.stopChildren(r.Context()); err != nil {
				b.clients[client.ID] = releasedClient
				_ = b.saveClients()
				writeError(w, err)
				return
			}
			b.stopping = true
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "running": len(b.clients) > 0, "remaining_clients": len(b.clients)})
		if b.stopping && b.server.stop != nil {
			go b.server.stop()
		}
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown lifecycle action"})
	}
}

func (b *sharedBridge) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/clients/") {
			b.handleManagement(w, r)
			return
		}
		if r.URL.Path == "/v1/shutdown" {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "shared bridge requires releasing this deployment's client; other deployments may still be using it"})
			return
		}
		origin := r.Header.Get("Origin")
		if origin == "" || r.Method == http.MethodOptions || r.URL.Path == "/v1/health" {
			next.ServeHTTP(w, r)
			return
		}
		identity, err := connectorconfig.OriginIdentity(origin)
		if err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid LazyMind origin"})
			return
		}
		home, err := b.browserHome(origin, identity)
		if err != nil {
			writeError(w, err)
			return
		}
		b.mu.Lock()
		if b.stopping {
			b.mu.Unlock()
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "bridge is stopping; retry startup"})
			return
		}
		endpoint, err := b.endpoint(r.Context(), home)
		if err == nil {
			b.clients["browser:"+identity] = ClientOptions{ID: "browser:" + identity, Home: home, ServerURL: origin}
			err = b.saveClients()
		}
		b.mu.Unlock()
		if err != nil {
			writeError(w, err)
			return
		}
		if home == b.root {
			next.ServeHTTP(w, r)
			return
		}
		target, _ := url.Parse(endpoint)
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "this environment's Assistant Bridge is unavailable; retry to reconnect"})
		}
		proxy.ServeHTTP(w, r)
	})
}

func (b *sharedBridge) browserHome(origin, identity string) (string, error) {
	// A browser opened on Desktop's own server uses that environment too.
	if _, err := os.Stat(filepath.Join(b.root, "environments.json")); errors.Is(err, os.ErrNotExist) {
		return connectorconfig.EnvironmentHome(b.root, identity)
	} else if err != nil {
		return "", err
	}
	home, err := connectorconfig.EnvironmentHome(b.root, "desktop")
	if err != nil {
		return "", err
	}
	var session struct {
		Server  string `json:"server_url"`
		Runtime string `json:"local_runtime_id"`
	}
	if body, err := os.ReadFile(filepath.Join(home, "credentials.json")); err == nil && json.Unmarshal(body, &session) == nil && session.Runtime != "" && sameOrigin(origin, session.Server) {
		return home, nil
	}
	return connectorconfig.EnvironmentHome(b.root, identity)
}

func (s *Server) validLifecycleRequest(r *http.Request) bool {
	if r.Header.Get("Origin") != "" {
		return false
	}
	root, err := connectorconfig.RootHome(s.store.Directory())
	if err != nil {
		return false
	}
	token, err := bridgeControlToken(root)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(r.Header.Get(managementHeader)), []byte(token)) == 1
}

func (b *sharedBridge) stopChildren(ctx context.Context) error {
	for home, endpoint := range b.children {
		u, err := url.Parse(endpoint)
		if err != nil {
			return err
		}
		status, err := Health(ctx, u.Host)
		if err != nil {
			if listenerAbsent(u.Host) {
				continue
			}
			return errors.New("cannot verify environment bridge; service was preserved, retry when it responds")
		}
		if !compatibleBridge(status, home, true) {
			return errors.New("environment bridge identity changed; service was preserved")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/v1/shutdown", nil)
		if err != nil {
			return err
		}
		req.Header.Set(managementHeader, b.token)
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("environment bridge refused shutdown: HTTP %d", resp.StatusCode)
		}
		deadline := time.Now().Add(5 * time.Second)
		pid, _ := status["pid"].(float64)
		for !listenerAbsent(u.Host) || (pid > 0 && !ownerExited(int(pid))) {
			if time.Now().After(deadline) {
				return errors.New("environment bridge shutdown timed out")
			}
			timer := time.NewTimer(25 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil
}

func listenerAbsent(address string) bool {
	connection, err := net.DialTimeout("tcp", address, 300*time.Millisecond)
	if err == nil {
		_ = connection.Close()
		return false
	}
	return errors.Is(err, syscall.ECONNREFUSED)
}

func (s *Server) originMatchesEnvironment(origin string) bool {
	identity, err := connectorconfig.EnvironmentIdentity(s.store.Directory())
	if err != nil {
		return false
	}
	if identity == "desktop" {
		var session struct {
			Server string `json:"server_url"`
		}
		body, err := os.ReadFile(filepath.Join(s.store.Directory(), "credentials.json"))
		return err == nil && json.Unmarshal(body, &session) == nil && sameOrigin(origin, session.Server)
	}
	expected, err := connectorconfig.OriginIdentity(origin)
	return err == nil && expected == identity
}
