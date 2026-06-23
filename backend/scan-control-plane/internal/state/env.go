package state

import (
	"os"
	"strings"
)

const (
	StateBackendRedis  = "redis"
	StateBackendSQLite = "sqlite"
)

func StateBackendFromEnv() string {
	stateBackend := strings.ToLower(strings.TrimSpace(os.Getenv("LAZYMIND_STATE_BACKEND")))
	if stateBackend == "" {
		return StateBackendRedis
	}
	return stateBackend
}
