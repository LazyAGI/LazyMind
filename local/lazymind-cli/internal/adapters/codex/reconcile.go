package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/pelletier/go-toml/v2/unstable"

	"lazymind/agentconnector/internal/codexplugin"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/connectorlauncher"
	"lazymind/agentconnector/internal/localfile"
)

// Reconcile repairs saved executable paths without starting Codex, changing its
// native enable switches, or migrating a legacy MCP registration into a plugin.
func (a *Adapter) Reconcile(ctx context.Context) error {
	if _, err := connectorlauncher.Ensure(ctx, a.home, a.self); err != nil {
		return err
	}
	profile, err := codexHome()
	if err != nil {
		return err
	}
	path := filepath.Join(profile, "config.toml")
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
	bounded, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	unlock, err := localfile.LockContext(bounded, filepath.Join(profile, ".lazymind-integration.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	if err := codexplugin.Reconcile(bounded, a.home, profile, a.hostID); err != nil {
		return err
	}
	config, exists, err := a.getConfig()
	if err != nil || !exists {
		return err
	}
	launcher := connectorlauncher.Path(a.home)
	if config.Transport.Command == launcher || !slices.Equal(config.Transport.Args, []string{"mcp", "proxy"}) || !filepath.IsAbs(config.Transport.Command) {
		return nil
	}
	if !a.ownedLegacyConnection(config, profile) {
		return nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updated, err := replaceLegacyCommand(body, launcher)
	if err != nil {
		return err
	}
	return writeConfig(path, updated, info.Mode().Perm())
}

func (a *Adapter) ownedLegacyConnection(config mcpConfig, profile string) bool {
	env := config.Transport.Env
	if reference := env[connectorconfig.EnvironmentKey]; reference != "" {
		connection, err := connectorconfig.Load(reference)
		return err == nil && connection.Home == a.home && connection.HostID == a.hostID && connection.Provider == "codex" && connection.Profile == profile
	}
	// Before connection records existed, these identity fields were saved in the
	// MCP entry itself. Its containing config.toml identifies the Codex profile.
	return filepath.IsAbs(env["LAZYMIND_HOME"]) && filepath.Clean(env["LAZYMIND_HOME"]) == a.home && env["LAZYMIND_AGENT_HOST_ID"] == a.hostID && env["LAZYMIND_AGENT_PROVIDER"] == "codex" && (env["CODEX_HOME"] == "" || filepath.Clean(env["CODEX_HOME"]) == profile)
}

// Use the parser's source range so comments, unknown keys, formatting and native
// enable/disable settings remain byte-for-byte unchanged outside this value.
func replaceLegacyCommand(body []byte, launcher string) ([]byte, error) {
	var parser unstable.Parser
	parser.Reset(body)
	var table []string
	var found *unstable.Range
	for parser.NextExpression() {
		node := parser.Expression()
		switch node.Kind {
		case unstable.Table, unstable.ArrayTable:
			table = tomlNodeKey(node)
		case unstable.KeyValue:
			if value := legacyCommandValue(node, table); value != nil {
				rangeCopy := value.Raw
				found = &rangeCopy
			}
		}
	}
	if err := parser.Error(); err != nil {
		return nil, err
	}
	if found == nil {
		return nil, errors.New("unable to locate the owned Codex MCP command")
	}
	quoted, _ := json.Marshal(launcher)
	updated := append([]byte{}, body[:found.Offset]...)
	updated = append(updated, quoted...)
	return append(updated, body[found.Offset+found.Length:]...), nil
}

func tomlNodeKey(node *unstable.Node) []string {
	var key []string
	iterator := node.Key()
	for iterator.Next() {
		key = append(key, string(iterator.Node().Data))
	}
	return key
}

func legacyCommandValue(node *unstable.Node, prefix []string) *unstable.Node {
	path := append(slices.Clone(prefix), tomlNodeKey(node)...)
	value := node.Value()
	if slices.Equal(path, []string{"mcp_servers", "lazymind", "command"}) && value.Kind == unstable.String {
		return value
	}
	if value.Kind == unstable.InlineTable {
		iterator := value.Children()
		for iterator.Next() {
			if found := legacyCommandValue(iterator.Node(), path); found != nil {
				return found
			}
		}
	}
	return nil
}

func writeConfig(path string, body []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".lazymind-codex-*.")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := localfile.Replace(file.Name(), path); err != nil {
		return fmt.Errorf("repair Codex MCP executable: %w", err)
	}
	return nil
}
