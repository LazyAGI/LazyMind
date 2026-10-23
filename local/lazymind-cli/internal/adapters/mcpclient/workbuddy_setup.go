package mcpclient

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WorkBuddy closes its stdio probe after listing tools. An idle MCP session is
// therefore not a setup failure. Read the user's persisted enable/trust choices
// instead; never create approvals or infer trust from our own managed config.
func workbuddySetup(path string) (trusted, enabled bool) {
	_, servers, err := readJSONConfig(path)
	if err != nil {
		return false, false
	}
	var entry struct {
		stdioMCPDefinition
		Disabled bool   `json:"disabled"`
		Cwd      string `json:"cwd"`
	}
	if json.Unmarshal(servers[serverName], &entry) != nil || entry.Command == "" || (entry.Type != "" && entry.Type != "stdio") {
		return false, false
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
	var approvals map[string]json.RawMessage
	if err != nil || json.Unmarshal(body, &approvals) != nil {
		return false, enabled
	}
	var approvedAt *float64
	if json.Unmarshal(approvals[key], &approvedAt) != nil || approvedAt == nil {
		return false, enabled
	}
	return true, enabled
}
