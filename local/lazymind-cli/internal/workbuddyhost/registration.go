package workbuddyhost

import (
	"encoding/json"
	"os"
	"path/filepath"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/connectorconfig"
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
			Command string            `json:"command"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal(body, &config) != nil {
		return false
	}
	entry, exists := config.Servers["lazymind"]
	self, err := os.Executable()
	return exists && err == nil && agentexec.SameExecutable(entry.Command, self) && entry.Env[connectorconfig.EnvironmentKey] == connectorconfig.Path(home, "workbuddy", "")
}
