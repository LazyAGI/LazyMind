package assistantbridge

import (
	"context"
	"debug/buildinfo"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"lazymind/agentconnector/internal/connectorconfig"
)

// replaceBridge sends lifecycle requests on the connection whose peer we
// verified. A restarted or unrelated listener cannot receive the request.
func replaceBridge(ctx context.Context, address string, status map[string]any, self, root, token string, client ClientOptions) (bool, error) {
	legacy := legacyBridge(status)
	if !legacy && !bridgeMatchesScope(status, root, false) {
		return false, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return false, fmt.Errorf("connect to previous Assistant Bridge: %w", err)
	}
	defer conn.Close()
	var used atomic.Bool
	transport := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		if used.Swap(true) {
			return nil, errors.New("Assistant Bridge connection changed during upgrade")
		}
		return conn, nil
	}}
	httpClient := &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	current, err := bridgeHealth(ctx, httpClient, address)
	version, _ := status["version"].(string)
	if err != nil || version == "" || current["version"] != version {
		return false, errors.New("Assistant Bridge changed during upgrade; retry startup")
	}
	pidNumber, _ := current["pid"].(float64)
	pid := int(pidNumber)
	if pid <= 0 || float64(pid) != pidNumber {
		return false, unverifiedBridge()
	}
	if legacy {
		if !legacyBridge(current) || current["platform"] != runtime.GOOS {
			return false, unverifiedBridge()
		}
		logPath := filepath.Join(root, "logs", "assistant-bridge.log")
		executable, err := verifyBridgePeer(ctx, pid, conn, logPath)
		if err != nil {
			return false, fmt.Errorf("%w: %v", unverifiedBridge(), err)
		}
		if !ownsLegacyBridge(executable, self, client) {
			return false, unverifiedBridge()
		}
		if err := shutdownLegacyBridge(ctx, httpClient, address); err != nil {
			return false, err
		}
	} else {
		// Scoped releases authenticate with this environment's existing token
		// and require its registered broker to agree with the health scope.
		record, err := connectorconfig.ReadBridgeEndpoint(root)
		if err != nil || record.Broker != "http://"+address || !bridgeMatchesScope(current, root, false) {
			return false, unverifiedBridge()
		}
		executable, err := verifyBridgePeer(ctx, pid, conn, "")
		if err != nil {
			return false, fmt.Errorf("%w: %v", unverifiedBridge(), err)
		}
		if !sameBridgePath(filepath.Dir(filepath.Dir(executable)), filepath.Join(root, "bridge", "bin")) {
			return false, unverifiedBridge()
		}
		if err := releaseForUpgrade(ctx, httpClient, address, token, client, root); err != nil {
			return false, err
		}
	}
	_ = conn.Close()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for !listenerAbsent(address) || !ownerExited(pid) {
		select {
		case <-ctx.Done():
			return false, errors.New("previous Assistant Bridge did not exit before the startup deadline")
		case <-ticker.C:
		}
	}
	return true, nil
}

func legacyBridge(status map[string]any) bool {
	return status["ok"] == true && status["service"] == nil && status["home"] == nil &&
		status["isolated"] == nil && (status["version"] == "v8" || status["version"] == "v9")
}

func unverifiedBridge() error {
	return errors.New("an incompatible Assistant Bridge is listening; cannot verify ownership for this environment")
}

func sameBridgePath(a, b string) bool {
	if !filepath.IsAbs(a) || !filepath.IsAbs(b) {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b || (runtime.GOOS == "windows" && strings.EqualFold(a, b)) {
		return true
	}
	aInfo, aErr := os.Stat(a)
	bInfo, bErr := os.Stat(b)
	return aErr == nil && bErr == nil && os.SameFile(aInfo, bInfo)
}

func ownsLegacyBridge(executable, self string, client ClientOptions) bool {
	if os.Getenv("LAZYMIND_RUNTIME_PROFILE") != "desktop" {
		// A pre-lease Compose service has no safe identity across launcher
		// replacements. A manual start can replace its own executable.
		return client.OwnerPID == 0 && client.ServerURL == "" && client.ID == "manual:"+client.Home &&
			sameBridgePath(executable, self)
	}
	if sameBridgePath(executable, self) {
		return true
	}
	// Desktop explicitly supplies its packaged runtime directory. Its
	// executable is distinct from the legacy Compose launcher.
	resources := os.Getenv("LAZYMIND_RUNTIME_RESOURCES_ROOT")
	name := "lazymind"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bundled := filepath.Join(resources, "bin", name)
	if sameBridgePath(executable, bundled) {
		return true
	}
	if runtime.GOOS != "windows" || !sameBridgePath(self, bundled) {
		return false
	}
	// Windows locks the running image, so an updated portable ZIP can run
	// from a new directory. The verified peer must still be LazyMind's CLI in
	// the Desktop layout. The legacy Compose launcher uses a different layout.
	previousRuntime := filepath.Dir(filepath.Dir(executable))
	if !strings.EqualFold(filepath.Base(filepath.Dir(previousRuntime)), "resources") ||
		!strings.EqualFold(filepath.Base(previousRuntime), "runtime") ||
		!strings.EqualFold(executable, filepath.Join(previousRuntime, "bin", name)) {
		return false
	}
	info, err := buildinfo.ReadFile(executable)
	return err == nil && info.Path == "lazymind/agentconnector/cmd/lazymind" && info.Main.Path == "lazymind/agentconnector"
}

func shutdownLegacyBridge(ctx context.Context, client *http.Client, address string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+address+"/v1/shutdown", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("stop previous Assistant Bridge: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("previous Assistant Bridge refused shutdown (HTTP %d)", response.StatusCode)
	}
	return nil
}

func releaseForUpgrade(ctx context.Context, httpClient *http.Client, address, token string, client ClientOptions, root string) error {
	leases, err := readBridgeClients(root)
	if err != nil {
		return fmt.Errorf("read previous Assistant Bridge clients: %w", err)
	}
	shared := errors.New("previous Assistant Bridge is in use by another LazyMind deployment")
	for id, lease := range leases {
		if id == client.ID && lease.Home == client.Home {
			continue
		}
		if lease.OwnerPID > 0 && ownerExited(lease.OwnerPID) {
			continue
		}
		if strings.HasPrefix(id, "browser:") && lease.Home == client.Home && client.ServerURL != "" {
			if origin, err := connectorconfig.OriginIdentity(client.ServerURL); err == nil && id == "browser:"+origin {
				continue
			}
		}
		return shared
	}
	if _, registered := leases[client.ID]; !registered {
		// A broker started before the first client registration needs a lease
		// for its existing authenticated release route to stop it.
		if err := manageBridgeHTTP(ctx, httpClient, address, token, "acquire", client, nil); err != nil {
			return err
		}
	}
	var result struct {
		Running bool `json:"running"`
	}
	if err := manageBridgeHTTP(ctx, httpClient, address, token, "release", client, &result); err != nil {
		return err
	}
	if result.Running {
		return shared
	}
	return nil
}
