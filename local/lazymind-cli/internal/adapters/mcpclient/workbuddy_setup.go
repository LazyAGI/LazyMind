package mcpclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"lazymind/agentconnector/internal/localfile"
)

// WorkBuddy closes its stdio probe after listing tools. An idle MCP session is
// therefore not a setup failure. Read the user's persisted enable/trust choices
// instead. Relocation may preserve an existing approval, but never grants new
// authorization or infers trust from our own managed config.
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
	key := workbuddyApprovalKey(entry.stdioMCPDefinition, entry.Cwd)
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

// WorkBuddy 5.8.x binds approval to command, sorted args, environment key
// names and optional cwd. Environment values are deliberately not included.
func workbuddyApprovalKey(entry stdioMCPDefinition, cwd string) string {
	args := append([]string(nil), entry.Args...)
	sort.Strings(args)
	keys := make([]string, 0, len(entry.Env))
	for key := range entry.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	material := []string{entry.Command, strings.Join(args, ","), strings.Join(keys, ",")}
	if cwd != "" {
		material = append(material, cwd)
	}
	hash := sha256.Sum256([]byte(strings.Join(material, "|")))
	return hex.EncodeToString(hash[:]) + "::" + serverName
}

// Carry only an existing approval for this verified managed invocation across
// its command relocation. The current invocation's missing/revoked approval
// also invalidates a grant left at the destination by an interrupted migration.
// An existing destination rejection always remains authoritative.
func migrateWorkBuddyApproval(ctx context.Context, path string, before, after stdioMCPDefinition, cwd string) error {
	oldKey, newKey := workbuddyApprovalKey(before, cwd), workbuddyApprovalKey(after, cwd)
	if oldKey == newKey {
		return nil
	}
	path = filepath.Join(filepath.Dir(path), "mcp-approvals.json")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	unlock, err := localfile.LockContext(ctx, path+".lazymind-lock")
	if err != nil {
		return err
	}
	defer unlock()
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var approvals map[string]json.RawMessage
	if err := json.Unmarshal(body, &approvals); err != nil {
		return err
	}
	if approvals == nil {
		return errors.New("invalid WorkBuddy approval store")
	}
	approval := approvals[oldKey]
	var approvedAt *float64
	if len(approval) > 0 {
		if err := json.Unmarshal(approval, &approvedAt); err != nil {
			return err
		}
	}
	if approvedAt == nil {
		// The command may still be old after an approval write succeeded but
		// configuration publication failed. Do not revive a revoked old entry
		// from that leftover grant when retrying the relocation.
		destination, exists := approvals[newKey]
		if !exists {
			return nil
		}
		var destinationApproval *float64
		if err := json.Unmarshal(destination, &destinationApproval); err != nil {
			return err
		}
		if destinationApproval == nil {
			return nil
		}
		approvals[newKey] = json.RawMessage("null")
	} else {
		if _, exists := approvals[newKey]; exists {
			return nil
		}
		approvals[newKey] = append(json.RawMessage(nil), approval...)
	}
	body, err = json.MarshalIndent(approvals, "", "  ")
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeConfigFile(path, append(body, '\n'))
}
