package connectorconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"lazymind/agentconnector/internal/localfile"
)

// Environment homes isolate backend credentials and pairings, not business data.
// The first (including legacy) environment retains its existing directory.
type environmentIndex struct {
	Primary string `json:"primary"`
}

type environmentMarker struct {
	Root     string `json:"root"`
	Identity string `json:"identity"`
}

type BridgeEndpoint struct {
	URL    string `json:"url"`
	Broker string `json:"broker"`
}

func Home() (string, error) {
	if c, err := Current(); err != nil {
		return "", err
	} else if c != nil {
		return c.Home, nil
	}
	home := strings.TrimSpace(os.Getenv("LAZYMIND_HOME"))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(userHome, ".lazymind")
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return "", err
	}
	if os.Getenv("LAZYMIND_RUNTIME_PROFILE") == "desktop" {
		if root, err := RootHome(home); err != nil {
			return "", err
		} else if root != home {
			return home, nil
		}
		return EnvironmentHome(home, "desktop")
	}
	return home, nil
}

func RootHome(home string) (string, error) {
	home, err := filepath.Abs(home)
	if err != nil {
		return "", err
	}
	body, err := os.ReadFile(filepath.Join(home, "environment.json"))
	if errors.Is(err, os.ErrNotExist) {
		return home, nil
	}
	if err != nil {
		return "", err
	}
	var marker environmentMarker
	if json.Unmarshal(body, &marker) != nil || !filepath.IsAbs(marker.Root) ||
		filepath.Clean(home) != filepath.Join(marker.Root, "environments", environmentID(marker.Identity)) {
		return "", errors.New("invalid LazyMind environment identity")
	}
	return marker.Root, nil
}

func OriginIdentity(origin string) (string, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") ||
		u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("LazyMind environment requires an HTTP(S) origin")
	}
	host := strings.ToLower(u.Host)
	if u.Hostname() == "localhost" {
		host = "127.0.0.1"
		if u.Port() != "" {
			host += ":" + u.Port()
		}
	}
	return "origin:" + strings.ToLower(u.Scheme) + "://" + host, nil
}

func EnvironmentIdentity(home string) (string, error) {
	root, err := RootHome(home)
	if err != nil {
		return "", err
	}
	if root == home {
		var index environmentIndex
		body, err := os.ReadFile(filepath.Join(root, "environments.json"))
		if err != nil {
			return "", err
		}
		if err := json.Unmarshal(body, &index); err != nil {
			return "", err
		}
		return index.Primary, nil
	}
	var marker environmentMarker
	body, err := os.ReadFile(filepath.Join(home, "environment.json"))
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(body, &marker); err != nil {
		return "", err
	}
	return marker.Identity, nil
}

func environmentID(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:16])
}

func EnvironmentHome(root, identity string) (string, error) {
	if identity != "desktop" && !strings.HasPrefix(identity, "origin:") {
		return "", errors.New("invalid environment identity")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	unlock, err := localfile.Lock(filepath.Join(root, "environments.lock"))
	if err != nil {
		return "", err
	}
	defer unlock()
	path := filepath.Join(root, "environments.json")
	var index environmentIndex
	body, err := os.ReadFile(path)
	if err == nil {
		if json.Unmarshal(body, &index) != nil || index.Primary == "" {
			return "", errors.New("invalid LazyMind environment registry")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		// Preserve ownership of pre-existing credentials; never move or overwrite them.
		var old struct {
			Server  string `json:"server_url"`
			Runtime string `json:"local_runtime_id"`
		}
		legacy, readErr := os.ReadFile(filepath.Join(root, "credentials.json"))
		if readErr == nil {
			if json.Unmarshal(legacy, &old) != nil {
				return "", errors.New("invalid existing LazyMind credentials")
			}
			if old.Runtime != "" {
				index.Primary = "desktop"
			} else if old.Server != "" {
				index.Primary, err = OriginIdentity(old.Server)
				if err != nil {
					return "", err
				}
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return "", readErr
		}
		if index.Primary == "" {
			index.Primary = identity
		}
		body, _ = json.Marshal(index)
		if err := localfile.Write(path, body, 0600); err != nil {
			return "", err
		}
	} else {
		return "", err
	}
	if index.Primary == identity {
		return root, nil
	}
	home := filepath.Join(root, "environments", environmentID(identity))
	marker, _ := json.Marshal(environmentMarker{Root: root, Identity: identity})
	if err := localfile.Write(filepath.Join(home, "environment.json"), marker, 0600); err != nil {
		return "", err
	}
	return home, nil
}

func ReadBridgeEndpoint(home string) (BridgeEndpoint, error) {
	var endpoint BridgeEndpoint
	body, err := os.ReadFile(filepath.Join(home, "bridge-endpoint.json"))
	if err != nil {
		return endpoint, err
	}
	if err := json.Unmarshal(body, &endpoint); err != nil {
		return endpoint, err
	}
	if err := ValidateURL("LAZYMIND_ASSISTANT_BRIDGE_URL", endpoint.URL); err != nil {
		return endpoint, err
	}
	if err := ValidateURL("LAZYMIND_ASSISTANT_BRIDGE_URL", endpoint.Broker); err != nil {
		return endpoint, err
	}
	return endpoint, nil
}

func WriteBridgeEndpoint(home string, endpoint BridgeEndpoint) error {
	for _, value := range []string{endpoint.URL, endpoint.Broker} {
		if err := ValidateURL("LAZYMIND_ASSISTANT_BRIDGE_URL", value); err != nil {
			return err
		}
	}
	body, err := json.Marshal(endpoint)
	if err != nil {
		return err
	}
	return localfile.Write(filepath.Join(home, "bridge-endpoint.json"), body, 0600)
}

func bridgeFallback(home, fallback string) (string, error) {
	endpoint, err := ReadBridgeEndpoint(home)
	if errors.Is(err, os.ErrNotExist) {
		return fallback, nil
	}
	if err != nil {
		return "", fmt.Errorf("read Assistant Bridge endpoint: %w", err)
	}
	return endpoint.URL, nil
}
