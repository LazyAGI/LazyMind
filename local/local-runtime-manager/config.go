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
	localNetworkProfileEnvVar     = "LAZYMIND_LOCAL_NETWORK_PROFILE"
	localProxyAddressEnvVar       = "LAZYMIND_LOCAL_PROXY_ADDRESS"
	localProxyPortEnvVar          = "LAZYMIND_LOCAL_PROXY_PORT"
	localAuthPortEnvVar           = "LAZYMIND_LOCAL_AUTH_PORT"
	localProxyAuthHostPortEnvVar  = "LAZYMIND_LOCAL_PROXY_AUTH_HOST_PORT"
	localProxyCoreHostPortEnvVar  = "LAZYMIND_LOCAL_PROXY_CORE_HOST_PORT"
	localProxyChatHostPortEnvVar  = "LAZYMIND_LOCAL_PROXY_CHAT_HOST_PORT"
	localProxyScanHostPortEnvVar  = "LAZYMIND_LOCAL_PROXY_SCAN_HOST_PORT"
	localProxyEvoHostPortEnvVar   = "LAZYMIND_LOCAL_PROXY_EVO_HOST_PORT"
	localFileWatcherPortEnvVar    = "LAZYMIND_LOCAL_FILE_WATCHER_PORT"
	localPostgresPortEnvVar       = "LAZYMIND_LOCAL_POSTGRES_PORT"
	localCorePortEnvVar           = "LAZYMIND_LOCAL_CORE_PORT"
	localDocPortEnvVar            = "LAZYMIND_LOCAL_DOC_PORT"
	localProcessorPortEnvVar      = "LAZYMIND_LOCAL_PROCESSOR_PORT"
	localAlgoPortEnvVar           = "LAZYMIND_LOCAL_ALGO_PORT"
	localWorkerPortEnvVar         = "LAZYMIND_LOCAL_WORKER_PORT"
	localChatPortEnvVar           = "LAZYMIND_LOCAL_CHAT_PORT"
	localEvoPortEnvVar            = "LAZYMIND_LOCAL_EVO_PORT"
	localMilvusPortEnvVar         = "LAZYMIND_LOCAL_MILVUS_PORT"
	localMilvusLiteDBPathEnvVar   = "LAZYMIND_LOCAL_MILVUS_DB_PATH"
	localOpenSearchPortEnvVar     = "LAZYMIND_LOCAL_OPENSEARCH_PORT"
	localEnableEvoEnvVar          = "LAZYMIND_LOCAL_ENABLE_EVO"
	routerPortPoolStartEnvVar     = "LAZYMIND_ROUTER_PORT_POOL_START"
	routerPortPoolEndEnvVar       = "LAZYMIND_ROUTER_PORT_POOL_END"
	routerPortsPerInstanceEnvVar  = "LAZYMIND_ROUTER_PORTS_PER_INSTANCE"
	frontendPortEnvVar            = "LAZYMIND_FRONTEND_PORT"
	frontendLANOriginEnvVar       = "LAZYMIND_FRONTEND_LAN_ORIGIN"
	authServicePortEnvVar         = "LAZYMIND_AUTH_SERVICE_PORT"
	authServicePythonEnvVar       = "LAZYMIND_AUTH_SERVICE_PYTHON"
	authServiceUVEnvVar           = "LAZYMIND_AUTH_SERVICE_UV"
	authServiceDatabaseURLEnvVar  = "LAZYMIND_AUTH_SERVICE_DATABASE_URL"
	authServiceInstallDepsEnvVar  = "LAZYMIND_AUTH_SERVICE_INSTALL_DEPS"
	localSQLiteDirEnvVar          = "LAZYMIND_LOCAL_SQLITE_DIR"
	caddyBinEnvVar                = "LAZYMIND_CADDY_BIN"
	caddyVersionEnvVar            = "LAZYMIND_CADDY_VERSION"
	defaultProfile                = "linux-browser"
	processComposeVersion         = 2
	defaultCaddyVersion           = "2.10.2"
	defaultProcessComposePort     = 19080
	defaultLocalUpTimeout         = 30 * 60
	defaultLocalDownTimeout       = 2 * 60
	defaultFrontendPort           = 8090
	defaultLocalNetworkProfile    = "localhost"
	defaultLocalProxyAddress      = "127.0.0.1"
	defaultLocalProxyPort         = 5024
	defaultLocalProxyAuthHostPort = 18000
	defaultLocalProxyCoreHostPort = 18001
	defaultLocalProxyChatHostPort = 18046
	defaultLocalProxyScanHostPort = 18080
	defaultLocalProxyEvoHostPort  = 18047
	defaultLocalFileWatcherPort   = 19090
	defaultLocalPostgresPort      = 15432
	defaultLocalDocPort           = 18002
	defaultLocalProcessorPort     = 18003
	defaultLocalAlgoPort          = 18004
	defaultLocalWorkerPort        = 18005
	defaultLocalMilvusPort        = 19530
	defaultLocalOpenSearchPort    = 19200
	defaultRouterPortPoolStart    = 18100
	defaultRouterPortsPerInstance = 100
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
	scanControlPlaneProcessName   = "scan-control-plane"
	fileWatcherProcessName        = "file-watcher"
	frontendProcessName           = "frontend"
	docServerProcessName          = "lazyllm-doc-server"
	processorServerProcessName    = "lazyllm-parse-server"
	processorWorkerProcessName    = "lazyllm-parse-worker"
	algoProcessName               = "lazyllm-algo"
	chatProcessName               = "chat"
	evoProcessName                = "evo-api"
	milvusLiteProcessName         = "milvus-lite"
)

type RuntimePaths struct {
	RepoRoot                 string
	RuntimeRoot              string
	StateDir                 string
	LogsDir                  string
	RunDir                   string
	GeneratedDir             string
	BinDir                   string
	StateFile                string
	RunDirTokenFile          string
	UpLockFile               string
	LogFilePath              string
	LocalProxyLog            string
	AuthServiceLog           string
	AuthServicePIDFile       string
	AuthServiceVenvDir       string
	AuthServiceStateDir      string
	AuthServiceDBPath        string
	CoreLog                  string
	CorePIDFile              string
	CoreBin                  string
	CoreStateDir             string
	CoreDBPath               string
	LazyLLMDBPath            string
	ScanDBPath               string
	ScanControlPlaneLog      string
	ScanControlPlanePIDFile  string
	ScanControlPlaneBin      string
	ScanControlPlaneStateDir string
	ScanControlPlaneTempDir  string
	FileWatcherLog           string
	FileWatcherPIDFile       string
	FileWatcherBin           string
	FileWatcherBaseRoot      string
	FrontendLog              string
	DocServerLog             string
	ProcessorServerLog       string
	ProcessorWorkerLog       string
	AlgoLog                  string
	ChatLog                  string
	EvoLog                   string
	MilvusLiteLog            string
	MilvusLitePIDFile        string
	MilvusLiteDBPath         string
	LocalProxyBin            string
	CaddyBin                 string
	LocalProxyConfig         string
	LocalProxyStopScript     string
	CaddyConfig              string
	GeneratedConfig          string
	ServiceEndpointsJSON     string
	ServiceEndpointsEnv      string
	AlgorithmVenv            string
	AlgorithmPython          string
	AlgorithmHome            string
	AlgorithmPIDDir          string
}

type RuntimeConfig struct {
	Profile            string
	RepoRoot           string
	RuntimeRoot        string
	ModeProfile        RuntimeModeProfileConfig
	ProcessComposePort int
	FrontendPort       int
	NetworkProfile     string
	LocalProxy         LocalProxyConfig
	AuthService        AuthServiceConfig
	CaddyVersion       string
	Algorithm          AlgorithmConfig
	FileWatcher        FileWatcherConfig
	PortResolutions    []PortResolution `json:"-"`
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

type FileWatcherConfig struct {
	Port          int
	AgentID       string
	AgentToken    string
	WatchHostDir  string
	HostPathStyle string
}

type RuntimeModeProfileConfig struct {
	Name        string
	VectorStore VectorStoreConfig
}

type VectorStoreConfig struct {
	Engine         string
	Endpoint       string
	Port           int
	ManagedProcess bool
	DBPath         string
}

type AlgorithmConfig struct {
	PostgresPort   int
	DocPort        int
	ProcessorPort  int
	AlgoPort       int
	WorkerPort     int
	ChatPort       int
	EvoPort        int
	OpenSearchPort int
	EnableEvo      bool
}

type PortResolution struct {
	Name          string
	EnvName       string
	RequestedPort int
	ResolvedPort  int
	Reason        string
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
	used        map[int]struct{}
	resolutions []PortResolution
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
	return a.firstEnvOrAvailable("", []string{envName}, fallback)
}

func (a *localPortAllocator) envOrAvailableDefaultCanMove(envName string, fallback int) int {
	return a.firstEnvOrAvailableOn("", []string{envName}, fallback, "127.0.0.1")
}

func (a *localPortAllocator) envOrAvailableDefaultCanMoveOn(envName string, fallback int, address string) int {
	return a.firstEnvOrAvailableOn("", []string{envName}, fallback, address)
}

func (a *localPortAllocator) firstEnvOrAvailable(name string, envNames []string, fallback int) int {
	return a.firstEnvOrAvailableOn(name, envNames, fallback, "127.0.0.1")
}

func (a *localPortAllocator) firstEnvOrAvailableOn(name string, envNames []string, fallback int, address string) int {
	for _, envName := range envNames {
		if strings.TrimSpace(os.Getenv(envName)) != "" {
			requested := envPort(envName, fallback)
			if envBool(localPortsPinnedEnvVar, false) {
				return a.reserve(requested)
			}
			if a.portAvailableOn(address, requested) {
				return a.reserve(requested)
			}
			resolved := a.availableFromOn(requested, 500, address)
			a.resolutions = append(a.resolutions, PortResolution{
				Name:          name,
				EnvName:       envName,
				RequestedPort: requested,
				ResolvedPort:  resolved,
				Reason:        "preferred port unavailable",
			})
			return resolved
		}
	}
	resolved := a.availableFromOn(fallback, 500, address)
	if resolved != fallback {
		a.resolutions = append(a.resolutions, PortResolution{
			Name:          name,
			RequestedPort: fallback,
			ResolvedPort:  resolved,
			Reason:        "default port unavailable",
		})
	}
	return resolved
}

func (a *localPortAllocator) availableFrom(start int, attempts int) int {
	return a.availableFromOn(start, attempts, "127.0.0.1")
}

func (a *localPortAllocator) availableFromOn(start int, attempts int, address string) int {
	for port := start; port < start+attempts && port < 65536; port++ {
		if a.portAvailableOn(address, port) {
			return a.reserve(port)
		}
	}
	return a.reserve(start)
}

func (a *localPortAllocator) portAvailable(port int) bool {
	if _, ok := a.used[port]; ok {
		return false
	}
	return localPortAvailable(port)
}

func (a *localPortAllocator) portAvailableOn(address string, port int) bool {
	if _, ok := a.used[port]; ok {
		return false
	}
	return localPortAvailableOn(address, port)
}

func (a *localPortAllocator) resolvedPort(name string, envNames []string, fallback int) int {
	return a.firstEnvOrAvailable(name, envNames, fallback)
}

func (a *localPortAllocator) resolvedPortOn(name string, envNames []string, fallback int, address string) int {
	return a.firstEnvOrAvailableOn(name, envNames, fallback, address)
}

func localPortAvailable(port int) bool {
	return localPortAvailableOn("127.0.0.1", port)
}

func localPortAvailableOn(address string, port int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort(address, strconv.Itoa(port)))
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

func localNetworkProfile() (string, error) {
	profile := strings.ToLower(strings.TrimSpace(os.Getenv(localNetworkProfileEnvVar)))
	if profile == "" {
		return defaultLocalNetworkProfile, nil
	}
	switch profile {
	case "localhost", "lan":
		return profile, nil
	default:
		return "", fmt.Errorf("%s must be localhost or lan", localNetworkProfileEnvVar)
	}
}

func defaultFileWatcherWatchHostDir(repoRoot string) string {
	raw := strings.TrimSpace(os.Getenv("LAZYMIND_FILE_WATCHER_WATCH_HOST_DIR"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("HOME"))
	}
	if raw == "" {
		raw = repoRoot
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	if abs, err := filepath.Abs(raw); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(raw)
}

func defaultFileWatcherBaseRoot(repoRoot string) string {
	raw := strings.TrimSpace(os.Getenv("LAZYMIND_FILE_WATCHER_BASE_ROOT"))
	if raw == "" {
		raw = filepath.Join(repoRoot, ".lazymind-local", "stores", "scan", "file-watcher")
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	return filepath.Clean(filepath.Join(repoRoot, raw))
}

func localRuntimeModeProfile(milvusPort int, milvusLiteDBPath string) RuntimeModeProfileConfig {
	return RuntimeModeProfileConfig{
		Name: "local",
		VectorStore: VectorStoreConfig{
			Engine:         "milvus-lite",
			Endpoint:       "http://127.0.0.1:" + strconv.Itoa(milvusPort),
			Port:           milvusPort,
			ManagedProcess: true,
			DBPath:         milvusLiteDBPath,
		},
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

func sqliteURL(path string) string {
	return "sqlite:///" + filepath.ToSlash(path)
}

func sqliteDSN(path string) string {
	return "file:" + filepath.ToSlash(path) + "?_journal_mode=WAL&_busy_timeout=30000&_foreign_keys=on"
}

func authServiceDatabaseURL(path string) string {
	if v := strings.TrimSpace(os.Getenv(authServiceDatabaseURLEnvVar)); v != "" {
		return v
	}
	return sqliteURL(path)
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
	sqliteRoot := envText(localSQLiteDirEnvVar, filepath.Join(runtimeRoot, "stores", "sqlite"))
	p := RuntimePaths{
		RepoRoot:                 root,
		RuntimeRoot:              runtimeRoot,
		StateDir:                 filepath.Join(runtimeRoot, "state"),
		LogsDir:                  filepath.Join(runtimeRoot, "logs"),
		RunDir:                   filepath.Join(runtimeRoot, "run"),
		GeneratedDir:             filepath.Join(runtimeRoot, "generated"),
		BinDir:                   filepath.Join(runtimeRoot, "bin"),
		StateFile:                filepath.Join(runtimeRoot, "state", stateFileName),
		RunDirTokenFile:          filepath.Join(runtimeRoot, "run", tokenFileName),
		UpLockFile:               filepath.Join(runtimeRoot, "run", upLockFileName),
		LogFilePath:              filepath.Join(runtimeRoot, "logs", logFileName),
		LocalProxyLog:            filepath.Join(runtimeRoot, "logs", localProxyLogFileName),
		AuthServiceLog:           filepath.Join(runtimeRoot, "logs", authServiceLogFileName),
		AuthServicePIDFile:       filepath.Join(runtimeRoot, "run", "auth-service.pid"),
		AuthServiceVenvDir:       filepath.Join(runtimeRoot, "venvs", "auth-service"),
		AuthServiceStateDir:      filepath.Join(runtimeRoot, "stores", "sqlite", "auth-state"),
		AuthServiceDBPath:        filepath.Join(sqliteRoot, "auth", "authservice.db"),
		CoreLog:                  filepath.Join(runtimeRoot, "logs", coreLogFileName),
		CorePIDFile:              filepath.Join(runtimeRoot, "run", "core.pid"),
		CoreBin:                  filepath.Join(runtimeRoot, "bin", "core"),
		CoreStateDir:             filepath.Join(runtimeRoot, "stores", "sqlite", "core-state"),
		CoreDBPath:               filepath.Join(sqliteRoot, "core", "core.db"),
		LazyLLMDBPath:            filepath.Join(sqliteRoot, "lazyllm", "app.db"),
		ScanDBPath:               filepath.Join(sqliteRoot, "scan", "scan_control_plane.db"),
		ScanControlPlaneLog:      filepath.Join(runtimeRoot, "logs", scanControlPlaneProcessName+".log"),
		ScanControlPlanePIDFile:  filepath.Join(runtimeRoot, "run", scanControlPlaneProcessName+".pid"),
		ScanControlPlaneBin:      filepath.Join(runtimeRoot, "bin", scanControlPlaneProcessName),
		ScanControlPlaneStateDir: filepath.Join(runtimeRoot, "stores", "sqlite", "scan-state"),
		ScanControlPlaneTempDir:  filepath.Join(runtimeRoot, "tmp", scanControlPlaneProcessName, "sourceengine"),
		FileWatcherLog:           filepath.Join(runtimeRoot, "logs", fileWatcherProcessName+".log"),
		FileWatcherPIDFile:       filepath.Join(runtimeRoot, "run", fileWatcherProcessName+".pid"),
		FileWatcherBin:           filepath.Join(runtimeRoot, "bin", fileWatcherProcessName),
		FileWatcherBaseRoot:      defaultFileWatcherBaseRoot(root),
		FrontendLog:              filepath.Join(runtimeRoot, "logs", frontendLogFileName),
		DocServerLog:             filepath.Join(runtimeRoot, "logs", docServerProcessName+".log"),
		ProcessorServerLog:       filepath.Join(runtimeRoot, "logs", processorServerProcessName+".log"),
		ProcessorWorkerLog:       filepath.Join(runtimeRoot, "logs", processorWorkerProcessName+".log"),
		AlgoLog:                  filepath.Join(runtimeRoot, "logs", algoProcessName+".log"),
		ChatLog:                  filepath.Join(runtimeRoot, "logs", chatProcessName+".log"),
		EvoLog:                   filepath.Join(runtimeRoot, "logs", evoProcessName+".log"),
		MilvusLiteLog:            filepath.Join(runtimeRoot, "logs", milvusLiteProcessName+".log"),
		MilvusLitePIDFile:        filepath.Join(runtimeRoot, "run", milvusLiteProcessName+".pid"),
		MilvusLiteDBPath:         filepath.Join(runtimeRoot, "stores", "milvus", "lazymind.db"),
		LocalProxyBin:            filepath.Join(runtimeRoot, "bin", "local-proxy"),
		CaddyBin:                 filepath.Join(runtimeRoot, "bin", "caddy"),
		LocalProxyConfig:         filepath.Join(root, localProxyConfigName),
		LocalProxyStopScript:     filepath.Join(root, localProxyScriptDirName, "stop.sh"),
		CaddyConfig:              filepath.Join(runtimeRoot, "generated", "Caddyfile"),
		GeneratedConfig:          filepath.Join(runtimeRoot, "generated", composeGeneratedFileName),
		ServiceEndpointsJSON:     filepath.Join(runtimeRoot, "generated", serviceEndpointsJSONName),
		ServiceEndpointsEnv:      filepath.Join(runtimeRoot, "generated", serviceEndpointsEnvName),
		AlgorithmVenv:            filepath.Join(runtimeRoot, "python", ".venv"),
		AlgorithmPython:          filepath.Join(runtimeRoot, "python", ".venv", "bin", "python"),
		AlgorithmHome:            filepath.Join(runtimeRoot, "home"),
		AlgorithmPIDDir:          filepath.Join(runtimeRoot, "run", "algorithm"),
	}
	ports := newLocalPortAllocator()
	networkProfile, err := localNetworkProfile()
	if err != nil {
		return RuntimeConfig{}, RuntimePaths{}, err
	}
	frontendBindCheckAddress := "127.0.0.1"
	if networkProfile == "lan" {
		frontendBindCheckAddress = "0.0.0.0"
	}
	processComposePort := ports.resolvedPort("process-compose", []string{processComposePortEnvVar}, defaultProcessComposePort)
	frontendPort := ports.resolvedPortOn("frontend", []string{frontendPortEnvVar}, defaultFrontendPort, frontendBindCheckAddress)
	localProxyPort := ports.resolvedPort("local-proxy", []string{localProxyPortEnvVar}, defaultLocalProxyPort)
	authHostPort := ports.resolvedPort("auth-service", []string{localAuthPortEnvVar, localProxyAuthHostPortEnvVar, authServicePortEnvVar}, defaultLocalProxyAuthHostPort)
	coreHostPort := ports.resolvedPort("core", []string{localCorePortEnvVar, localProxyCoreHostPortEnvVar}, defaultLocalProxyCoreHostPort)
	scanHostPort := ports.resolvedPort("scan-control-plane", []string{localProxyScanHostPortEnvVar}, defaultLocalProxyScanHostPort)
	fileWatcherPort := ports.resolvedPort("file-watcher", []string{localFileWatcherPortEnvVar}, defaultLocalFileWatcherPort)
	postgresPort := ports.resolvedPort("postgres", []string{localPostgresPortEnvVar}, defaultLocalPostgresPort)
	docPort := ports.resolvedPort("document-service", []string{localDocPortEnvVar}, defaultLocalDocPort)
	processorPort := ports.resolvedPort("processor-server", []string{localProcessorPortEnvVar}, defaultLocalProcessorPort)
	algoPort := ports.resolvedPort("lazyllm-algo", []string{localAlgoPortEnvVar}, defaultLocalAlgoPort)
	workerPort := ports.resolvedPort("processor-worker", []string{localWorkerPortEnvVar}, defaultLocalWorkerPort)
	milvusPort := ports.resolvedPort("milvus-lite", []string{localMilvusPortEnvVar}, defaultLocalMilvusPort)
	openSearchPort := ports.resolvedPort("opensearch", []string{localOpenSearchPortEnvVar}, defaultLocalOpenSearchPort)
	chatPort := ports.resolvedPort("chat", []string{localChatPortEnvVar, localProxyChatHostPortEnvVar}, defaultLocalProxyChatHostPort)
	evoPort := ports.resolvedPort("evo-api", []string{localEvoPortEnvVar, localProxyEvoHostPortEnvVar}, defaultLocalProxyEvoHostPort)
	milvusLiteDBPath := filepath.Clean(envText(localMilvusLiteDBPathEnvVar, p.MilvusLiteDBPath))
	return RuntimeConfig{
		Profile:            profile,
		RepoRoot:           p.RepoRoot,
		RuntimeRoot:        runtimeRoot,
		ModeProfile:        localRuntimeModeProfile(milvusPort, milvusLiteDBPath),
		ProcessComposePort: processComposePort,
		FrontendPort:       frontendPort,
		NetworkProfile:     networkProfile,
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
			OpenSearchPort: openSearchPort,
			EnableEvo:      envBool(localEnableEvoEnvVar, false),
		},
		AuthService: AuthServiceConfig{
			Port:        authHostPort,
			Python:      envText(authServicePythonEnvVar, "python3"),
			DatabaseURL: authServiceDatabaseURL(p.AuthServiceDBPath),
			InstallDeps: envBool(authServiceInstallDepsEnvVar, true),
		},
		FileWatcher: FileWatcherConfig{
			Port:          fileWatcherPort,
			AgentID:       envText("LAZYMIND_FILE_WATCHER_AGENT_ID", envText("LAZYMIND_SCAN_CONTROL_PLANE_LOCAL_FS_DEFAULT_AGENT_ID", "file-watcher-local-001")),
			AgentToken:    envText("LAZYMIND_FILE_WATCHER_AGENT_TOKEN", envText("LAZYMIND_SCAN_CONTROL_PLANE_AGENT_TOKEN", "my-secret-token")),
			WatchHostDir:  defaultFileWatcherWatchHostDir(root),
			HostPathStyle: envText("LAZYMIND_FILE_WATCHER_HOST_PATH_STYLE", "posix"),
		},
		PortResolutions: ports.resolutions,
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
		filepath.Dir(p.AuthServiceDBPath),
		p.CoreStateDir,
		filepath.Dir(p.CoreDBPath),
		filepath.Dir(p.LazyLLMDBPath),
		filepath.Dir(p.ScanDBPath),
		p.ScanControlPlaneStateDir,
		p.ScanControlPlaneTempDir,
		p.FileWatcherBaseRoot,
		p.AuthServiceVenvDir,
		filepath.Dir(p.AlgorithmVenv),
		p.AlgorithmHome,
		p.AlgorithmPIDDir,
		filepath.Dir(p.MilvusLiteDBPath),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	for _, d := range []string{
		filepath.Dir(filepath.Dir(p.AuthServiceDBPath)),
		filepath.Dir(p.AuthServiceDBPath),
		filepath.Dir(p.CoreDBPath),
		filepath.Dir(p.LazyLLMDBPath),
		filepath.Dir(p.ScanDBPath),
	} {
		_ = os.Chmod(d, 0o777)
	}
	return nil
}
