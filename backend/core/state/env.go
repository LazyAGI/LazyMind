package state

import (
	"os"
	"strings"
)

const (
	BackendRedis  = "redis"
	BackendSQLite = "sqlite"
)

func BackendFromEnv() string {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("LAZYMIND_STATE_BACKEND")))
	if backend == "" {
		return BackendRedis
	}
	return backend
}

func MustFromEnv() Store {
	switch BackendFromEnv() {
	case BackendSQLite:
		return MustSQLiteFromEnv()
	default:
		return MustRedisFromEnv()
	}
}

func IsSQLiteMode() bool {
	return BackendFromEnv() == BackendSQLite
}
