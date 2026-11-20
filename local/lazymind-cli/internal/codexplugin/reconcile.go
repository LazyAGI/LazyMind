package codexplugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/connectorlauncher"
	"lazymind/agentconnector/internal/localfile"
)

// Reconcile repairs executable references in registrations already owned by this
// home, host and profile. It never installs a plugin or changes its native switch.
// Cached versions stay in place: an existing Codex process may retain its parsed
// MCP configuration until its next startup.
func Reconcile(ctx context.Context, home, profile, hostID string) error {
	markets, err := registeredMarketplaces(profile)
	if err != nil || len(markets) == 0 {
		return err
	}
	root, marketPath, err := Paths()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(marketPath), 0700); err != nil {
		return err
	}
	unlock, err := localfile.LockContext(ctx, marketPath+".lazymind.lock")
	if err != nil {
		return err
	}
	defer unlock()
	launcher := connectorlauncher.Path(home)
	for _, market := range markets {
		if err := reconcileRoot(root, home, profile, hostID, market, launcher); err != nil {
			return err
		}
		cache := filepath.Join(profile, "plugins", "cache", market, Name)
		versions, err := os.ReadDir(cache)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, version := range versions {
			if version.IsDir() && version.Type()&os.ModeSymlink == 0 {
				if err := reconcileRoot(filepath.Join(cache, version.Name()), home, profile, hostID, market, launcher); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func registeredMarketplaces(profile string) ([]string, error) {
	body, err := os.ReadFile(filepath.Join(profile, "config.toml"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var config struct {
		Plugins map[string]struct {
			Enabled bool `toml:"enabled"`
		} `toml:"plugins"`
	}
	if err := toml.Unmarshal(body, &config); err != nil {
		return nil, err
	}
	var markets []string
	for key := range config.Plugins {
		market, ok := strings.CutPrefix(key, Name+"@")
		if ok && marketplaceName.MatchString(market) {
			markets = append(markets, market)
		}
	}
	sort.Strings(markets)
	return markets, nil
}

func reconcileRoot(root, home, profile, hostID, market, launcher string) error {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	markerPath, mcpPath := filepath.Join(root, ".lazymind-installation.json"), filepath.Join(root, ".mcp.json")
	for _, path := range []string{markerPath, mcpPath} {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
	}
	markerBytes, err := os.ReadFile(markerPath)
	if err != nil {
		return err
	}
	var record Installation
	if json.Unmarshal(markerBytes, &record) != nil || record.Home != home || record.Profile != profile || record.Marketplace != market || !filepath.IsAbs(record.Self) || record.ConnectorID == "" || record.PairingFile != filepath.Join(home, "workflow-hosts", record.ConnectorID+".json") {
		return nil
	}
	mcpBytes, err := os.ReadFile(mcpPath)
	if err != nil {
		return err
	}
	var document, servers, server map[string]json.RawMessage
	var command string
	var args []string
	var env map[string]string
	if json.Unmarshal(mcpBytes, &document) != nil || json.Unmarshal(document["mcpServers"], &servers) != nil || json.Unmarshal(servers["lazymind"], &server) != nil || json.Unmarshal(server["command"], &command) != nil || json.Unmarshal(server["args"], &args) != nil || json.Unmarshal(server["env"], &env) != nil {
		return nil
	}
	if (filepath.Clean(command) != filepath.Clean(record.Self) && filepath.Clean(command) != launcher) || len(args) != 2 || args[0] != "mcp" || args[1] != "codex-workflow" {
		return nil
	}
	connection, err := connectorconfig.Load(env[connectorconfig.EnvironmentKey])
	if err != nil || connection.Home != home || connection.HostID != hostID || connection.Provider != "codex" || connection.Profile != profile || connection.PairingFile != record.PairingFile {
		return nil
	}
	// Keep every unrecognised field and the actual installed resource version.
	// Write command first so an interrupted marker update is safe to retry.
	if command != launcher {
		server["command"], _ = json.Marshal(launcher)
		servers["lazymind"], _ = json.Marshal(server)
		document["mcpServers"], _ = json.Marshal(servers)
		if err := writeJSON(mcpPath, document); err != nil {
			return fmt.Errorf("repair Codex plugin command: %w", err)
		}
	}
	if record.Self != launcher {
		var marker map[string]json.RawMessage
		if err := json.Unmarshal(markerBytes, &marker); err != nil {
			return err
		}
		marker["self"], _ = json.Marshal(launcher)
		if err := writeJSON(markerPath, marker); err != nil {
			return fmt.Errorf("repair Codex plugin installation marker: %w", err)
		}
	}
	return nil
}
