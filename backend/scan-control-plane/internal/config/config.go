package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const stateBackendEnv = "LAZYMIND_STATE_BACKEND"

type Config struct {
	Address                           string
	Port                              int
	DBDSN                             string
	DBMigrationFile                   string
	CoreBaseURL                       string
	DefaultDatasetAlgoID              string
	DefaultDatasetAlgoName            string
	AgentBaseURL                      string
	AgentToken                        string
	LocalFSDefaultAgentID             string
	LocalFSPublicRoot                 string
	FeishuBaseURL                     string
	AuthServiceBaseURL                string
	AuthServiceInternalToken          string
	RedisURL                          string
	TempDir                           string
	TempTTL                           time.Duration
	TargetSearchCachePrewarmInterval  time.Duration
	TargetSearchCachePrewarmStagger   time.Duration
	WorkerLeaseTTL                    time.Duration
	WorkerMaxBackoff                  time.Duration
	CrawlListRequestInterval          time.Duration
	ParseDeadLetterAfter              int64
	GenerateTasksMaxObjectsPerRequest int
	ParseWorkerGlobalConcurrency      int
	ParseWorkerSourceConcurrency      int
	WorkerPollInterval                time.Duration
	CoreResultPollInterval            time.Duration
	CompensationPollInterval          time.Duration
}

func Load() (Config, error) {
	cfg := defaultConfig()
	cfg.applyEnv()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func defaultConfig() Config {
	return Config{
		Address:                           "127.0.0.1",
		Port:                              18080,
		DefaultDatasetAlgoID:              "general_algo",
		DefaultDatasetAlgoName:            "General",
		LocalFSDefaultAgentID:             "file-watcher-local-001",
		TempDir:                           filepath.Join(os.TempDir(), "scan-control-plane", "sourceengine"),
		TempTTL:                           24 * time.Hour,
		TargetSearchCachePrewarmInterval:  10 * time.Minute,
		TargetSearchCachePrewarmStagger:   10 * time.Second,
		WorkerLeaseTTL:                    60 * time.Second,
		WorkerMaxBackoff:                  10 * time.Minute,
		CrawlListRequestInterval:          500 * time.Millisecond,
		ParseDeadLetterAfter:              3,
		GenerateTasksMaxObjectsPerRequest: 20,
		ParseWorkerGlobalConcurrency:      20,
		ParseWorkerSourceConcurrency:      2,
		WorkerPollInterval:                5 * time.Second,
		CoreResultPollInterval:            10 * time.Second,
		CompensationPollInterval:          30 * time.Second,
	}
}

func (c *Config) applyEnv() {
	if address := strings.TrimSpace(os.Getenv("LAZYMIND_SCAN_CONTROL_PLANE_ADDRESS")); address != "" {
		c.Address = address
	}
	if portText := strings.TrimSpace(os.Getenv("LAZYMIND_SCAN_CONTROL_PLANE_PORT")); portText != "" {
		port, err := strconv.Atoi(portText)
		if err == nil {
			c.Port = port
		} else {
			c.Port = -1
		}
	}
	if dsn := strings.TrimSpace(os.Getenv("LAZYMIND_SCAN_CONTROL_PLANE_DB_DSN")); dsn != "" {
		c.DBDSN = dsn
	}
	if migrationFile := strings.TrimSpace(os.Getenv("LAZYMIND_SCAN_CONTROL_PLANE_DB_MIGRATION_FILE")); migrationFile != "" {
		c.DBMigrationFile = migrationFile
	}
	if baseURL := strings.TrimSpace(os.Getenv("LAZYMIND_SCAN_CONTROL_PLANE_CORE_BASE_URL")); baseURL != "" {
		c.CoreBaseURL = baseURL
	}
	if algoID := strings.TrimSpace(os.Getenv("LAZYMIND_SCAN_CONTROL_PLANE_DEFAULT_DATASET_ALGO_ID")); algoID != "" {
		c.DefaultDatasetAlgoID = algoID
	}
	if algoName := strings.TrimSpace(os.Getenv("LAZYMIND_SCAN_CONTROL_PLANE_DEFAULT_DATASET_ALGO_NAME")); algoName != "" {
		c.DefaultDatasetAlgoName = algoName
	}
	if baseURL := strings.TrimSpace(os.Getenv("LAZYMIND_SCAN_CONTROL_PLANE_AGENT_BASE_URL")); baseURL != "" {
		c.AgentBaseURL = baseURL
	}
	if token := strings.TrimSpace(os.Getenv("LAZYMIND_SCAN_CONTROL_PLANE_AGENT_TOKEN")); token != "" {
		c.AgentToken = token
	}
	if agentID := strings.TrimSpace(os.Getenv("LAZYMIND_SCAN_CONTROL_PLANE_LOCAL_FS_DEFAULT_AGENT_ID")); agentID != "" {
		c.LocalFSDefaultAgentID = agentID
	}
	if publicRoot := strings.TrimSpace(os.Getenv("LAZYMIND_SCAN_CONTROL_PLANE_LOCAL_FS_PUBLIC_ROOT")); publicRoot != "" {
		c.LocalFSPublicRoot = publicRoot
	}
	if baseURL := strings.TrimSpace(os.Getenv("LAZYMIND_SCAN_CONTROL_PLANE_FEISHU_BASE_URL")); baseURL != "" {
		c.FeishuBaseURL = baseURL
	}
	if baseURL := strings.TrimSpace(os.Getenv("LAZYMIND_SCAN_CONTROL_PLANE_AUTH_SERVICE_BASE_URL")); baseURL != "" {
		c.AuthServiceBaseURL = baseURL
	}
	if token := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN")); token != "" {
		c.AuthServiceInternalToken = token
	}
	if redisURL := strings.TrimSpace(os.Getenv("LAZYMIND_SCAN_CONTROL_PLANE_REDIS_URL")); redisURL != "" {
		c.RedisURL = redisURL
	} else if redisURL := strings.TrimSpace(os.Getenv("LAZYMIND_REDIS_URL")); redisURL != "" {
		c.RedisURL = redisURL
	}
	if tempDir := strings.TrimSpace(os.Getenv("LAZYMIND_SCAN_CONTROL_PLANE_TEMP_DIR")); tempDir != "" {
		c.TempDir = tempDir
	}
	c.TempTTL = durationEnv("SOURCEENGINE_TEMP_TTL", c.TempTTL)
	c.TargetSearchCachePrewarmInterval = durationEnv("SOURCEENGINE_TARGET_SEARCH_CACHE_PREWARM_INTERVAL", c.TargetSearchCachePrewarmInterval)
	c.TargetSearchCachePrewarmStagger = durationEnv("SOURCEENGINE_TARGET_SEARCH_CACHE_PREWARM_STAGGER", c.TargetSearchCachePrewarmStagger)
	c.WorkerLeaseTTL = durationEnv("SOURCEENGINE_WORKER_LEASE_TTL", c.WorkerLeaseTTL)
	c.WorkerMaxBackoff = durationEnv("SOURCEENGINE_WORKER_MAX_BACKOFF", c.WorkerMaxBackoff)
	c.CrawlListRequestInterval = durationEnv("SOURCEENGINE_CRAWL_LIST_REQUEST_INTERVAL", c.CrawlListRequestInterval)
	c.ParseDeadLetterAfter = int64Env("SOURCEENGINE_PARSE_DEAD_LETTER_AFTER", c.ParseDeadLetterAfter)
	c.GenerateTasksMaxObjectsPerRequest = intEnv("SOURCEENGINE_GENERATE_TASKS_MAX_OBJECTS_PER_REQUEST", c.GenerateTasksMaxObjectsPerRequest)
	c.ParseWorkerGlobalConcurrency = intEnv("SOURCEENGINE_PARSE_WORKER_GLOBAL_CONCURRENCY", c.ParseWorkerGlobalConcurrency)
	c.ParseWorkerSourceConcurrency = intEnv("SOURCEENGINE_PARSE_WORKER_SOURCE_CONCURRENCY", c.ParseWorkerSourceConcurrency)
	c.WorkerPollInterval = durationEnv("SOURCEENGINE_WORKER_POLL_INTERVAL", c.WorkerPollInterval)
	c.CoreResultPollInterval = durationEnv("SOURCEENGINE_CORE_RESULT_POLL_INTERVAL", c.CoreResultPollInterval)
	c.CompensationPollInterval = durationEnv("SOURCEENGINE_COMPENSATION_POLL_INTERVAL", c.CompensationPollInterval)
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Address) == "" {
		return fmt.Errorf("address is required")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	if strings.TrimSpace(c.DBDSN) == "" {
		return fmt.Errorf("db dsn is required")
	}
	if strings.TrimSpace(c.CoreBaseURL) == "" {
		return fmt.Errorf("core base url is required")
	}
	if strings.TrimSpace(c.DefaultDatasetAlgoID) == "" {
		return fmt.Errorf("default dataset algo id is required")
	}
	if strings.TrimSpace(c.DefaultDatasetAlgoName) == "" {
		return fmt.Errorf("default dataset algo name is required")
	}
	if strings.TrimSpace(c.TempDir) == "" {
		return fmt.Errorf("temp dir is required")
	}
	if strings.TrimSpace(c.AgentBaseURL) == "" {
		return fmt.Errorf("agent base url is required")
	}
	if strings.TrimSpace(c.FeishuBaseURL) == "" {
		return fmt.Errorf("feishu base url is required")
	}
	if strings.TrimSpace(c.AuthServiceBaseURL) == "" {
		return fmt.Errorf("auth service base url is required")
	}
	if strings.TrimSpace(c.AuthServiceInternalToken) == "" {
		return fmt.Errorf("auth service internal token is required")
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv(stateBackendEnv)), "sqlite") && strings.TrimSpace(c.RedisURL) != "" {
		return fmt.Errorf("redis url must not be configured when LAZYMIND_STATE_BACKEND=sqlite")
	}
	if c.GenerateTasksMaxObjectsPerRequest <= 0 {
		return fmt.Errorf("generate tasks max objects per request must be positive")
	}
	if c.TempTTL <= 0 {
		return fmt.Errorf("temp ttl must be positive")
	}
	if c.TargetSearchCachePrewarmInterval < 0 {
		return fmt.Errorf("target search cache prewarm interval must be positive")
	}
	if c.TargetSearchCachePrewarmStagger < 0 {
		return fmt.Errorf("target search cache prewarm stagger must be positive")
	}
	if c.WorkerLeaseTTL <= 0 {
		return fmt.Errorf("worker lease ttl must be positive")
	}
	if c.WorkerMaxBackoff <= 0 {
		return fmt.Errorf("worker max backoff must be positive")
	}
	if c.CrawlListRequestInterval < 0 {
		return fmt.Errorf("crawl list request interval must be non-negative")
	}
	if c.ParseDeadLetterAfter <= 0 {
		return fmt.Errorf("parse dead letter after must be positive")
	}
	if c.ParseWorkerGlobalConcurrency <= 0 {
		return fmt.Errorf("parse worker global concurrency must be positive")
	}
	if c.ParseWorkerSourceConcurrency <= 0 {
		return fmt.Errorf("parse worker source concurrency must be positive")
	}
	if c.WorkerPollInterval <= 0 {
		return fmt.Errorf("worker poll interval must be positive")
	}
	if c.CoreResultPollInterval <= 0 {
		return fmt.Errorf("core result poll interval must be positive")
	}
	if c.CompensationPollInterval <= 0 {
		return fmt.Errorf("compensation poll interval must be positive")
	}
	return nil
}

func (c Config) ListenAddr() string {
	return net.JoinHostPort(c.Address, strconv.Itoa(c.Port))
}

func intEnv(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return -1
	}
	return parsed
}

func int64Env(name string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return -1
	}
	return parsed
}

func durationEnv(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return -1
	}
	return parsed
}
