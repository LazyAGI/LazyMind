package evalset

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lazymind/core/log"
)

const (
	defaultImportPreviewTTL      = 2 * time.Hour
	defaultImportCleanupInterval = 30 * time.Minute
	defaultImportTaskRetention   = 30 * 24 * time.Hour
	defaultImportMaxFileSize     = int64(20 * 1024 * 1024)
	defaultImportMaxRows         = 50000
	defaultAsyncJobConcurrency   = 2
	defaultAsyncJobPollInterval  = 2 * time.Second
	defaultAsyncJobLockTTL       = 10 * time.Minute
	envImportCleanupInterval     = "EVAL_SET_IMPORT_CLEANUP_INTERVAL"
	legacyEnvImportCleanInterval = "EVAL_SET_IMPORT_CLEAN_INTERVAL"
)

type ImportRuntimeConfig struct {
	TempDir         string
	PreviewTTL      time.Duration
	CleanupInterval time.Duration
	TaskRetention   time.Duration
	MaxFileSize     int64
	MaxRows         int
}

type AsyncJobRuntimeConfig struct {
	Concurrency  int
	PollInterval time.Duration
	LockTTL      time.Duration
}

func LoadImportRuntimeConfigFromEnv() ImportRuntimeConfig {
	return ImportRuntimeConfig{
		TempDir:         envString("EVAL_SET_IMPORT_TEMP_DIR", defaultImportTempDir()),
		PreviewTTL:      envDuration("EVAL_SET_IMPORT_PREVIEW_TTL", defaultImportPreviewTTL),
		CleanupInterval: envDurationWithAliases(defaultImportCleanupInterval, envImportCleanupInterval, legacyEnvImportCleanInterval),
		TaskRetention:   envDuration("EVAL_SET_IMPORT_TASK_RETENTION", defaultImportTaskRetention),
		MaxFileSize:     envBytes("EVAL_SET_IMPORT_MAX_FILE_SIZE", defaultImportMaxFileSize),
		MaxRows:         envInt("EVAL_SET_IMPORT_MAX_ROWS", defaultImportMaxRows),
	}
}

func LoadAsyncJobRuntimeConfigFromEnv() AsyncJobRuntimeConfig {
	return AsyncJobRuntimeConfig{
		Concurrency:  envInt("ASYNC_JOB_CONCURRENCY", defaultAsyncJobConcurrency),
		PollInterval: envDuration("ASYNC_JOB_POLL_INTERVAL", defaultAsyncJobPollInterval),
		LockTTL:      envDuration("ASYNC_JOB_LOCK_TTL", defaultAsyncJobLockTTL),
	}
}

func defaultImportTempDir() string {
	return filepath.Join(os.TempDir(), "lazymind", "eval-set-import")
}

func importTempDir() string {
	return LoadImportRuntimeConfigFromEnv().TempDir
}

func importPreviewTTL() time.Duration {
	return LoadImportRuntimeConfigFromEnv().PreviewTTL
}

func importMaxFileSize() int64 {
	return LoadImportRuntimeConfigFromEnv().MaxFileSize
}

func importMaxRows() int {
	return LoadImportRuntimeConfigFromEnv().MaxRows
}

func envString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envDuration(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		log.Logger.Warn().Str("env", name).Str("value", raw).Dur("default", fallback).Msg("invalid duration env; using default")
		return fallback
	}
	return value
}

func envDurationWithAliases(fallback time.Duration, names ...string) time.Duration {
	for _, name := range names {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return envDuration(name, fallback)
		}
	}
	return fallback
}

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		log.Logger.Warn().Str("env", name).Str("value", raw).Int("default", fallback).Msg("invalid int env; using default")
		return fallback
	}
	return value
}

func envBytes(name string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		log.Logger.Warn().Str("env", name).Str("value", raw).Int64("default", fallback).Msg("invalid bytes env; using default")
		return fallback
	}
	return value
}
