package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfigUsesPrivateTempDir(t *testing.T) {
	cfg := defaultConfig()
	want := filepath.Join(os.TempDir(), "scan-control-plane", "sourceengine")
	if cfg.TempDir != want {
		t.Fatalf("default temp dir = %q, want %q", cfg.TempDir, want)
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_DB_DSN", "postgres://scan-control-plane")
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_CORE_BASE_URL", "http://core.test")
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_DEFAULT_DATASET_ALGO_ID", "custom_algo")
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_DEFAULT_DATASET_ALGO_NAME", "Custom Algo")
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_AGENT_BASE_URL", "http://agent.test")
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_LOCAL_FS_DEFAULT_AGENT_ID", "agent-default")
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_LOCAL_FS_PUBLIC_ROOT", "/host/root")
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_FEISHU_BASE_URL", "http://feishu.test")
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_AUTH_SERVICE_BASE_URL", "http://auth.test")
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal-token")
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_TEMP_DIR", "/tmp/scan-control-plane-test")
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_DB_MIGRATION_FILE", "/tmp/scan-control-plane-test/init.up.sql")
	t.Setenv("SOURCEENGINE_TEMP_TTL", "2h")
	t.Setenv("SOURCEENGINE_WORKER_LEASE_TTL", "45s")
	t.Setenv("SOURCEENGINE_WORKER_MAX_BACKOFF", "3m")
	t.Setenv("SOURCEENGINE_PARSE_DEAD_LETTER_AFTER", "4")
	t.Setenv("SOURCEENGINE_GENERATE_TASKS_MAX_OBJECTS_PER_REQUEST", "7")
	t.Setenv("SOURCEENGINE_PARSE_WORKER_GLOBAL_CONCURRENCY", "9")
	t.Setenv("SOURCEENGINE_PARSE_WORKER_SOURCE_CONCURRENCY", "3")
	t.Setenv("SOURCEENGINE_WORKER_POLL_INTERVAL", "6s")
	t.Setenv("SOURCEENGINE_CORE_RESULT_POLL_INTERVAL", "11s")
	t.Setenv("SOURCEENGINE_COMPENSATION_POLL_INTERVAL", "31s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.DBDSN == "" || cfg.CoreBaseURL == "" || cfg.AgentBaseURL == "" || cfg.FeishuBaseURL == "" || cfg.AuthServiceBaseURL == "" || cfg.AuthServiceInternalToken == "" || cfg.TempDir == "" {
		t.Fatalf("config did not read required env: %+v", cfg)
	}
	if cfg.DBMigrationFile != "/tmp/scan-control-plane-test/init.up.sql" {
		t.Fatalf("config did not read db bootstrap env: %+v", cfg)
	}
	if cfg.DefaultDatasetAlgoID != "custom_algo" || cfg.DefaultDatasetAlgoName != "Custom Algo" {
		t.Fatalf("config did not read default dataset algo: %+v", cfg)
	}
	if cfg.LocalFSDefaultAgentID != "agent-default" {
		t.Fatalf("config did not read local_fs default agent id: %+v", cfg)
	}
	if cfg.LocalFSPublicRoot != "/host/root" {
		t.Fatalf("config did not read local_fs public root: %+v", cfg)
	}
	if cfg.GenerateTasksMaxObjectsPerRequest != 7 || cfg.ParseWorkerGlobalConcurrency != 9 || cfg.ParseWorkerSourceConcurrency != 3 {
		t.Fatalf("config did not read limits: %+v", cfg)
	}
	if cfg.TempTTL != 2*time.Hour || cfg.WorkerLeaseTTL != 45*time.Second || cfg.WorkerMaxBackoff != 3*time.Minute || cfg.ParseDeadLetterAfter != 4 {
		t.Fatalf("config did not read worker ttl/backoff/deadletter: %+v", cfg)
	}
	if cfg.WorkerPollInterval != 6*time.Second || cfg.CoreResultPollInterval != 11*time.Second || cfg.CompensationPollInterval != 31*time.Second {
		t.Fatalf("config did not read poll intervals: %+v", cfg)
	}
}

func TestValidateRequiresSQLBoundaries(t *testing.T) {
	base := Config{
		Address:                           "127.0.0.1",
		Port:                              18080,
		DBDSN:                             "postgres://scan-control-plane",
		CoreBaseURL:                       "http://core.test",
		DefaultDatasetAlgoID:              "general_algo",
		DefaultDatasetAlgoName:            "General",
		AgentBaseURL:                      "http://agent.test",
		FeishuBaseURL:                     "http://feishu.test",
		AuthServiceBaseURL:                "http://auth.test",
		AuthServiceInternalToken:          "internal-token",
		TempDir:                           "/tmp/scan-control-plane-test",
		TempTTL:                           24 * time.Hour,
		WorkerLeaseTTL:                    time.Minute,
		WorkerMaxBackoff:                  10 * time.Minute,
		ParseDeadLetterAfter:              3,
		GenerateTasksMaxObjectsPerRequest: 20,
		ParseWorkerGlobalConcurrency:      20,
		ParseWorkerSourceConcurrency:      2,
		WorkerPollInterval:                5 * time.Second,
		CoreResultPollInterval:            10 * time.Second,
		CompensationPollInterval:          30 * time.Second,
	}

	for name, mutate := range map[string]func(*Config){
		"db dsn": func(cfg *Config) {
			cfg.DBDSN = ""
		},
		"core url": func(cfg *Config) {
			cfg.CoreBaseURL = ""
		},
		"default dataset algo id": func(cfg *Config) {
			cfg.DefaultDatasetAlgoID = ""
		},
		"default dataset algo name": func(cfg *Config) {
			cfg.DefaultDatasetAlgoName = ""
		},
		"agent url": func(cfg *Config) {
			cfg.AgentBaseURL = ""
		},
		"temp dir": func(cfg *Config) {
			cfg.TempDir = ""
		},
		"feishu url": func(cfg *Config) {
			cfg.FeishuBaseURL = ""
			cfg.AuthServiceBaseURL = "http://auth.test"
		},
		"auth service url": func(cfg *Config) {
			cfg.FeishuBaseURL = "http://feishu.test"
			cfg.AuthServiceBaseURL = ""
		},
		"auth service internal token": func(cfg *Config) {
			cfg.AuthServiceInternalToken = ""
		},
		"generate tasks max objects": func(cfg *Config) {
			cfg.GenerateTasksMaxObjectsPerRequest = 0
		},
		"temp ttl": func(cfg *Config) {
			cfg.TempTTL = 0
		},
		"worker lease ttl": func(cfg *Config) {
			cfg.WorkerLeaseTTL = 0
		},
		"worker max backoff": func(cfg *Config) {
			cfg.WorkerMaxBackoff = 0
		},
		"parse dead letter after": func(cfg *Config) {
			cfg.ParseDeadLetterAfter = 0
		},
		"parse worker global concurrency": func(cfg *Config) {
			cfg.ParseWorkerGlobalConcurrency = 0
		},
		"parse worker source concurrency": func(cfg *Config) {
			cfg.ParseWorkerSourceConcurrency = 0
		},
		"worker poll interval": func(cfg *Config) {
			cfg.WorkerPollInterval = 0
		},
		"core result poll interval": func(cfg *Config) {
			cfg.CoreResultPollInterval = 0
		},
		"compensation poll interval": func(cfg *Config) {
			cfg.CompensationPollInterval = 0
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := base
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("expected missing %s to fail validation", name)
			}
		})
	}
}
