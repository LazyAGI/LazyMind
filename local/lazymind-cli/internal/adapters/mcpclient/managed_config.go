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
)

type managedConfigState struct {
	configured bool
	owned      bool
	command    string
	arguments  []string
}

type rawMCPFile map[string]json.RawMessage

func readManagedConfig(kind Kind, path, self, home string) (managedConfigState, error) {
	if kind == DeepSeekHarness {
		return readDSHConfig(path, self, home)
	}
	root, servers, err := readJSONConfig(path)
	if err != nil {
		return managedConfigState{}, err
	}
	_ = root
	entry, exists := servers[serverName]
	if !exists {
		return managedConfigState{}, nil
	}
	return managedConfigState{
		configured: true,
		owned:      ownedStdio(entry, self, home),
		command:    entry.Command,
		arguments:  append([]string(nil), entry.Args...),
	}, nil
}

func writeManagedConfig(kind Kind, path, self, home string) error {
	if kind == DeepSeekHarness {
		return writeDSHConfig(path, self, home)
	}
	root, servers, err := readJSONConfig(path)
	if err != nil {
		return err
	}
	servers[serverName] = managedStdio(self, home)
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
	root, servers, err := readJSONConfig(path)
	if err != nil {
		return err
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

func readJSONConfig(path string) (rawMCPFile, map[string]stdioMCPDefinition, error) {
	root := rawMCPFile{}
	servers := map[string]stdioMCPDefinition{}
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
	return root, servers, nil
}

func managedStdio(self, home string) stdioMCPDefinition {
	environment := map[string]string(nil)
	if home != "" {
		environment = map[string]string{"LAZYMIND_HOME": home}
	}
	return stdioMCPDefinition{Type: "stdio", Command: self, Args: []string{"mcp", "proxy"}, Env: environment}
}

func ownedStdio(entry stdioMCPDefinition, self, home string) bool {
	if !agentexec.SameExecutable(entry.Command, self) || len(entry.Args) != 2 || entry.Args[0] != "mcp" || entry.Args[1] != "proxy" {
		return false
	}
	configuredHome := filepath.Clean(strings.TrimSpace(entry.Env["LAZYMIND_HOME"]))
	if configuredHome == "." {
		configuredHome = ""
	}
	return configuredHome == home
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
	if err := replaceConfigFile(temporaryPath, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func readDSHConfig(path, self, home string) (managedConfigState, error) {
	document, err := readYAMLDocument(path)
	if err != nil {
		return managedConfigState{}, err
	}
	entry := findDSHEntry(document)
	if entry == nil {
		return managedConfigState{}, nil
	}
	stdio := decodeDSHStdio(entry)
	return managedConfigState{
		configured: true,
		owned:      ownedStdio(stdio, self, home),
		command:    stdio.Command,
		arguments:  append([]string(nil), stdio.Args...),
	}, nil
}

func writeDSHConfig(path, self, home string) error {
	document, err := readYAMLDocument(path)
	if err != nil {
		return err
	}
	if findDSHEntry(document) != nil {
		return nil
	}
	entry, err := newDSHEntry(self, home)
	if err != nil {
		return err
	}
	sequence := yamlSequence(document)
	sequence.Content = append([]*yaml.Node{entry}, sequence.Content...)
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
	if errors.Is(err, os.ErrNotExist) || len(bytes.TrimSpace(body)) == 0 {
		return document, nil
	}
	if err != nil {
		return nil, err
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

func newDSHEntry(self, home string) (*yaml.Node, error) {
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(dshProfilePatch(self, map[string]string{"LAZYMIND_HOME": home})), &document); err != nil {
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
