package workbuddyhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/localfile"
	"lazymind/agentconnector/internal/workflowhost"
)

type Config struct {
	ConnectionFile string `json:"connection_file,omitempty"`
	PairingFile    string `json:"pairing_file"`
	Node           string `json:"node"`
	CDPPort        int    `json:"cdp_port"`
}

func ConfigPath(home string) string { return filepath.Join(home, "workbuddy-workflow", "config.json") }
func Load(home, account string) (Config, workflowhost.Pairing, error) {
	var config Config
	var pair workflowhost.Pairing
	body, err := os.ReadFile(ConfigPath(home))
	if err != nil {
		return config, pair, err
	}
	if json.Unmarshal(body, &config) != nil || !filepath.IsAbs(config.Node) || !filepath.IsAbs(config.PairingFile) || config.CDPPort < 1 || config.CDPPort > 65535 {
		return config, pair, errors.New("invalid WorkBuddy connector config")
	}
	body, err = os.ReadFile(config.PairingFile)
	if err != nil {
		return config, pair, err
	}
	if json.Unmarshal(body, &pair) != nil || pair.Provider != "workbuddy" {
		return config, pair, errors.New("invalid WorkBuddy pairing")
	}
	pair, err = workflowhost.Verify(home, pair.ConnectorID, pair.Token, account)
	return config, pair, err
}
func Install(ctx context.Context, home, profile, account string) error {
	node, err := findNode(ctx, profile)
	if err != nil {
		return err
	}
	pair, err := workflowhost.EnsureForProvider(home, "workbuddy", profile, account)
	if err != nil {
		return err
	}
	port := 9333
	// Preserve explicitly configured endpoint across reconnects.
	if existing, _, err := Load(home, account); err == nil {
		port = existing.CDPPort
	}
	config := Config{ConnectionFile: connectorconfig.Path(home, "workbuddy", ""), PairingFile: filepath.Join(home, "workflow-hosts", pair.ConnectorID+".json"), Node: node, CDPPort: port}
	body, _ := json.Marshal(config)
	if err := retireLegacyPlugins(profile); err != nil {
		return err
	}
	if err := writeAtomic(ConfigPath(home), body); err != nil {
		return err
	}
	return configureDesktopStartup(ctx, home, port, true)
}

// The Connector now owns presentation; the old plugins also install resume
// hooks that must not run alongside CDP control. Keep their files for recovery.
func retireLegacyPlugins(profile string) error {
	path := filepath.Join(profile, "settings.json")
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(body, &settings); err != nil {
		return fmt.Errorf("read WorkBuddy settings: %w", err)
	}
	if settings["enabledPlugins"] == nil {
		return nil
	}
	var plugins map[string]json.RawMessage
	if err := json.Unmarshal(settings["enabledPlugins"], &plugins); err != nil {
		return fmt.Errorf("read WorkBuddy enabled plugins: %w", err)
	}
	changed := false
	for _, key := range []string{"lazymind-workflow-panel@lazymind-workflow-panel-local", "lazymind-workflow-resume@lazymind-workflow-resume-local"} {
		value, exists := plugins[key]
		if !exists {
			continue
		}
		var enabled bool
		if err := json.Unmarshal(value, &enabled); err != nil {
			return fmt.Errorf("read legacy LazyMind plugin setting: %w", err)
		}
		if enabled {
			plugins[key] = json.RawMessage("false")
			changed = true
		}
	}
	if !changed {
		return nil
	}
	settings["enabledPlugins"], _ = json.Marshal(plugins)
	updated, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	backup, err := os.CreateTemp(profile, "settings.json.before-lazymind-cdp-*")
	if err != nil {
		return err
	}
	if _, err := backup.Write(body); err != nil {
		backup.Close()
		return err
	}
	if err := backup.Close(); err != nil {
		return err
	}
	return writeAtomic(path, append(updated, '\n'))
}
func Disable(home, account string) error {
	config, pair, err := Load(home, account)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		// Repeated disconnect is harmless, but never treat another account's
		// record as this account's already-disabled installation.
		var config Config
		var previous workflowhost.Pairing
		body, readErr := os.ReadFile(ConfigPath(home))
		if readErr == nil && json.Unmarshal(body, &config) == nil {
			body, readErr = os.ReadFile(config.PairingFile)
			if readErr == nil && json.Unmarshal(body, &previous) == nil && previous.Provider == "workbuddy" &&
				previous.AccountScope == account && !previous.Enabled {
				if err := configureDesktopStartup(context.Background(), home, 0, false); err != nil {
					return err
				}
				return removePresentation(home, config)
			}
		}
		return err
	}
	if err := workflowhost.Disable(home, pair.ConnectorID); err != nil {
		return err
	}
	if err := configureDesktopStartup(context.Background(), home, 0, false); err != nil {
		return err
	}
	return removePresentation(home, config)
}
func writeAtomic(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".workbuddy-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return localfile.Replace(f.Name(), path)
}

// Reuse WorkBuddy's managed runtime, so a desktop user does not need to
// install Node or configure the GUI process's PATH.
func findNode(ctx context.Context, profile string, preferred ...string) (string, error) {
	candidates := append([]string{}, preferred...)
	if override := connectorconfig.Value("LAZYMIND_NODE_BIN"); override != "" {
		node, err := agentexec.CompatibleNode(ctx, override)
		if err != nil {
			return "", fmt.Errorf("LAZYMIND_NODE_BIN: %w", err)
		}
		return node, nil
	}
	versions := filepath.Join(profile, "binaries", "node", "versions")
	names := []string{}
	if current, err := os.ReadFile(filepath.Join(versions, "current")); err == nil {
		names = append(names, strings.TrimSpace(string(current)))
	}
	entries, _ := os.ReadDir(versions)
	others := []string{}
	for _, entry := range entries {
		if entry.IsDir() {
			others = append(others, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(others)))
	names = append(names, others...)
	for _, name := range names {
		if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.Contains(name, "installing") || strings.Contains(name, "extract_temp") {
			continue
		}
		relative := filepath.Join("bin", "node")
		if runtime.GOOS == "windows" {
			relative = "node.exe"
		}
		candidates = append(candidates, filepath.Join(versions, name, relative))
	}
	if node, err := agentexec.FindExecutable("", []string{"node"}); err == nil {
		candidates = append(candidates, node)
	}
	return agentexec.CompatibleNode(ctx, candidates...)
}
