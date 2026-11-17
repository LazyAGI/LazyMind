package assistantbridge

import (
	"strings"

	"lazymind/agentconnector/internal/connectorconfig"
)

func detachedServiceEnvironment(home string) []string {
	var environment []string
	for _, value := range connectorconfig.ServiceEnvironment(home) {
		key, _, _ := strings.Cut(value, "=")
		if !strings.EqualFold(key, "LAZYMIND_CONNECTOR_LAUNCHER_JOB") {
			environment = append(environment, value)
		}
	}
	return environment
}
