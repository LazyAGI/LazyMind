package workbuddyhost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/connectorlauncher"
)

func registered(home string) bool {
	user, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	body, err := os.ReadFile(filepath.Join(user, ".workbuddy", "mcp.json"))
	if err != nil {
		return false
	}
	var config struct {
		Servers map[string]struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal(body, &config) != nil {
		return false
	}
	entry, exists := config.Servers["lazymind"]
	self, err := os.Executable()
	if !exists || err != nil ||
		(!agentexec.SameExecutable(entry.Command, self) && !agentexec.SameExecutable(entry.Command, connectorlauncher.Path(home))) {
		return false
	}
	if reference := entry.Env[connectorconfig.EnvironmentKey]; reference != "" {
		return reference == connectorconfig.Path(home, "workbuddy", "")
	}
	// Keep pre-record registrations usable after relocating their command. The
	// original env keys are retained because WorkBuddy includes them in trust.
	host, err := os.ReadFile(filepath.Join(home, "connector-host-id"))
	hostID := strings.TrimSpace(string(host))
	return err == nil && hostID != "" && entry.Env["LAZYMIND_AGENT_HOST_ID"] == hostID &&
		entry.Env["LAZYMIND_AGENT_PROVIDER"] == "workbuddy" &&
		entry.Env["LAZYMIND_HOME"] != "" && filepath.Clean(entry.Env["LAZYMIND_HOME"]) == filepath.Clean(home) &&
		(entry.Type == "" || entry.Type == "stdio") && len(entry.Args) == 2 && entry.Args[0] == "mcp" && entry.Args[1] == "proxy"
}
