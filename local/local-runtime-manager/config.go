package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultProfileEnvVar          = "LAZYMIND_LOCAL_PROFILE"
	localPortsPinnedEnvVar        = "LAZYMIND_LOCAL_PORTS_PINNED"
	processComposePortEnvVar      = "LAZYMIND_PROCESS_COMPOSE_PORT"
	localUpTimeoutEnvVar          = "LAZYMIND_LOCAL_UP_TIMEOUT"
	localDownTimeoutEnvVar        = "LAZYMIND_LOCAL_DOWN_TIMEOUT"
	localProxyAddressEnvVar       = "LAZYMIND_LOCAL_PROXY_ADDRESS"
	localProxyPortEnvVar          = "LAZYMIND_LOCAL_PROXY_PORT"
	localProxyAuthHostPortEnvVar  = "LAZYMIND_LOCAL_PROXY_AUTH_HOST_PORT"
	localProxyCoreHostPortEnvVar  = "LAZYMIND_LOCAL_PROXY_CORE_HOST_PORT"
	localProxyChatHostPortEnvVar  = "LAZYMIND_LOCAL_PROXY_CHAT_HOST_PORT"
	localProxyScanHostPortEnvVar  = "LAZYMIND_LOCAL_PROXY_SCAN_HOST_PORT"
	localProxyEvoHostPortEnvVar   = "LAZYMIND_LOCAL_PROXY_EVO_HOST_PORT"
	localPostgresPortEnvVar       = "LAZYMIND_LOCAL_POSTGRES_PORT"
	localCorePortEnvVar           = "LAZYMIND_LOCAL_CORE_PORT"
	localDocPortEnvVar            = "LAZYMIND_LOCAL_DOC_PORT"
	localProcessorPortEnvVar      = "LAZYMIND_LOCAL_PROCESSOR_PORT"
	localAlgoPortEnvVar           = "LAZYMIND_LOCAL_ALGO_PORT"
	localWorkerPortEnvVar         = "LAZYMIND_LOCAL_WORKER_PORT"
	localChatPortEnvVar           = "LAZYMIND_LOCAL_CHAT_PORT"
	localEvoPortEnvVar            = "LAZYMIND_LOCAL_EVO_PORT"
	localMilvusPortEnvVar         = "LAZYMIND_LOCAL_MILVUS_PORT"
	localOpenSearchPortEnvVar     = "LAZYMIND_LOCAL_OPENSEARCH_PORT"
	localEnableEvoEnvVar          = "LAZYMIND_LOCAL_ENABLE_EVO"
	frontendPortEnvVar            = "LAZYMIND_FRONTEND_PORT"
	authServicePortEnvVar         = "LAZYMIND_AUTH_SERVICE_PORT"
	authServicePythonEnvVar       = "LAZYMIND_AUTH_SERVICE_PYTHON"
	authServiceUVEnvVar           = "LAZYMIND_AUTH_SERVICE_UV"
	authServiceDatabaseURLEnvVar  = "LAZYMIND_AUTH_SERVICE_DATABASE_URL"
	authServiceInstallDepsEnvVar  = "LAZYMIND_AUTH_SERVICE_INSTALL_DEPS"
	caddyBinEnvVar                = "LAZYMIND_CADDY_BIN"
	caddyVersionEnvVar            = "LAZYMIND_CADDY_VERSION"
	defaultProfile                = "linux-browser"
	processComposeVersion         = 2
	defaultCaddyVersion           = "2.10.2"
	defaultProcessComposePort     = 19080
	defaultLocalUpTimeout         = 30 * 60
	defaultLocalDownTimeout       = 2 * 60
	defaultFrontendPort           = 8090
	defaultLocalProxyAddress      = "0.0.0.0"
	defaultLocalProxyPort         = 5024
	defaultLocalProxyAuthHostPort = 18000
	defaultLocalProxyCoreHostPort = 18001
	defaultLocalProxyChatHostPort = 18046
	defaultLocalProxyScanHostPort = 18080
	defaultLocalProxyEvoHostPort  = 18047
	defaultLocalPostgresPort      = 15432
	defaultLocalDocPort           = 18002
	defaultLocalProcessorPort     = 18003
	defaultLocalAlgoPort          = 18004
	defaultLocalWorkerPort        = 18005
	defaultLocalMilvusPort        = 19530
	defaultLocalOpenSearchPort    = 19200
	stateFileName                 = "runtime-state.json"
	composeGeneratedFileName      = "process-compose.generated.yaml"
	serviceEndpointsJSONName      = "service-endpoints.json"
	serviceEndpointsEnvName       = "service-endpoints.env"
	tokenFileName                 = "pc-token"
	upLockFileName                = "up.lock"
	logFileName                   = "docker-stack.log"
	localProxyLogFileName         = "local-proxy.log"
	authServiceLogFileName        = "auth-service.log"
	coreLogFileName               = "core.log"
	frontendLogFileName           = "frontend.log"
	repoComposeFileName           = "docker-compose.yml"
	localComposeOverrideName      = "local/docker-compose.local.yml"
	localProcessComposeBin        = "local/bin/process-compose"
	localProxyConfigName          = "local/local-proxy/configs/cloud-replace-kong.yaml"
	localProxyScriptDirName       = "local/local-proxy/scripts"
	localProxySourceDirName       = "local/local-proxy"
	authServiceSourceDirName      = "backend/auth-service"
	coreSourceDirName             = "backend/core"
	processComposeServiceName     = "docker-stack"
	localProxyProcessName         = "local-proxy"
	authServiceProcessName        = "auth-service"
	coreProcessName               = "core"
	frontendProcessName           = "frontend"
	docServerProcessName          = "lazyllm-doc-server"
	processorServerProcessName    = "lazyllm-parse-server"
	processorWorkerProcessName    = "lazyllm-parse-worker"
	algoProcessName               = "lazyllm-algo"
	chatProcessName               = "chat"
	evoProcessName                = "evo-api"
)

type RuntimePaths struct {
	RepoRoot             string
	RuntimeRoot          string
	StateDir             string
	LogsDir              string
	RunDir               string
	GeneratedDir         string
	BinDir               string
	StateFile            string
	RunDirTokenFile      string
	UpLockFile           string
	LogFilePath          string
	LocalProxyLog        string
	AuthServiceLog       string
	AuthServicePIDFile   string
	AuthServiceVenvDir   string
	AuthServiceStateDir  string
	CoreLog              string
	CorePIDFile          string
	CoreBin              string
	CoreStateDir         string
	FrontendLog          string
	DocServerLog         string
	ProcessorServerLog   string
	ProcessorWorkerLog   string
	AlgoLog              string
	ChatLog              string
	EvoLog               string
	LocalProxyBin        string
	CaddyBin             string
	LocalProxyConfig     string
	LocalProxyStopScript string
	CaddyConfig          string
	GeneratedConfig      string
	ServiceEndpointsJSON string
	ServiceEndpointsEnv  string
	AlgorithmVenv        string
	AlgorithmPython      string
	AlgorithmHome        string
	AlgorithmPIDDir      string
}

type RuntimeConfig struct {
	Profile            string
	RepoRoot           string
	RuntimeRoot        string
	ProcessComposePort int
	FrontendPort       int
	LocalProxy         LocalProxyConfig
	AuthService        AuthServiceConfig
	CaddyVersion       string
	Algorithm          AlgorithmConfig
}

type LocalProxyConfig struct {
	Address      string
	Port         int
	AuthHostPort int
	CoreHostPort int
	ChatHostPort int
	ScanHostPort int
	EvoHostPort  int
}

type AuthServiceConfig struct {
	Port        int
	Python      string
	DatabaseURL string
	InstallDeps bool
}

type AlgorithmConfig struct {
	PostgresPort   int
	DocPort        int
	ProcessorPort  int
	AlgoPort       int
	WorkerPort     int
	ChatPort       int
	EvoPort        int
	MilvusPort     int
	OpenSearchPort int
	EnableEvo      bool
}

type ServiceEndpoints struct {
	Host      ServiceEndpointURLs `json:"host"`
	Container ServiceEndpointURLs `json:"container"`
}

type ServiceEndpointURLs struct {
	AuthServiceBaseURL     string `json:"authServiceBaseUrl"`
	CoreBaseURL            string `json:"coreBaseUrl"`
	DocumentServiceBaseURL string `json:"documentServiceBaseUrl"`
	ProcessorBaseURL       string `json:"processorBaseUrl"`
	ChatBaseURL            string `json:"chatBaseUrl"`
	EvoBaseURL             string `json:"evoBaseUrl"`
	OfficeConvertURL       string `json:"officeConvertUrl"`
	PostgresAddress        string `json:"postgresAddress"`
}

func defaultProfileValue() string {
	if v := os.Getenv(defaultProfileEnvVar); v != "" {
		return v
	}
	return defaultProfile
}

func defaultProcessComposePortValue() int {
	if strings.TrimSpace(os.Getenv(processComposePortEnvVar)) != "" {
		return envPort(processComposePortEnvVar, defaultProcessComposePort)
	}
	return firstAvailableLocalPort(defaultProcessComposePort, 100)
}

func defaultLocalProxyPortValue() int {
	if strings.TrimSpace(os.Getenv(localProxyPortEnvVar)) != "" {
		return envPort(localProxyPortEnvVar, defaultLocalProxyPort)
	}
	return firstAvailableLocalPort(defaultLocalProxyPort, 100)
}

func firstAvailableLocalPort(start int, attempts int) int {
	for port := start; port < start+attempts && port < 65536; port++ {
		if localPortAvailable(port) {
			return port
		}
	}
	return start
}

type localPortAllocator struct {
	used map[int]struct{}
}

func newLocalPortAllocator() *localPortAllocator {
	return &localPortAllocator{used: map[int]struct{}{}}
}

func (a *localPortAllocator) reserve(port int) int {
	if port > 0 && port < 65536 {
		a.used[port] = struct{}{}
	}
	return port
}

func (a *localPortAllocator) envOrAvailable(envName string, fallback int) int {
	if strings.TrimSpace(os.Getenv(envName)) != "" {
		return a.reserve(envPort(envName, fallback))
	}
	return a.availableFrom(fallback, 500)
}

func (a *localPortAllocator) envOrAvailableDefaultCanMove(envName string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(envName))
	if raw == "" {
		return a.availableFrom(fallback, 500)
	}
	port := envPort(envName, fallback)
	if envBool(localPortsPinnedEnvVar, false) {
		return a.reserve(port)
	}
	if port != fallback || localPortAvailable(port) {
		return a.reserve(port)
	}
	return a.availableFrom(fallback, 500)
}

func (a *localPortAllocator) firstEnvOrAvailable(envNames []string, fallback int) int {
	for _, envName := range envNames {
		if strings.TrimSpace(os.Getenv(envName)) != "" {
			return a.reserve(envPort(envName, fallback))
		}
	}
	return a.availableFrom(fallback, 500)
}

func (a *localPortAllocator) availableFrom(start int, attempts int) int {
	for port := start; port < start+attempts && port < 65536; port++ {
		if _, ok := a.used[port]; ok {
			continue
		}
		if !localPortAvailable(port) {
			continue
		}
		return a.reserve(port)
	}
	return a.reserve(start)
}

func localPortAvailable(port int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func envPort(name string, fallback int) int {
	if v := os.Getenv(name); v != "" {
		port, err := strconv.Atoi(v)
		if err == nil && port > 0 && port < 65536 {
			return port
		}
	}
	return fallback
}

func envPortCompat(name, legacyName string, fallback int) int {
	if strings.TrimSpace(os.Getenv(name)) != "" {
		return envPort(name, fallback)
	}
	if legacyName != "" && strings.TrimSpace(os.Getenv(legacyName)) != "" {
		return envPort(legacyName, fallback)
	}
	return fallback
}

func envText(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func defaultAuthServicePortValue() int {
	if v := os.Getenv(localProxyAuthHostPortEnvVar); v != "" {
		return envPort(localProxyAuthHostPortEnvVar, defaultLocalProxyAuthHostPort)
	}
	if v := os.Getenv(authServicePortEnvVar); v != "" {
		return envPort(authServicePortEnvVar, defaultLocalProxyAuthHostPort)
	}
	return defaultLocalProxyAuthHostPort
}

func defaultLocalProxyAuthHostPortValue() int {
	if v := os.Getenv(localProxyAuthHostPortEnvVar); v != "" {
		return envPort(localProxyAuthHostPortEnvVar, defaultLocalProxyAuthHostPort)
	}
	if v := os.Getenv(authServicePortEnvVar); v != "" {
		return envPort(authServicePortEnvVar, defaultLocalProxyAuthHostPort)
	}
	return defaultLocalProxyAuthHostPort
}

func authServiceDatabaseURL(postgresPort int) string {
	if v := strings.TrimSpace(os.Getenv(authServiceDatabaseURLEnvVar)); v != "" {
		return v
	}
	if postgresPort <= 0 {
		postgresPort = defaultLocalPostgresPort
	}
	return "postgresql+psycopg://root:123456@127.0.0.1:" + strconv.Itoa(postgresPort) + "/authservice"
}

func serviceEndpointsFromConfig(cfg RuntimeConfig) ServiceEndpoints {
	host := ServiceEndpointURLs{
		AuthServiceBaseURL:     "http://127.0.0.1:" + strconv.Itoa(cfg.AuthService.Port),
		CoreBaseURL:            "http://127.0.0.1:" + strconv.Itoa(cfg.LocalProxy.CoreHostPort),
		DocumentServiceBaseURL: "http://127.0.0.1:" + strconv.Itoa(cfg.Algorithm.DocPort),
		ProcessorBaseURL:       "http://127.0.0.1:" + strconv.Itoa(cfg.Algorithm.ProcessorPort),
		ChatBaseURL:            "http://127.0.0.1:" + strconv.Itoa(cfg.Algorithm.ChatPort),
		EvoBaseURL:             "http://127.0.0.1:" + strconv.Itoa(cfg.Algorithm.EvoPort),
		OfficeConvertURL:       "http://127.0.0.1:18082/v1/office/to-pdf",
		PostgresAddress:        "127.0.0.1:" + strconv.Itoa(cfg.Algorithm.PostgresPort),
	}
	container := ServiceEndpointURLs{
		AuthServiceBaseURL:     "http://host.docker.internal:" + strconv.Itoa(cfg.AuthService.Port),
		CoreBaseURL:            "http://host.docker.internal:" + strconv.Itoa(cfg.LocalProxy.CoreHostPort),
		DocumentServiceBaseURL: "http://host.docker.internal:" + strconv.Itoa(cfg.Algorithm.DocPort),
		ProcessorBaseURL:       "http://host.docker.internal:" + strconv.Itoa(cfg.Algorithm.ProcessorPort),
		ChatBaseURL:            "http://host.docker.internal:" + strconv.Itoa(cfg.Algorithm.ChatPort),
		EvoBaseURL:             "http://host.docker.internal:" + strconv.Itoa(cfg.Algorithm.EvoPort),
		OfficeConvertURL:       "http://host.docker.internal:18082/v1/office/to-pdf",
		PostgresAddress:        "host.docker.internal:" + strconv.Itoa(cfg.Algorithm.PostgresPort),
	}
	return ServiceEndpoints{Host: host, Container: container}
}

func coreDatabaseDSN(port int, database, user, password string) string {
	return "host=127.0.0.1 user=" + user + " password=" + password + " dbname=" + database + " port=" + strconv.Itoa(port) + " sslmode=disable TimeZone=UTC"
}

func resolveRepoRoot(start string) (string, error) {
	if start == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		start = cwd
	}
	start = filepath.Clean(start)

	for {
		candidate := filepath.Join(start, repoComposeFileName)
		if _, err := os.Stat(candidate); err == nil {
			return start, nil
		}
		parent := filepath.Dir(start)
		if parent == start {
			return "", fmt.Errorf("could not find %s in current or parent directories", repoComposeFileName)
		}
		start = parent
	}
}

func NewRuntimeConfig(profile, repoRootHint string) (RuntimeConfig, RuntimePaths, error) {
	if profile == "" {
		profile = defaultProfileValue()
	}
	resolved, err := resolveRepoRoot(repoRootHint)
	if err != nil {
		return RuntimeConfig{}, RuntimePaths{}, err
	}

	root := filepath.Clean(resolved)
	runtimeRoot := filepath.Join(root, ".lazymind-local")
	p := RuntimePaths{
		RepoRoot:             root,
		RuntimeRoot:          runtimeRoot,
		StateDir:             filepath.Join(runtimeRoot, "state"),
		LogsDir:              filepath.Join(runtimeRoot, "logs"),
		RunDir:               filepath.Join(runtimeRoot, "run"),
		GeneratedDir:         filepath.Join(runtimeRoot, "generated"),
		BinDir:               filepath.Join(runtimeRoot, "bin"),
		StateFile:            filepath.Join(runtimeRoot, "state", stateFileName),
		RunDirTokenFile:      filepath.Join(runtimeRoot, "run", tokenFileName),
		UpLockFile:           filepath.Join(runtimeRoot, "run", upLockFileName),
		LogFilePath:          filepath.Join(runtimeRoot, "logs", logFileName),
		LocalProxyLog:        filepath.Join(runtimeRoot, "logs", localProxyLogFileName),
		AuthServiceLog:       filepath.Join(runtimeRoot, "logs", authServiceLogFileName),
		AuthServicePIDFile:   filepath.Join(runtimeRoot, "run", "auth-service.pid"),
		AuthServiceVenvDir:   filepath.Join(runtimeRoot, "venvs", "auth-service"),
		AuthServiceStateDir:  filepath.Join(runtimeRoot, "stores", "sqlite", "auth-state"),
		CoreLog:              filepath.Join(runtimeRoot, "logs", coreLogFileName),
		CorePIDFile:          filepath.Join(runtimeRoot, "run", "core.pid"),
		CoreBin:              filepath.Join(runtimeRoot, "bin", "core"),
		CoreStateDir:         filepath.Join(runtimeRoot, "stores", "sqlite", "core-state"),
		FrontendLog:          filepath.Join(runtimeRoot, "logs", frontendLogFileName),
		DocServerLog:         filepath.Join(runtimeRoot, "logs", docServerProcessName+".log"),
		ProcessorServerLog:   filepath.Join(runtimeRoot, "logs", processorServerProcessName+".log"),
		ProcessorWorkerLog:   filepath.Join(runtimeRoot, "logs", processorWorkerProcessName+".log"),
		AlgoLog:              filepath.Join(runtimeRoot, "logs", algoProcessName+".log"),
		ChatLog:              filepath.Join(runtimeRoot, "logs", chatProcessName+".log"),
		EvoLog:               filepath.Join(runtimeRoot, "logs", evoProcessName+".log"),
		LocalProxyBin:        filepath.Join(runtimeRoot, "bin", "local-proxy"),
		CaddyBin:             filepath.Join(runtimeRoot, "bin", "caddy"),
		LocalProxyConfig:     filepath.Join(root, localProxyConfigName),
		LocalProxyStopScript: filepath.Join(root, localProxyScriptDirName, "stop.sh"),
		CaddyConfig:          filepath.Join(runtimeRoot, "generated", "Caddyfile"),
		GeneratedConfig:      filepath.Join(runtimeRoot, "generated", composeGeneratedFileName),
		ServiceEndpointsJSON: filepath.Join(runtimeRoot, "generated", serviceEndpointsJSONName),
		ServiceEndpointsEnv:  filepath.Join(runtimeRoot, "generated", serviceEndpointsEnvName),
		AlgorithmVenv:        filepath.Join(runtimeRoot, "python", ".venv"),
		AlgorithmPython:      filepath.Join(runtimeRoot, "python", ".venv", "bin", "python"),
		AlgorithmHome:        filepath.Join(runtimeRoot, "home"),
		AlgorithmPIDDir:      filepath.Join(runtimeRoot, "run", "algorithm"),
	}
	ports := newLocalPortAllocator()
	processComposePort := ports.envOrAvailable(processComposePortEnvVar, defaultProcessComposePort)
	frontendPort := ports.envOrAvailableDefaultCanMove(frontendPortEnvVar, defaultFrontendPort)
	localProxyPort := ports.envOrAvailable(localProxyPortEnvVar, defaultLocalProxyPort)
	authHostPort := ports.envOrAvailable(localProxyAuthHostPortEnvVar, defaultLocalProxyAuthHostPort)
	coreHostPort := ports.firstEnvOrAvailable([]string{localCorePortEnvVar, localProxyCoreHostPortEnvVar}, defaultLocalProxyCoreHostPort)
	scanHostPort := ports.envOrAvailable(localProxyScanHostPortEnvVar, defaultLocalProxyScanHostPort)
	postgresPort := ports.envOrAvailable(localPostgresPortEnvVar, defaultLocalPostgresPort)
	docPort := ports.envOrAvailable(localDocPortEnvVar, defaultLocalDocPort)
	processorPort := ports.envOrAvailable(localProcessorPortEnvVar, defaultLocalProcessorPort)
	algoPort := ports.envOrAvailable(localAlgoPortEnvVar, defaultLocalAlgoPort)
	workerPort := ports.envOrAvailable(localWorkerPortEnvVar, defaultLocalWorkerPort)
	milvusPort := ports.envOrAvailable(localMilvusPortEnvVar, defaultLocalMilvusPort)
	openSearchPort := ports.envOrAvailable(localOpenSearchPortEnvVar, defaultLocalOpenSearchPort)
	chatPort := ports.firstEnvOrAvailable([]string{localChatPortEnvVar, localProxyChatHostPortEnvVar}, defaultLocalProxyChatHostPort)
	evoPort := ports.firstEnvOrAvailable([]string{localEvoPortEnvVar, localProxyEvoHostPortEnvVar}, defaultLocalProxyEvoHostPort)
	return RuntimeConfig{
		Profile:            profile,
		RepoRoot:           p.RepoRoot,
		RuntimeRoot:        runtimeRoot,
		ProcessComposePort: processComposePort,
		FrontendPort:       frontendPort,
		CaddyVersion:       envText(caddyVersionEnvVar, defaultCaddyVersion),
		LocalProxy: LocalProxyConfig{
			Address:      envText(localProxyAddressEnvVar, defaultLocalProxyAddress),
			Port:         localProxyPort,
			AuthHostPort: authHostPort,
			CoreHostPort: coreHostPort,
			ChatHostPort: chatPort,
			ScanHostPort: scanHostPort,
			EvoHostPort:  evoPort,
		},
		Algorithm: AlgorithmConfig{
			PostgresPort:   postgresPort,
			DocPort:        docPort,
			ProcessorPort:  processorPort,
			AlgoPort:       algoPort,
			WorkerPort:     workerPort,
			ChatPort:       chatPort,
			EvoPort:        evoPort,
			MilvusPort:     milvusPort,
			OpenSearchPort: openSearchPort,
			EnableEvo:      envBool(localEnableEvoEnvVar, false),
		},
		AuthService: AuthServiceConfig{
			Port:        authHostPort,
			Python:      envText(authServicePythonEnvVar, "python3"),
			DatabaseURL: authServiceDatabaseURL(postgresPort),
			InstallDeps: envBool(authServiceInstallDepsEnvVar, true),
		},
	}, p, nil
}

func (p RuntimePaths) EnsureAllDirs() error {
	dirs := []string{
		p.StateDir,
		p.LogsDir,
		p.RunDir,
		p.GeneratedDir,
		p.BinDir,
		filepath.Dir(p.AuthServicePIDFile),
		p.AuthServiceStateDir,
		p.CoreStateDir,
		p.AuthServiceVenvDir,
		filepath.Dir(p.AlgorithmVenv),
		p.AlgorithmHome,
		p.AlgorithmPIDDir,
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
