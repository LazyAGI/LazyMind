package mcpclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/localfile"
)

type managedConfigState struct {
	configured         bool
	owned              bool
	current            bool
	command            string
	arguments          []string
	reference          string
	workflowConfigured bool
	workflowReference  string
}

type rawMCPFile map[string]json.RawMessage

func readManagedConfig(kind Kind, path, self, home, hostID string) (managedConfigState, error) {
	if kind == DeepSeekHarness {
		return readDSHConfig(path, self, home, hostID)
	}
	root, servers, err := readJSONConfig(path)
	if err != nil {
		return managedConfigState{}, err
	}
	_ = root
	rawEntry, exists := servers[serverName]
	if !exists {
		return managedConfigState{}, nil
	}
	var entry stdioMCPDefinition
	if err := json.Unmarshal(rawEntry, &entry); err != nil {
		return managedConfigState{}, fmt.Errorf("decode %s in %s: %w", serverName, path, err)
	}
	return stateForStdio(entry, self, home, hostID, kind), nil
}

func writeManagedConfig(kind Kind, path, self, home, hostID string, controlled bool) error {
	if kind == DeepSeekHarness {
		return writeDSHConfig(path, self, home, hostID, controlled)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	unlock, err := localfile.Lock(path + ".lazymind-lock")
	if err != nil {
		return err
	}
	defer unlock()
	root, servers, err := readJSONConfig(path)
	if err != nil {
		return err
	}

	if _, err := connectorconfig.Save(connectorconfig.Config{Home: home, Provider: string(kind), HostID: hostID, Controlled: kind == WorkBuddy}); err != nil {
		return err
	}
	entry, err := json.Marshal(managedStdio(self, home, hostID, kind))
	if err != nil {
		return err
	}
	servers[serverName] = entry
	encodedServers, err := json.Marshal(servers)
	if err != nil {
		return err
	}
	root["mcpServers"] = encodedServers
	body, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	return writeConfigFile(path, append(body, '\n'))
}

func removeManagedConfig(kind Kind, path string) error {
	if kind == DeepSeekHarness {
		return removeDSHConfig(path)
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	unlock, err := localfile.Lock(path + ".lazymind-lock")
	if err != nil {
		return err
	}
	defer unlock()
	root, servers, err := readJSONConfig(path)
	if err != nil {
		return err
	}
	if _, exists := servers[serverName]; !exists {
		return nil
	}
	delete(servers, serverName)
	encodedServers, err := json.Marshal(servers)
	if err != nil {
		return err
	}
	root["mcpServers"] = encodedServers
	body, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	return writeConfigFile(path, append(body, '\n'))
}

func readJSONConfig(path string) (rawMCPFile, map[string]json.RawMessage, error) {
	root := rawMCPFile{}
	servers := map[string]json.RawMessage{}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return root, servers, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return root, servers, nil
	}
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if raw := root["mcpServers"]; len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return nil, nil, fmt.Errorf("decode mcpServers in %s: %w", path, err)
		}
	}
	if root == nil {
		root = rawMCPFile{}
	}
	if servers == nil {
		servers = map[string]json.RawMessage{}
	}
	return root, servers, nil
}

func managedStdio(self, home, hostID string, kind Kind) stdioMCPDefinition {
	return stdioMCPDefinition{Type: "stdio", Command: self, Args: []string{"mcp", "proxy"}, Env: connectorconfig.Environment(connectorconfig.Path(home, string(kind), ""))}
}

func stateForStdio(entry stdioMCPDefinition, self, home, hostID string, kind Kind) managedConfigState {
	return stateForProfileStdio(entry, self, home, hostID, kind, "")
}

func stateForProfileStdio(entry stdioMCPDefinition, self, home, hostID string, kind Kind, profile string) managedConfigState {
	owned := ownedManagedStdio(entry, home, hostID, kind, profile)
	current := owned && agentexec.SameExecutable(entry.Command, self)
	if reference := entry.Env[connectorconfig.EnvironmentKey]; reference != "" && current && kind == WorkBuddy {
		c, err := connectorconfig.Load(reference)
		current = err == nil && c.Controlled
	}
	return managedConfigState{
		configured: true, owned: owned,
		current: current,
		command: entry.Command, arguments: append([]string(nil), entry.Args...), reference: entry.Env[connectorconfig.EnvironmentKey],
	}
}

// Identity is independent of an executable's lifetime: an upgrade may have
// removed that executable already. Never infer ownership from its name alone.
func ownedManagedStdio(entry stdioMCPDefinition, home, hostID string, kind Kind, profile string) bool {
	if (entry.Type != "" && entry.Type != "stdio") || !filepath.IsAbs(entry.Command) ||
		len(entry.Args) != 2 || entry.Args[0] != "mcp" || entry.Args[1] != "proxy" {
		return false
	}
	if reference := entry.Env[connectorconfig.EnvironmentKey]; reference != "" {
		return ownedConnectionReference(reference, home, hostID, kind, profile)
	}
	if home == "" || hostID == "" || normalizedManagedHome(entry.Env["LAZYMIND_HOME"]) != filepath.Clean(home) ||
		entry.Env["LAZYMIND_AGENT_HOST_ID"] != hostID || entry.Env["LAZYMIND_AGENT_PROVIDER"] != string(kind) {
		return false
	}
	if profile == "" {
		return true
	}
	// Old DSH entries predate connector records. Their scoped pairing provides
	// the profile identity; a home/host match alone must not claim another profile.
	file := entry.Env["LAZYMIND_WORKFLOW_PAIRING_FILE"]
	if filepath.Dir(file) != filepath.Join(home, "workflow-hosts") {
		return false
	}
	var pair struct {
		Provider    string `json:"provider"`
		Profile     string `json:"profile"`
		ConnectorID string `json:"connector_id"`
	}
	body, err := os.ReadFile(file)
	if err != nil || json.Unmarshal(body, &pair) != nil || pair.Profile != profile {
		return false
	}
	return pair.Provider == string(kind) || (pair.Provider == "" && kind == DeepSeekHarness && strings.HasPrefix(pair.ConnectorID, "dsh-"))
}

func ownedConnectionReference(reference, home, hostID string, kind Kind, profile string) bool {
	c, err := connectorconfig.Load(reference)
	return err == nil && home != "" && hostID != "" && filepath.Clean(c.Home) == filepath.Clean(home) &&
		c.HostID == hostID && c.Provider == string(kind) && c.Profile == profile
}

func normalizedManagedHome(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return filepath.Clean(value)
}

func writeConfigFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		backup := path + ".lazymind-backup"
		if _, backupErr := os.Stat(backup); errors.Is(backupErr, os.ErrNotExist) {
			original, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			if writeErr := os.WriteFile(backup, original, 0o600); writeErr != nil {
				return writeErr
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(body); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := localfile.Replace(temporaryPath, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func readDSHConfig(path, self, home, hostID string) (managedConfigState, error) {
	document, err := readYAMLDocument(path)
	if err != nil {
		return managedConfigState{}, err
	}
	workflowConfigured, workflowReference := false, ""
	for _, row := range yamlSequence(document).Content {
		if scalarValue(row, "id") == dshWorkflowRow && scalarValue(row, "disabled") != "true" {
			workflowConfigured = true
			workflowReference = scalarValue(mappingValue(row, "config"), "connectorConfig")
		}
	}
	entry := findDSHEntry(document)
	if entry == nil {
		return managedConfigState{workflowConfigured: workflowConfigured, workflowReference: workflowReference}, nil
	}
	stdio := scopedDSHStdio(document, entry)
	state := stateForProfileStdio(stdio, self, home, hostID, DeepSeekHarness, filepath.Dir(path))
	state.workflowConfigured, state.workflowReference = workflowConfigured, workflowReference
	if reference := stdio.Env[connectorconfig.EnvironmentKey]; reference != "" {
		c, err := connectorconfig.Load(reference)
		if err != nil || c.Profile != filepath.Dir(path) {
			state.owned, state.current = false, false
		}
	}
	return state, nil
}

func writeDSHConfig(path, self, home, hostID string, controlled bool) error {
	document, err := readYAMLDocument(path)
	if err != nil {
		return err
	}
	profile := filepath.Dir(path)
	c := connectorconfig.Config{Home: home, Provider: string(DeepSeekHarness), HostID: hostID, Profile: profile, Controlled: controlled}
	if previous, err := connectorconfig.Load(connectorconfig.Path(home, c.Provider, profile)); err == nil {
		c.PairingFile = previous.PairingFile
	}
	configPath, err := connectorconfig.Save(c)
	if err != nil {
		return err
	}
	entry, err := newDSHEntry(self, configPath)
	if err != nil {
		return err
	}
	sequence := yamlSequence(document)
	replaced := false
	for index, existing := range sequence.Content {
		if dshEntryID(existing) == "mcp-lazymind" {
			sequence.Content[index] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		sequence.Content = append([]*yaml.Node{entry}, sequence.Content...)
	}
	body, err := encodeYAML(document)
	if err != nil {
		return err
	}
	return writeConfigFile(path, body)
}

func removeDSHConfig(path string) error {
	document, err := readYAMLDocument(path)
	if err != nil {
		return err
	}
	sequence := yamlSequence(document)
	for index, item := range sequence.Content {
		if dshEntryID(item) == "mcp-lazymind" {
			sequence.Content = append(sequence.Content[:index], sequence.Content[index+1:]...)
			body, encodeErr := encodeYAML(document)
			if encodeErr != nil {
				return encodeErr
			}
			return writeConfigFile(path, body)
		}
	}
	return nil
}

func readYAMLDocument(path string) (*yaml.Node, error) {
	document := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.SequenceNode}}}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return document, nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return document, nil
	}
	if err := yaml.Unmarshal(body, document); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.SequenceNode {
		return nil, errors.New("DeepSeek Harness profile patch must be a top-level YAML list")
	}
	return document, nil
}

func yamlSequence(document *yaml.Node) *yaml.Node { return document.Content[0] }

func findDSHEntry(document *yaml.Node) *yaml.Node {
	for _, item := range yamlSequence(document).Content {
		if dshEntryID(item) == "mcp-lazymind" {
			return item
		}
	}
	return nil
}

func dshEntryID(item *yaml.Node) string {
	insert := mappingValue(item, "insert")
	if insert == nil || insert.Kind != yaml.SequenceNode || len(insert.Content) != 1 {
		return ""
	}
	return scalarValue(insert.Content[0], "id")
}

func decodeDSHStdio(item *yaml.Node) stdioMCPDefinition {
	insert := mappingValue(item, "insert")
	if insert == nil || len(insert.Content) != 1 {
		return stdioMCPDefinition{}
	}
	config := mappingValue(insert.Content[0], "config")
	if config == nil {
		return stdioMCPDefinition{}
	}
	result := stdioMCPDefinition{Command: scalarValue(config, "command")}
	if args := mappingValue(config, "args"); args != nil {
		for _, argument := range args.Content {
			result.Args = append(result.Args, argument.Value)
		}
	}
	if environment := mappingValue(config, "env"); environment != nil {
		result.Env = map[string]string{}
		for index := 0; index+1 < len(environment.Content); index += 2 {
			result.Env[environment.Content[index].Value] = environment.Content[index+1].Value
		}
	}
	return result
}

// Older DSH packages kept the pairing reference on the Workflow row rather
// than in MCP's env. Use it only as ownership evidence; never change those keys.
func scopedDSHStdio(document, item *yaml.Node) stdioMCPDefinition {
	entry := decodeDSHStdio(item)
	if entry.Env[connectorconfig.EnvironmentKey] != "" || entry.Env["LAZYMIND_WORKFLOW_PAIRING_FILE"] != "" {
		return entry
	}
	for _, row := range yamlSequence(document).Content {
		if scalarValue(row, "id") != dshWorkflowRow {
			continue
		}
		file := scalarValue(mappingValue(row, "config"), "pairingFile")
		if file != "" {
			if entry.Env == nil {
				entry.Env = map[string]string{}
			}
			entry.Env["LAZYMIND_WORKFLOW_PAIRING_FILE"] = file
		}
		break
	}
	return entry
}

func newDSHEntry(self, configPath string) (*yaml.Node, error) {
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(dshProfilePatch(self, connectorconfig.Environment(configPath))), &document); err != nil {
		return nil, err
	}
	return document.Content[0].Content[0], nil
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}
	return nil
}

func scalarValue(mapping *yaml.Node, key string) string {
	value := mappingValue(mapping, key)
	if value == nil {
		return ""
	}
	return value.Value
}

func encodeYAML(document *yaml.Node) ([]byte, error) {
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
