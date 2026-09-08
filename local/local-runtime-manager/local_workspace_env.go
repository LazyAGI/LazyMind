package main

import (
	"os"
	"strings"
)

const localWorkspaceHostTokenEnvVar = "LAZYMIND_LOCAL_WORKSPACE_HOST_TOKEN"

func localWorkspaceHostToken(cfg RuntimeConfig, paths RuntimePaths) string {
	if token := strings.TrimSpace(cfg.OwnerToken); token != "" {
		return token
	}
	body, err := os.ReadFile(paths.RunDirTokenFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}
