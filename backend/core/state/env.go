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

func MustFromEnv() Store {
	switch StateBackendFromEnv() {
	case StateBackendSQLite:
		return MustSQLiteFromEnv()
	default:
		return MustRedisFromEnv()
	}
}

func IsSQLiteMode() bool {
	return StateBackendFromEnv() == StateBackendSQLite
}
