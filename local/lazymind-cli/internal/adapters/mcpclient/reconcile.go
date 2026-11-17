package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"lazymind/agentconnector/internal/localfile"
)

// Reconcile relocates existing, identity-verified bindings without enabling an
// integration, installing plugins, restarting an Agent, or changing its pairing.
func (a *Adapter) Reconcile(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if a.kind != DeepSeekHarness {
		return a.reconcileJSON(ctx, configPath(a.kind))
	}
	paths := []string{filepath.Join(dshHome(), "cordis.patch.yml")}
	profiles, err := os.ReadDir(filepath.Join(dshHome(), "profiles"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, profile := range profiles {
		if profile.IsDir() {
			paths = append(paths, filepath.Join(dshHome(), "profiles", profile.Name(), "cordis.patch.yml"))
		}
	}
	var failures []error
	for _, path := range paths {
		if err := a.reconcileDSH(ctx, path); err != nil {
			failures = append(failures, fmt.Errorf("repair DSH connection %s: %w", path, err))
		}
	}
	return errors.Join(failures...)
}

func (a *Adapter) jsonRelocation(servers map[string]json.RawMessage) (before, after stdioMCPDefinition, fields map[string]json.RawMessage, err error) {
	raw, exists := servers[serverName]
	if !exists {
		return
	}
	if err = json.Unmarshal(raw, &before); err != nil {
		return
	}
	if !ownedManagedStdio(before, a.home, a.hostID, a.kind, "") || before.Command == a.self {
		return
	}
	if err = json.Unmarshal(raw, &fields); err != nil {
		return
	}
	after = before
	after.Command = a.self
	return
}

func (a *Adapter) reconcileJSON(ctx context.Context, path string) error {
	_, servers, err := readJSONConfig(path)
	if err != nil {
		return err
	}
	_, _, fields, err := a.jsonRelocation(servers)
	if err != nil || fields == nil {
		return err
	}
	unlock, err := localfile.LockContext(ctx, path+".lazymind-lock")
	if err != nil {
		return err
	}
	defer unlock()
	// Re-read ownership and decisions under the same lock used by connect and
	// disconnect, so an earlier snapshot cannot restore a removed integration.
	root, servers, err := readJSONConfig(path)
	if err != nil {
		return err
	}
	before, after, fields, err := a.jsonRelocation(servers)
	if err != nil || fields == nil {
		return err
	}
	if a.kind == WorkBuddy {
		var cwd string
		if raw := fields["cwd"]; len(raw) != 0 {
			if err := json.Unmarshal(raw, &cwd); err != nil {
				return err
			}
		}
		if err := migrateWorkBuddyApproval(ctx, path, before, after, cwd); err != nil {
			return fmt.Errorf("preserve WorkBuddy approval: %w", err)
		}
	}
	fields["command"], err = json.Marshal(after.Command)
	if err != nil {
		return err
	}
	servers[serverName], err = json.Marshal(fields)
	if err != nil {
		return err
	}
	root["mcpServers"], err = json.Marshal(servers)
	if err != nil {
		return err
	}
	body, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeConfigFile(path, append(body, '\n'))
}

func (a *Adapter) relocateDSH(document *yaml.Node, path string) (bool, error) {
	profile := filepath.Dir(path)
	mcpCount, workflowCount := 0, 0
	for _, row := range yamlSequence(document).Content {
		if dshEntryID(row) == "mcp-lazymind" {
			mcpCount++
		} else if scalarValue(row, "id") == dshWorkflowRow {
			workflowCount++
		} else if hasLazyMindEntry(row) {
			return false, fmt.Errorf("unsupported LazyMind entry in %s", path)
		}
	}
	if mcpCount > 1 || workflowCount > 1 {
		return false, fmt.Errorf("duplicate LazyMind entries in %s", path)
	}
	changed := false
	for _, row := range yamlSequence(document).Content {
		if dshEntryID(row) == "mcp-lazymind" {
			config := mappingValue(mappingValue(row, "insert").Content[0], "config")
			entry := scopedDSHStdio(document, row)
			if scalarValue(config, "transport") == "stdio" && scalarValue(config, "serverName") == serverName &&
				ownedManagedStdio(entry, a.home, a.hostID, DeepSeekHarness, profile) && entry.Command != a.self {
				mappingValue(config, "command").Value = a.self
				changed = true
			}
		} else if scalarValue(row, "id") == dshWorkflowRow {
			config := mappingValue(row, "config")
			if command := mappingValue(config, "command"); command != nil && filepath.IsAbs(command.Value) && command.Value != a.self &&
				ownedConnectionReference(scalarValue(config, "connectorConfig"), a.home, a.hostID, DeepSeekHarness, profile) {
				command.Value = a.self
				changed = true
			}
		}
	}
	return changed, nil
}

func (a *Adapter) reconcileDSH(ctx context.Context, path string) error {
	document, err := readYAMLDocument(path)
	if err != nil {
		return err
	}
	changed, err := a.relocateDSH(document, path)
	if err != nil || !changed {
		return err
	}
	unlock, err := lockDSHPatch(ctx, path)
	if err != nil {
		return err
	}
	defer unlock()
	document, err = readYAMLDocument(path)
	if err != nil {
		return err
	}
	changed, err = a.relocateDSH(document, path)
	if err != nil || !changed {
		return err
	}
	body, err := encodeYAML(document)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeConfigFile(path, body)
}
