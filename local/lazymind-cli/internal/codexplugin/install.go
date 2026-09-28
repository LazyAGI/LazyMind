// Package codexplugin installs and runs the desktop Workflow plugin.
package codexplugin

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/localfile"
	"lazymind/agentconnector/internal/workflowhost"
)

const Name = "lazymind-workflow"

//go:embed all:assets/lazymind-workflow assets/dispatcher.mjs
var assets embed.FS

var marketplaceName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type Installation struct {
	BuildID     string `json:"build_id"`
	Home        string `json:"home"`
	Self        string `json:"self"`
	Profile     string `json:"profile"`
	Binary      string `json:"binary"`
	Node        string `json:"node"`
	ConnectorID string `json:"connector_id"`
	PairingFile string `json:"pairing_file"`
	Marketplace string `json:"marketplace"`
	Version     string `json:"version"`
}

func Paths() (root, marketplace string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(home, "plugins", Name), filepath.Join(home, ".agents", "plugins", "marketplace.json"), nil
}

func Read() (Installation, error) {
	root, _, err := Paths()
	if err != nil {
		return Installation{}, err
	}
	var record Installation
	body, err := os.ReadFile(filepath.Join(root, ".lazymind-installation.json"))
	if err != nil {
		return record, err
	}
	if err := json.Unmarshal(body, &record); err != nil {
		return record, err
	}
	if record.Home == "" || record.Profile == "" || !marketplaceName.MatchString(record.Marketplace) {
		return record, errors.New("invalid LazyMind plugin installation record")
	}
	return record, nil
}

func Enabled(profile, marketplace string) (bool, error) {
	body, err := os.ReadFile(filepath.Join(profile, "config.toml"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var config struct {
		Plugins map[string]struct {
			Enabled bool `toml:"enabled"`
		} `toml:"plugins"`
	}
	if err := toml.Unmarshal(body, &config); err != nil {
		return false, err
	}
	return config.Plugins[Name+"@"+marketplace].Enabled, nil
}

func FindNode(ctx context.Context) (string, error) {
	node, err := agentexec.FindExecutable(os.Getenv("LAZYMIND_NODE_BIN"), []string{"node"})
	if err != nil {
		return "", errors.New("Codex Workflow needs Node.js 22.19 or later")
	}
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	version, err := agentexec.Run(probe, node, "--version")
	if err != nil {
		return "", err
	}
	var major, minor int
	if _, err := fmt.Sscanf(strings.TrimSpace(version), "v%d.%d", &major, &minor); err != nil || major < 22 || major == 22 && minor < 19 {
		return "", errors.New("Codex Workflow needs Node.js 22.19 or later")
	}
	return node, nil
}

// Install uses the personal marketplace. The marker prevents replacing a
// different plugin; atomic writes preserve other personal marketplace entries.
func Install(ctx context.Context, home, self, binary, profile, hostID string) (Installation, error) {
	root, marketPath, err := Paths()
	if err != nil {
		return Installation{}, err
	}
	if err := os.MkdirAll(filepath.Dir(marketPath), 0700); err != nil {
		return Installation{}, err
	}
	unlock, err := localfile.Lock(marketPath + ".lazymind.lock")
	if err != nil {
		return Installation{}, err
	}
	defer unlock()
	if _, err := os.Stat(root); err == nil {
		previous, err := Read()
		if err != nil || previous.Home != home || previous.Profile != profile {
			return Installation{}, errors.New("a different plugin already owns ~/plugins/lazymind-workflow")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Installation{}, err
	}
	market, err := readMarketplace(marketPath)
	if err != nil {
		return Installation{}, err
	}
	node, err := FindNode(ctx)
	if err != nil {
		return Installation{}, err
	}
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	_, err = agentexec.Run(probe, binary, "queue", "--help")
	cancel()
	if err != nil {
		return Installation{}, fmt.Errorf("desktop Codex does not expose queue: %w", err)
	}
	store, err := credentials.NewStore(home, "")
	if err != nil {
		return Installation{}, err
	}
	account, err := store.AccountScope()
	if err != nil {
		return Installation{}, err
	}
	pair, err := workflowhost.EnsureForProvider(home, "codex", profile, account)
	if err != nil {
		return Installation{}, err
	}
	record := Installation{BuildID: BuildID(), Home: home, Self: self, Profile: profile, Binary: binary, Node: node,
		Marketplace: market["name"].(string), ConnectorID: pair.ConnectorID,
		PairingFile: filepath.Join(home, "workflow-hosts", pair.ConnectorID+".json")}
	env := map[string]string{"LAZYMIND_HOME": home, "LAZYMIND_AGENT_PROVIDER": "codex", "LAZYMIND_AGENT_HOST_ID": hostID,
		"LAZYMIND_WORKFLOW_HOST_CONTROL": "1", "LAZYMIND_WORKFLOW_PAIRING_FILE": record.PairingFile,
		"LAZYMIND_CODEX_BIN": binary, "LAZYMIND_NODE_BIN": node, "CODEX_HOME": profile}
	if web := os.Getenv("LAZYMIND_WEB_URL"); web != "" {
		env["LAZYMIND_WEB_URL"] = web
	}
	mcp := map[string]any{"mcpServers": map[string]any{"lazymind": map[string]any{
		"command": self, "args": []string{"mcp", "codex-workflow"}, "env": env}}}
	mcpBytes, _ := json.MarshalIndent(mcp, "", "  ")

	manifestBytes, _ := assets.ReadFile("assets/lazymind-workflow/.codex-plugin/plugin.json")
	sum := sha256.Sum256(append([]byte(record.BuildID), mcpBytes...))
	record.Version = "0.1.0+codex." + hex.EncodeToString(sum[:8])
	// Preserve ownership for retries, including a failed first installation,
	// without advertising the new build as successfully installed.
	pending := record
	pending.BuildID = ""
	if err := writeJSON(filepath.Join(root, ".lazymind-installation.json"), pending); err != nil {
		return Installation{}, err
	}
	if err := fs.WalkDir(assets, "assets/lazymind-workflow", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := assets.ReadFile(path)
		if err != nil {
			return err
		}
		relative := strings.TrimPrefix(path, "assets/lazymind-workflow/")
		return writeAtomic(filepath.Join(root, filepath.FromSlash(relative)), data)
	}); err != nil {
		return Installation{}, err
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return Installation{}, err
	}
	manifest["version"] = record.Version
	if err := writeJSON(filepath.Join(root, ".codex-plugin", "plugin.json"), manifest); err != nil {
		return Installation{}, err
	}
	if err := writeAtomic(filepath.Join(root, ".mcp.json"), mcpBytes); err != nil {
		return Installation{}, err
	}
	if err := writeJSON(marketPath, market); err != nil {
		return Installation{}, err
	}
	install, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if _, err := agentexec.Run(install, binary, "plugin", "add", Name+"@"+record.Marketplace, "--json"); err != nil {
		return Installation{}, fmt.Errorf("install Codex plugin: %w", err)
	}
	if err := writeJSON(filepath.Join(root, ".lazymind-installation.json"), record); err != nil {
		return Installation{}, err
	}
	return record, nil
}

func readMarketplace(path string) (map[string]any, error) {
	market := map[string]any{"name": "personal", "interface": map[string]any{"displayName": "Personal"}, "plugins": []any{}}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &market); err != nil {
			return nil, errors.New("invalid personal marketplace JSON")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	name, _ := market["name"].(string)
	if !marketplaceName.MatchString(name) {
		return nil, errors.New("invalid personal marketplace name")
	}
	entries, ok := market["plugins"].([]any)
	if !ok {
		return nil, errors.New("invalid personal marketplace plugins")
	}
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("invalid personal marketplace entry")
		}
		if entry["name"] == Name {
			source, _ := entry["source"].(map[string]any)
			if source["source"] != "local" || source["path"] != "./plugins/"+Name {
				return nil, errors.New("personal marketplace has a conflicting LazyMind plugin")
			}
			return market, nil
		}
	}
	market["plugins"] = append(entries, map[string]any{"name": Name, "source": map[string]any{"source": "local", "path": "./plugins/" + Name},
		"policy": map[string]any{"installation": "AVAILABLE", "authentication": "ON_INSTALL"}, "category": "Productivity"})
	return market, nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(data, '\n'))
}
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".lazymind-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return localfile.Replace(file.Name(), path)
}

// Includes skills and configuration templates so reconnect also refreshes
// cached plugin content when only instructions change.
func BuildID() string {
	hash := sha256.New()
	_ = fs.WalkDir(assets, "assets", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := assets.ReadFile(path)
		if err != nil {
			return err
		}
		hash.Write([]byte(path))
		hash.Write([]byte{0})
		hash.Write(data)
		return nil
	})
	return hex.EncodeToString(hash.Sum(nil))
}
