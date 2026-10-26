// Package connectorconfig owns the bootstrap contract shared by Agent integrations.
// Connection records contain identity and references, never copies of live service
// addresses, executable bindings or account credentials.
package connectorconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"lazymind/agentconnector/internal/localfile"
)

const EnvironmentKey = "LAZYMIND_CONNECTOR_CONFIG"
const DefaultBridgeURL = "http://127.0.0.1:19091"

type Config struct {
	Version     int    `json:"version"`
	Home        string `json:"home"`
	Provider    string `json:"provider"`
	HostID      string `json:"host_id"`
	Profile     string `json:"profile,omitempty"`
	PairingFile string `json:"pairing_file,omitempty"`
	Controlled  bool   `json:"controlled"`
	// Overrides are an explicit manual configuration, never imported from the
	// installing process's inherited environment.
	Overrides map[string]string `json:"overrides,omitempty"`
}

var overrideKeys = map[string]bool{
	"LAZYMIND_WEB_URL": true, "LAZYMIND_MCP_URL": true,
	"LAZYMIND_ASSISTANT_BRIDGE_URL": true, "LAZYMIND_NODE_BIN": true,
	"LAZYMIND_CODEX_BIN": true, "LAZYMIND_CURSOR_AGENT_BIN": true,
	"LAZYMIND_WORKBUDDY_AGENT_BIN": true, "LAZYMIND_DSH_PATH": true,
	"LAZYMIND_DSH_DESKTOP_PATH": true,
}

func Managed() bool {
	return strings.TrimSpace(os.Getenv(EnvironmentKey)) != "" || os.Getenv("LAZYMIND_RUNTIME_PROFILE") == "desktop"
}

func Path(home, provider, profile string) string {
	hash := sha256.Sum256([]byte(provider + "\x00" + filepath.Clean(profile)))
	return filepath.Join(home, "connections", provider+"-"+hex.EncodeToString(hash[:8])+".json")
}

func Load(path string) (Config, error) {
	var c Config
	if !filepath.IsAbs(path) {
		return c, fmt.Errorf("%s must be an absolute connection configuration path", EnvironmentKey)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return c, fmt.Errorf("read connection configuration %s: %w", path, err)
	}
	if err = json.Unmarshal(body, &c); err != nil {
		return c, fmt.Errorf("decode connection configuration %s: %w", path, err)
	}
	if err = c.Validate(); err != nil {
		return c, fmt.Errorf("connection configuration %s: %w", path, err)
	}
	return c, nil
}

func Current() (*Config, error) {
	path := strings.TrimSpace(os.Getenv(EnvironmentKey))
	if path == "" {
		return nil, nil
	}
	c, err := Load(path)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (c Config) Validate() error {
	if c.Version != 1 || !filepath.IsAbs(c.Home) || c.HostID == "" {
		return errors.New("unsupported or incomplete connection record")
	}
	switch c.Provider {
	case "codex", "workbuddy", "deepseek-harness", "cursor", "raccoon", "traework":
	default:
		return errors.New("unsupported connection provider")
	}
	if c.Profile != "" && !filepath.IsAbs(c.Profile) {
		return errors.New("profile must be absolute")
	}
	if c.PairingFile != "" && (!filepath.IsAbs(c.PairingFile) || filepath.Dir(c.PairingFile) != filepath.Join(c.Home, "workflow-hosts")) {
		return errors.New("pairing must belong to this LazyMind home")
	}
	for name, value := range c.Overrides {
		if !overrideKeys[name] {
			return fmt.Errorf("unsupported override %s", name)
		}
		if strings.HasSuffix(name, "_URL") && value != "" {
			if err := ValidateURL(name, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func ValidateURL(name, value string) error {
	u, err := url.Parse(value)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%s must be an absolute HTTP(S) URL without credentials, query or fragment", name)
	}
	if name == "LAZYMIND_ASSISTANT_BRIDGE_URL" && (u.Scheme != "http" || u.Port() == "" || (u.Path != "" && u.Path != "/") || (u.Hostname() != "localhost" && !net.ParseIP(u.Hostname()).IsLoopback())) {
		return fmt.Errorf("%s must be a loopback HTTP URL with a port", name)
	}
	return nil
}

// Value centralizes legacy CLI compatibility. Managed installations never inherit
// runtime overrides; only their explicitly edited connection record may set them.
// Call Current at operation boundaries to surface malformed records as errors.
func Value(name string) string {
	c, err := Current()
	if err != nil {
		return ""
	}
	if c != nil {
		switch name {
		case "LAZYMIND_HOME":
			return c.Home
		case "LAZYMIND_AGENT_PROVIDER":
			return c.Provider
		case "LAZYMIND_AGENT_HOST_ID":
			return c.HostID
		case "LAZYMIND_WORKFLOW_PAIRING_FILE":
			return c.PairingFile
		case "LAZYMIND_WORKFLOW_HOST_CONTROL":
			if c.Controlled {
				return "1"
			}
			return ""
		case "CODEX_HOME":
			if c.Provider == "codex" {
				return c.Profile
			}
		}
		if overrideKeys[name] {
			return strings.TrimSpace(c.Overrides[name])
		}
	}
	if Managed() && (overrideKeys[name] || name == "LAZYMIND_SERVER_URL" || name == "LAZYMIND_RUNTIME_STATE_FILE") {
		return ""
	}
	return strings.TrimSpace(os.Getenv(name))
}

func URL(name, fallback string) (string, error) {
	if _, err := Current(); err != nil {
		return "", err
	}
	value := Value(name)
	if value == "" {
		value = fallback
	}
	if err := ValidateURL(name, value); err != nil {
		return "", err
	}
	return strings.TrimRight(value, "/"), nil
}

func Save(c Config) (string, error) {
	c.Version = 1
	path := Path(c.Home, c.Provider, c.Profile)
	if err := c.Validate(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	unlock, err := localfile.Lock(path + ".lock")
	if err != nil {
		return "", err
	}
	defer unlock()
	previous, err := Load(path)
	if err == nil {
		if previous.Home != c.Home || previous.Provider != c.Provider || previous.Profile != c.Profile {
			return "", errors.New("connection ownership mismatch")
		}
		c.Overrides = previous.Overrides
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := c.Validate(); err != nil {
		return "", err
	}
	body, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".connection-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(append(body, '\n'))
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err = localfile.Replace(file.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}

func Environment(path string) map[string]string { return map[string]string{EnvironmentKey: path} }

// ServiceEnvironment separates a shared desktop service from the calling Agent.
func ServiceEnvironment(home string) []string {
	if !Managed() {
		return os.Environ()
	}
	env := []string{}
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if name == EnvironmentKey || name == "LAZYMIND_HOME" || name == "LAZYMIND_RUNTIME_PROFILE" || strings.HasPrefix(name, "LAZYMIND_AGENT_") || strings.HasPrefix(name, "LAZYMIND_WORKFLOW_") {
			continue
		}
		env = append(env, item)
	}
	return append(env, "LAZYMIND_HOME="+home, "LAZYMIND_RUNTIME_PROFILE=desktop")
}

// ForProvider lets shared services resolve an Agent without changing process env.
func ForProvider(home, provider, profile string) (*Config, error) {
	c, err := Load(Path(home, provider, profile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if c.Home != home || c.Provider != provider || c.Profile != profile {
		return nil, errors.New("connection ownership mismatch")
	}
	return &c, nil
}
func (c Config) URL(name, fallback string) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	value := strings.TrimSpace(c.Overrides[name])
	if value == "" {
		value = fallback
	}
	if err := ValidateURL(name, value); err != nil {
		return "", err
	}
	return strings.TrimRight(value, "/"), nil
}

func BridgeAddress() (string, error) {
	value, err := URL("LAZYMIND_ASSISTANT_BRIDGE_URL", DefaultBridgeURL)
	if err != nil {
		return "", err
	}
	u, _ := url.Parse(value)
	return u.Host, nil
}
