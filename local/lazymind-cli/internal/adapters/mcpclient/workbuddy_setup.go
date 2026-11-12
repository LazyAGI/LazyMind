package mcpclient

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WorkBuddy closes its stdio probe after listing tools. An idle MCP session is
// therefore not a setup failure. Read the user's persisted enable/trust choices
// instead; never create approvals or infer trust from our own managed config.
func workbuddySetup(path string) (trusted, enabled bool, err error) {
	_, servers, err := readJSONConfig(path)
	if err != nil {
		return false, false, err
	}
	var entry struct {
		stdioMCPDefinition
		Disabled bool   `json:"disabled"`
		Cwd      string `json:"cwd"`
	}
	if json.Unmarshal(servers[serverName], &entry) != nil || entry.Command == "" || (entry.Type != "" && entry.Type != "stdio") {
		return false, false, errors.New("invalid WorkBuddy MCP configuration")
	}
	enabled = !entry.Disabled
	// WorkBuddy 5.8.x binds approval to command, sorted args, environment key
	// names and optional cwd. Environment values are deliberately not included.
	args := append([]string(nil), entry.Args...)
	sort.Strings(args)
	keys := make([]string, 0, len(entry.Env))
	for key := range entry.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	material := []string{entry.Command, strings.Join(args, ","), strings.Join(keys, ",")}
	if entry.Cwd != "" {
		material = append(material, entry.Cwd)
	}
	hash := sha256.Sum256([]byte(strings.Join(material, "|")))
	key := hex.EncodeToString(hash[:]) + "::" + serverName
	body, err := os.ReadFile(filepath.Join(filepath.Dir(path), "mcp-approvals.json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, enabled, nil
	}
	if err != nil {
		return false, enabled, err
	}
	var approvals map[string]json.RawMessage
	if err := json.Unmarshal(body, &approvals); err != nil {
		return false, enabled, err
	}
	if approvals == nil {
		return false, enabled, errors.New("invalid WorkBuddy approval store")
	}
	approval, exists := approvals[key]
	if !exists {
		return false, enabled, nil
	}
	var approvedAt *float64
	if err := json.Unmarshal(approval, &approvedAt); err != nil {
		return false, enabled, err
	}
	return approvedAt != nil, enabled, nil
}
