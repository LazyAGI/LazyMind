package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	runtimeProfileEnvVar             = "LAZYMIND_RUNTIME_PROFILE"
	runtimeRootEnvVar                = "LAZYMIND_RUNTIME_ROOT"
	localBuildRootEnvVar             = "LAZYMIND_LOCAL_BUILD_ROOT"
	runtimeResourcesRootEnvVar       = "LAZYMIND_RUNTIME_RESOURCES_ROOT"
	runtimeOwnerTokenEnvVar          = "LAZYMIND_RUNTIME_OWNER_TOKEN"
	localPortsPinnedEnvVar           = "LAZYMIND_LOCAL_PORTS_PINNED"
	processComposePortEnvVar         = "LAZYMIND_PROCESS_COMPOSE_PORT"
	processComposeDownTimeoutEnvVar  = "LAZYMIND_PROCESS_COMPOSE_DOWN_TIMEOUT"
	localUpTimeoutEnvVar             = "LAZYMIND_LOCAL_UP_TIMEOUT"
	localDownTimeoutEnvVar           = "LAZYMIND_LOCAL_DOWN_TIMEOUT"
	localNetworkProfileEnvVar        = "LAZYMIND_LOCAL_NETWORK_PROFILE"
	localAutoLoginAllowLANEnvVar     = "LAZYMIND_LOCAL_AUTO_LOGIN_ALLOW_LAN"
	localProxyAddressEnvVar          = "LAZYMIND_LOCAL_PROXY_ADDRESS"
	localProxyPortEnvVar             = "LAZYMIND_LOCAL_PROXY_PORT"
	localAuthPortEnvVar              = "LAZYMIND_LOCAL_AUTH_PORT"
	localProxyAuthHostPortEnvVar     = "LAZYMIND_LOCAL_PROXY_AUTH_HOST_PORT"
	localProxyCoreHostPortEnvVar     = "LAZYMIND_LOCAL_PROXY_CORE_HOST_PORT"
	localProxyChatHostPortEnvVar     = "LAZYMIND_LOCAL_PROXY_CHAT_HOST_PORT"
	localProxyScanHostPortEnvVar     = "LAZYMIND_LOCAL_PROXY_SCAN_HOST_PORT"
	localProxyEvoHostPortEnvVar      = "LAZYMIND_LOCAL_PROXY_EVO_HOST_PORT"
	localFileWatcherPortEnvVar       = "LAZYMIND_LOCAL_FILE_WATCHER_PORT"
	localPostgresPortEnvVar          = "LAZYMIND_LOCAL_POSTGRES_PORT"
	localCorePortEnvVar              = "LAZYMIND_LOCAL_CORE_PORT"
	localDocPortEnvVar               = "LAZYMIND_LOCAL_DOC_PORT"
	localProcessorPortEnvVar         = "LAZYMIND_LOCAL_PROCESSOR_PORT"
	localAlgoPortEnvVar              = "LAZYMIND_LOCAL_ALGO_PORT"
	localWorkerPortEnvVar            = "LAZYMIND_LOCAL_WORKER_PORT"
	localChatPortEnvVar              = "LAZYMIND_LOCAL_CHAT_PORT"
	localEvoPortEnvVar               = "LAZYMIND_LOCAL_EVO_PORT"
	localMilvusPortEnvVar            = "LAZYMIND_LOCAL_MILVUS_PORT"
	localMilvusLiteDataDirEnvVar     = "LAZYMIND_LOCAL_MILVUS_DATA_DIR"
	localMilvusLiteDBPathEnvVar      = "LAZYMIND_LOCAL_MILVUS_DB_PATH" // legacy; remove after the v3 transition
	localOpenSearchPortEnvVar        = "LAZYMIND_LOCAL_OPENSEARCH_PORT"
	localEnableEvoEnvVar             = "LAZYMIND_LOCAL_ENABLE_EVO"
	routerPortPoolStartEnvVar        = "LAZYMIND_ROUTER_PORT_POOL_START"
	routerPortPoolEndEnvVar          = "LAZYMIND_ROUTER_PORT_POOL_END"
	routerPortsPerInstanceEnvVar     = "LAZYMIND_ROUTER_PORTS_PER_INSTANCE"
	frontendPortEnvVar               = "LAZYMIND_FRONTEND_PORT"
	frontendLANOriginEnvVar          = "LAZYMIND_FRONTEND_LAN_ORIGIN"
	authServicePortEnvVar            = "LAZYMIND_AUTH_SERVICE_PORT"
	authServiceUVEnvVar              = "LAZYMIND_AUTH_SERVICE_UV"
	authServiceDatabaseURLEnvVar     = "LAZYMIND_AUTH_SERVICE_DATABASE_URL"
	authServiceInstallDepsEnvVar     = "LAZYMIND_AUTH_SERVICE_INSTALL_DEPS"
	localPythonVersionEnvVar         = "LAZYMIND_LOCAL_PYTHON_VERSION"
	localSQLiteDirEnvVar             = "LAZYMIND_LOCAL_SQLITE_DIR"
	caddyBinEnvVar                   = "LAZYMIND_CADDY_BIN"
	caddyVersionEnvVar               = "LAZYMIND_CADDY_VERSION"
	processComposeVersion            = 2
	defaultCaddyVersion              = "2.10.2"
	defaultLocalPythonVersion        = "3.11.15"
	defaultProcessComposePort        = 19080
	defaultProcessComposeDownTimeout = 60
	defaultLocalUpTimeout            = 30 * 60
	defaultLocalDownTimeout          = 2 * 60
	defaultFrontendPort              = 8090
	defaultLocalNetworkProfile       = "localhost"
	defaultLocalProxyAddress         = "127.0.0.1"
	defaultLocalProxyPort            = 5024
	defaultLocalProxyAuthHostPort    = 18000
	defaultLocalProxyCoreHostPort    = 18001
	defaultLocalProxyChatHostPort    = 18046
	defaultLocalProxyScanHostPort    = 18080
	defaultLocalProxyEvoHostPort     = 18047
	defaultLocalFileWatcherPort      = 19090
	defaultLocalPostgresPort         = 15432
	defaultLocalDocPort              = 18002
	defaultLocalProcessorPort        = 18003
	defaultLocalAlgoPort             = 18004
	defaultLocalWorkerPort           = 18005
	defaultLocalMilvusPort           = 19530
	defaultLocalOpenSearchPort       = 19200
	defaultRouterPortPoolStart       = 18100
	defaultRouterPortsPerInstance    = 100
	stateFileName                    = "runtime-state.json"
	composeGeneratedFileName         = "process-compose.generated.yaml"
	serviceEndpointsJSONName         = "service-endpoints.json"
	serviceEndpointsEnvName          = "service-endpoints.env"
	tokenFileName                    = "pc-token"
	upLockFileName                   = "up.lock"
	logFileName                      = "process-compose.log"
	localProxyLogFileName            = "local-proxy.log"
	authServiceLogFileName           = "auth-service.log"
	coreLogFileName                  = "core.log"
	frontendLogFileName              = "frontend.log"
	localProcessComposeBin           = "local/build/bin/process-compose"
	localProxyConfigName             = "local/local-proxy/configs/cloud-replace-kong.yaml"
	localProxyScriptDirName          = "local/local-proxy/scripts"
	localProxySourceDirName          = "local/local-proxy"
	authServiceSourceDirName         = "backend/auth-service"
	coreSourceDirName                = "backend/core"
	processComposeServiceName        = "process-supervisor"
	localProxyProcessName            = "local-proxy"
	authServiceProcessName           = "auth-service"
	coreProcessName                  = "core"
	scanControlPlaneProcessName      = "scan-control-plane"
	fileWatcherProcessName           = "file-watcher"
	frontendProcessName              = "frontend"
	docServerProcessName             = "lazyllm-doc-server"
	processorServerProcessName       = "lazyllm-parse-server"
	processorWorkerProcessName       = "lazyllm-parse-worker"
	algoProcessName                  = "lazyllm-algo"
	chatProcessName                  = "chat"
	evoProcessName                   = "evo-api"
	milvusLiteProcessName            = "milvus-lite"
)

type RuntimePaths struct {
	RepoRoot                 string
	ResourcesRoot            string
	BuildRoot                string
	RuntimeRoot              string
	CacheDir                 string
	DataDir                  string
	DepsDir                  string
	StateDir                 string
	LogsDir                  string
	RunDir                   string
	ConfigDir                string
	GeneratedDir             string
	BinDir                   string
	StateFile                string
	ProcessRegistryFile      string
	RunDirTokenFile          string
	UpLockFile               string
	LogFilePath              string
	ProcessComposeBin        string
	ProcessComposePIDFile    string
	LocalProxyLog            string
	AuthServiceLog           string
	AuthServicePIDFile       string
	AuthServiceVenvDir       string
	PythonRuntimeDir         string
	NodeRuntimeDir           string
	PythonStateDir           string
	UVCacheDir               string
	PipCacheDir              string
	XDGCacheDir              string
	XDGStateDir              string
	ProcessComposeHome       string
	ServiceHome              string
	AuthServiceStateDir      string
	AuthServiceDBPath        string
	CoreLog                  string
	CorePIDFile              string
	CoreBin                  string
	CoreStateDir             string
	CoreDBPath               string
	LazyLLMDBPath            string
	UploadRoot               string
	LazyLLMTempDir           string
	OCRCacheDir              string
	SubagentDataDir          string
	TracesDir                string
	LazyLLMHome              string
	EvoDataDir               string
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
	FrontendPIDFile          string
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
	FrontendNodeModules      string
	AlgorithmPIDDir          string
}

type RuntimeConfig struct {
	Profile            string
	OwnerToken         string
	RepoRoot           string
	BuildRoot          string
	ResourcesRoot      string
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

type RuntimeConfigOptions struct {
	Profile       string
	OwnerToken    string
	RepoRoot      string
	RuntimeRoot   string
	BuildRoot     string
	ResourcesRoot string
}

type RuntimePathLayout struct {
	DataRoot        string
	CacheRoot       string
	LogsRoot        string
	LocalImportRoot string
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
	Port          int
	PythonVersion string
	DatabaseURL   string
	InstallDeps   bool
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
	PostgresPort        int
	DocPort             int
	ProcessorPort       int
	AlgoPort            int
	WorkerPort          int
	ChatPort            int
	EvoPort             int
	OpenSearchPort      int
	RouterPortPoolStart int
	RouterPortPoolEnd   int
	EnableEvo           bool
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

func (a *localPortAllocator) availableBlockFromOn(start int, size int, attempts int, address string) int {
	if size <= 0 {
		return a.availableFromOn(start, attempts, address)
	}
	for port := start; port < start+attempts && port+size-1 < 65536; port++ {
		ok := true
		for candidate := port; candidate < port+size; candidate++ {
			if !a.portAvailableOn(address, candidate) {
				ok = false
				break
			}
		}
		if ok {
			for candidate := port; candidate < port+size; candidate++ {
				a.reserve(candidate)
			}
			return port
		}
	}
	for candidate := start; candidate < start+size && candidate < 65536; candidate++ {
		a.reserve(candidate)
	}
	return start
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

func defaultFileWatcherWatchHostDir(defaultRoot string) string {
	raw := strings.TrimSpace(os.Getenv("LAZYMIND_FILE_WATCHER_WATCH_HOST_DIR"))
	if raw == "" {
		raw = defaultRoot
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	if abs, err := filepath.Abs(raw); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(raw)
}

func defaultFileWatcherBaseRoot(runtimeRoot string) string {
	raw := strings.TrimSpace(os.Getenv("LAZYMIND_FILE_WATCHER_BASE_ROOT"))
	if raw == "" {
		raw = filepath.Join(runtimeRoot, "data", "stores", "scan", "file-watcher")
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	return filepath.Clean(filepath.Join(runtimeRoot, raw))
}

func defaultFileWatcherHostPathStyle() string {
	if runtime.GOOS == "windows" {
		return "windows"
	}
	return "posix"
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
		makefile := filepath.Join(start, "Makefile")
		managerMod := filepath.Join(start, "local", "local-runtime-manager", "go.mod")
		if _, err := os.Stat(makefile); err == nil {
			if _, err := os.Stat(managerMod); err == nil {
				return start, nil
			}
		}
		if _, err := os.Stat(filepath.Join(start, ".git")); err == nil {
			return start, nil
		}
		parent := filepath.Dir(start)
		if parent == start {
			return "", fmt.Errorf("could not find LazyMind repo root in current or parent directories")
		}
		start = parent
	}
}

func NewRuntimeConfig(profile, repoRootHint string) (RuntimeConfig, RuntimePaths, error) {
	return NewRuntimeConfigWithOptions(RuntimeConfigOptions{Profile: profile, RepoRoot: repoRootHint})
}

func NewRuntimeConfigWithOptions(opts RuntimeConfigOptions) (RuntimeConfig, RuntimePaths, error) {
	profile, err := normalizeRuntimeProfile(firstNonEmpty(opts.Profile, os.Getenv(runtimeProfileEnvVar), "local"))
	if err != nil {
		return RuntimeConfig{}, RuntimePaths{}, err
	}
	resolved, err := resolveRepoRoot(opts.RepoRoot)
	if err != nil {
		return RuntimeConfig{}, RuntimePaths{}, err
	}

	root := filepath.Clean(resolved)
	resourcesRoot := cleanOptionalPath(firstNonEmpty(opts.ResourcesRoot, os.Getenv(runtimeResourcesRootEnvVar), root))
	defaultBuildRoot := filepath.Join(root, "local", "build")
	if profile == "desktop" {
		defaultBuildRoot = resourcesRoot
	}
	buildRoot := cleanOptionalPath(firstNonEmpty(opts.BuildRoot, os.Getenv(localBuildRootEnvVar), defaultBuildRoot))
	pathLayout := defaultRuntimePathLayout()
	runtimeRoot := cleanOptionalPath(firstNonEmpty(opts.RuntimeRoot, os.Getenv(runtimeRootEnvVar), pathLayout.DataRoot))
	cacheRoot := cleanOptionalPath(pathLayout.CacheRoot)
	dataRoot := filepath.Join(runtimeRoot, "data")
	depsRoot := filepath.Join(buildRoot, "deps")
	sqliteRoot := envText(localSQLiteDirEnvVar, filepath.Join(dataRoot, "stores", "sqlite"))
	uploadRoot := filepath.Join(runtimeRoot, "data", "core", "uploads")
	frontendNodeModules := filepath.Join(depsRoot, "node", "frontend")
	logsRoot := cleanOptionalPath(pathLayout.LogsRoot)
	p := RuntimePaths{
		RepoRoot:                 root,
		ResourcesRoot:            resourcesRoot,
		BuildRoot:                buildRoot,
		RuntimeRoot:              runtimeRoot,
		CacheDir:                 cacheRoot,
		DataDir:                  dataRoot,
		DepsDir:                  depsRoot,
		StateDir:                 filepath.Join(runtimeRoot, "state"),
		LogsDir:                  logsRoot,
		RunDir:                   filepath.Join(runtimeRoot, "run"),
		ConfigDir:                filepath.Join(runtimeRoot, "config"),
		GeneratedDir:             filepath.Join(runtimeRoot, "generated"),
		BinDir:                   filepath.Join(buildRoot, "bin"),
		StateFile:                filepath.Join(runtimeRoot, "state", stateFileName),
		ProcessRegistryFile:      filepath.Join(runtimeRoot, "run", "processes.json"),
		RunDirTokenFile:          filepath.Join(runtimeRoot, "run", tokenFileName),
		UpLockFile:               filepath.Join(runtimeRoot, "run", upLockFileName),
		LogFilePath:              filepath.Join(logsRoot, logFileName),
		ProcessComposeBin:        executablePath(filepath.Join(buildRoot, "bin"), "process-compose"),
		ProcessComposePIDFile:    filepath.Join(runtimeRoot, "run", "process-compose.pid"),
		LocalProxyLog:            filepath.Join(logsRoot, localProxyLogFileName),
		AuthServiceLog:           filepath.Join(logsRoot, authServiceLogFileName),
		AuthServicePIDFile:       filepath.Join(runtimeRoot, "run", "auth-service.pid"),
		AuthServiceVenvDir:       filepath.Join(depsRoot, "python", "auth-service"),
		PythonRuntimeDir:         filepath.Join(buildRoot, "runtimes", "python"),
		NodeRuntimeDir:           filepath.Join(buildRoot, "runtimes", "node"),
		PythonStateDir:           filepath.Join(runtimeRoot, "state", "python"),
		UVCacheDir:               filepath.Join(defaultHostCacheDir(hostHomeDir()), "uv"),
		PipCacheDir:              filepath.Join(defaultHostCacheDir(hostHomeDir()), "pip"),
		XDGCacheDir:              filepath.Join(cacheRoot, "xdg"),
		XDGStateDir:              filepath.Join(runtimeRoot, "state", "xdg"),
		ProcessComposeHome:       filepath.Join(dataRoot, "homes", "process-compose"),
		ServiceHome:              filepath.Join(dataRoot, "homes", "services"),
		AuthServiceStateDir:      filepath.Join(dataRoot, "stores", "sqlite", "auth-state"),
		AuthServiceDBPath:        filepath.Join(sqliteRoot, "auth", "authservice.db"),
		CoreLog:                  filepath.Join(logsRoot, coreLogFileName),
		CorePIDFile:              filepath.Join(runtimeRoot, "run", "core.pid"),
		CoreBin:                  executablePath(filepath.Join(buildRoot, "bin"), "core"),
		CoreStateDir:             filepath.Join(dataRoot, "stores", "sqlite", "core-state"),
		CoreDBPath:               filepath.Join(sqliteRoot, "core", "core.db"),
		LazyLLMDBPath:            filepath.Join(sqliteRoot, "lazyllm", "app.db"),
		UploadRoot:               uploadRoot,
		LazyLLMTempDir:           filepath.Join(uploadRoot, ".lazyllm_temp"),
		OCRCacheDir:              filepath.Join(uploadRoot, ".image_cache"),
		SubagentDataDir:          filepath.Join(dataRoot, "subagent"),
		TracesDir:                filepath.Join(dataRoot, "traces"),
		LazyLLMHome:              filepath.Join(dataRoot, "homes", "lazyllm"),
		EvoDataDir:               filepath.Join(dataRoot, "evo"),
		ScanDBPath:               filepath.Join(sqliteRoot, "scan", "scan_control_plane.db"),
		ScanControlPlaneLog:      filepath.Join(logsRoot, scanControlPlaneProcessName+".log"),
		ScanControlPlanePIDFile:  filepath.Join(runtimeRoot, "run", scanControlPlaneProcessName+".pid"),
		ScanControlPlaneBin:      executablePath(filepath.Join(buildRoot, "bin"), scanControlPlaneProcessName),
		ScanControlPlaneStateDir: filepath.Join(dataRoot, "stores", "sqlite", "scan-state"),
		ScanControlPlaneTempDir:  filepath.Join(runtimeRoot, "tmp", scanControlPlaneProcessName, "sourceengine"),
		FileWatcherLog:           filepath.Join(logsRoot, fileWatcherProcessName+".log"),
		FileWatcherPIDFile:       filepath.Join(runtimeRoot, "run", fileWatcherProcessName+".pid"),
		FileWatcherBin:           executablePath(filepath.Join(buildRoot, "bin"), fileWatcherProcessName),
		FileWatcherBaseRoot:      defaultFileWatcherBaseRoot(runtimeRoot),
		FrontendLog:              filepath.Join(logsRoot, frontendLogFileName),
		FrontendPIDFile:          filepath.Join(runtimeRoot, "run", frontendProcessName+".pid"),
		DocServerLog:             filepath.Join(logsRoot, docServerProcessName+".log"),
		ProcessorServerLog:       filepath.Join(logsRoot, processorServerProcessName+".log"),
		ProcessorWorkerLog:       filepath.Join(logsRoot, processorWorkerProcessName+".log"),
		AlgoLog:                  filepath.Join(logsRoot, algoProcessName+".log"),
		ChatLog:                  filepath.Join(logsRoot, chatProcessName+".log"),
		EvoLog:                   filepath.Join(logsRoot, evoProcessName+".log"),
		MilvusLiteLog:            filepath.Join(logsRoot, milvusLiteProcessName+".log"),
		MilvusLitePIDFile:        filepath.Join(runtimeRoot, "run", milvusLiteProcessName+".pid"),
		MilvusLiteDBPath:         filepath.Join(dataRoot, "stores", "milvus", "lazymind-v3"),
		LocalProxyBin:            executablePath(filepath.Join(buildRoot, "bin"), "local-proxy"),
		CaddyBin:                 executablePath(filepath.Join(buildRoot, "bin"), "caddy"),
		LocalProxyConfig:         filepath.Join(root, localProxyConfigName),
		LocalProxyStopScript:     filepath.Join(root, localProxyScriptDirName, "stop.sh"),
		CaddyConfig:              filepath.Join(runtimeRoot, "generated", "Caddyfile"),
		GeneratedConfig:          filepath.Join(runtimeRoot, "generated", composeGeneratedFileName),
		ServiceEndpointsJSON:     filepath.Join(runtimeRoot, "generated", serviceEndpointsJSONName),
		ServiceEndpointsEnv:      filepath.Join(runtimeRoot, "generated", serviceEndpointsEnvName),
		AlgorithmVenv:            filepath.Join(depsRoot, "python", "algorithm"),
		AlgorithmPython:          venvExecutable(filepath.Join(depsRoot, "python", "algorithm"), "python"),
		AlgorithmHome:            filepath.Join(dataRoot, "homes", "lazymind"),
		FrontendNodeModules:      frontendNodeModules,
		AlgorithmPIDDir:          filepath.Join(runtimeRoot, "run", "algorithm"),
	}
	if profile == "desktop" {
		if err := applyDesktopManifestPaths(&p); err != nil {
			return RuntimeConfig{}, RuntimePaths{}, err
		}
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
	routerPoolFallback := defaultRouterPortPoolStart + (processComposePort-defaultProcessComposePort)*defaultRouterPortsPerInstance
	if routerPoolFallback < 1024 || routerPoolFallback+defaultRouterPortsPerInstance-1 >= 65536 {
		routerPoolFallback = defaultRouterPortPoolStart
	}
	routerPoolStart := ports.resolvedPort("router-port-pool", []string{routerPortPoolStartEnvVar}, routerPoolFallback)
	if !envBool(localPortsPinnedEnvVar, false) {
		for {
			conflict := false
			for port := routerPoolStart + 1; port < routerPoolStart+defaultRouterPortsPerInstance && port < 65536; port++ {
				if !ports.portAvailable(port) {
					conflict = true
					break
				}
				ports.reserve(port)
			}
			if !conflict {
				break
			}
			routerPoolStart = ports.availableBlockFromOn(routerPoolStart+defaultRouterPortsPerInstance, defaultRouterPortsPerInstance, 500, "127.0.0.1")
			ports.resolutions = append(ports.resolutions, PortResolution{
				Name:          "router-port-pool",
				RequestedPort: routerPoolFallback,
				ResolvedPort:  routerPoolStart,
				Reason:        "default port range unavailable",
			})
			break
		}
	}
	routerPoolEnd := envPort(routerPortPoolEndEnvVar, routerPoolStart+defaultRouterPortsPerInstance-1)
	milvusDataDir := strings.TrimSpace(os.Getenv(localMilvusLiteDataDirEnvVar))
	if milvusDataDir == "" {
		milvusDataDir = envText(localMilvusLiteDBPathEnvVar, p.MilvusLiteDBPath)
	}
	milvusLiteDBPath := filepath.Clean(milvusDataDir)
	return RuntimeConfig{
		Profile:            profile,
		OwnerToken:         strings.TrimSpace(firstNonEmpty(opts.OwnerToken, os.Getenv(runtimeOwnerTokenEnvVar))),
		RepoRoot:           p.RepoRoot,
		BuildRoot:          p.BuildRoot,
		ResourcesRoot:      p.ResourcesRoot,
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
			PostgresPort:        postgresPort,
			DocPort:             docPort,
			ProcessorPort:       processorPort,
			AlgoPort:            algoPort,
			WorkerPort:          workerPort,
			ChatPort:            chatPort,
			EvoPort:             evoPort,
			OpenSearchPort:      openSearchPort,
			RouterPortPoolStart: routerPoolStart,
			RouterPortPoolEnd:   routerPoolEnd,
			EnableEvo:           envBool(localEnableEvoEnvVar, false),
		},
		AuthService: AuthServiceConfig{
			Port:          authHostPort,
			PythonVersion: envText(localPythonVersionEnvVar, defaultLocalPythonVersion),
			DatabaseURL:   authServiceDatabaseURL(p.AuthServiceDBPath),
			InstallDeps:   envBool(authServiceInstallDepsEnvVar, true),
		},
		FileWatcher: FileWatcherConfig{
			Port:          fileWatcherPort,
			AgentID:       envText("LAZYMIND_FILE_WATCHER_AGENT_ID", envText("LAZYMIND_SCAN_CONTROL_PLANE_LOCAL_FS_DEFAULT_AGENT_ID", "file-watcher-local-001")),
			AgentToken:    envText("LAZYMIND_FILE_WATCHER_AGENT_TOKEN", envText("LAZYMIND_SCAN_CONTROL_PLANE_AGENT_TOKEN", "my-secret-token")),
			WatchHostDir:  defaultFileWatcherWatchHostDir(pathLayout.LocalImportRoot),
			HostPathStyle: envText("LAZYMIND_FILE_WATCHER_HOST_PATH_STYLE", defaultFileWatcherHostPathStyle()),
		},
		PortResolutions: ports.resolutions,
	}, p, nil
}

func applyDesktopManifestPaths(paths *RuntimePaths) error {
	manifest, err := loadRuntimeManifest(paths.ResourcesRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	joinResource := func(value string) string {
		if value == "" {
			return ""
		}
		if filepath.IsAbs(value) {
			return filepath.Clean(value)
		}
		return filepath.Join(paths.ResourcesRoot, value)
	}
	if value := joinResource(manifest.Binaries[processComposeServiceName]); value != "" {
		paths.ProcessComposeBin = value
	}
	if value := joinResource(manifest.Binaries[localProxyProcessName]); value != "" {
		paths.LocalProxyBin = value
	}
	if value := joinResource(manifest.Binaries[coreProcessName]); value != "" {
		paths.CoreBin = value
	}
	if value := joinResource(manifest.Binaries[scanControlPlaneProcessName]); value != "" {
		paths.ScanControlPlaneBin = value
	}
	if value := joinResource(manifest.Binaries[fileWatcherProcessName]); value != "" {
		paths.FileWatcherBin = value
	}
	if value := joinResource(manifest.Binaries["caddy"]); value != "" {
		paths.CaddyBin = value
	}
	if value := joinResource(manifest.Paths.LocalProxyConfig); value != "" {
		paths.LocalProxyConfig = value
	}
	if value := joinResource(manifest.Paths.PythonRuntime); value != "" {
		paths.PythonRuntimeDir = value
	}
	if value := joinResource(manifest.Paths.AuthServiceVenv); value != "" {
		paths.AuthServiceVenvDir = value
	}
	if value := joinResource(manifest.Paths.AlgorithmVenv); value != "" {
		paths.AlgorithmVenv = value
		paths.AlgorithmPython = venvExecutable(value, "python")
	}
	return nil
}

func executableName(name string) string {
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		return name + ".exe"
	}
	return name
}

func executablePath(dir, name string) string {
	return filepath.Join(dir, executableName(name))
}

func venvExecutable(venv, name string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venv, "Scripts", executableName(name))
	}
	return filepath.Join(venv, "bin", name)
}

func normalizeRuntimeProfile(profile string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "", "local":
		return "local", nil
	case "desktop":
		return "desktop", nil
	default:
		return "", fmt.Errorf("%s must be local or desktop", runtimeProfileEnvVar)
	}
}

func defaultRuntimePathLayout() RuntimePathLayout {
	return runtimePathLayoutForGOOS(runtime.GOOS, hostHomeDir(), os.Getenv("LOCALAPPDATA"), os.Getenv("XDG_DATA_HOME"), os.Getenv("XDG_CACHE_HOME"), os.Getenv("XDG_STATE_HOME"))
}

func runtimePathLayoutForGOOS(goos, home, localAppData, xdgDataHome, xdgCacheHome, xdgStateHome string) RuntimePathLayout {
	home = filepath.Clean(strings.TrimSpace(home))
	if home == "" || home == "." {
		home = "."
	}
	appName := "LazyMind"
	documentsRoot := filepath.Join(home, "Documents", appName)
	switch goos {
	case "darwin":
		return RuntimePathLayout{
			DataRoot:        filepath.Join(home, "Library", "Application Support", appName),
			CacheRoot:       filepath.Join(home, "Library", "Caches", appName),
			LogsRoot:        filepath.Join(home, "Library", "Logs", appName),
			LocalImportRoot: documentsRoot,
		}
	case "windows":
		dataRoot := strings.TrimSpace(localAppData)
		if dataRoot == "" {
			dataRoot = filepath.Join(home, "AppData", "Local")
		}
		dataRoot = filepath.Clean(dataRoot)
		return RuntimePathLayout{
			DataRoot:        filepath.Join(dataRoot, appName),
			CacheRoot:       filepath.Join(dataRoot, appName, "Cache"),
			LogsRoot:        filepath.Join(dataRoot, appName, "Logs"),
			LocalImportRoot: documentsRoot,
		}
	default:
		dataRoot := strings.TrimSpace(xdgDataHome)
		if dataRoot == "" {
			dataRoot = filepath.Join(home, ".local", "share")
		}
		cacheRoot := strings.TrimSpace(xdgCacheHome)
		if cacheRoot == "" {
			cacheRoot = filepath.Join(home, ".cache")
		}
		stateRoot := strings.TrimSpace(xdgStateHome)
		if stateRoot == "" {
			stateRoot = filepath.Join(home, ".local", "state")
		}
		return RuntimePathLayout{
			DataRoot:        filepath.Join(filepath.Clean(dataRoot), appName),
			CacheRoot:       filepath.Join(filepath.Clean(cacheRoot), appName),
			LogsRoot:        filepath.Join(filepath.Clean(stateRoot), appName, "logs"),
			LocalImportRoot: documentsRoot,
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func cleanOptionalPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

func (p RuntimePaths) buildRootIsBundledResources() bool {
	buildRoot := filepath.Clean(p.BuildRoot)
	resourcesRoot := filepath.Clean(p.ResourcesRoot)
	repoRoot := filepath.Clean(p.RepoRoot)
	return buildRoot != "" && buildRoot == resourcesRoot && resourcesRoot != repoRoot
}

func (p RuntimePaths) EnsureAllDirs() error {
	dirs := []string{
		p.CacheDir,
		p.DataDir,
		p.StateDir,
		p.LogsDir,
		p.RunDir,
		p.ConfigDir,
		filepath.Join(p.ConfigDir, "process-compose"),
		p.GeneratedDir,
		p.XDGCacheDir,
		p.XDGStateDir,
		p.ProcessComposeHome,
		p.ServiceHome,
		p.PythonStateDir,
		filepath.Dir(p.AuthServicePIDFile),
		p.AuthServiceStateDir,
		filepath.Dir(p.AuthServiceDBPath),
		p.CoreStateDir,
		filepath.Dir(p.CoreDBPath),
		filepath.Dir(p.LazyLLMDBPath),
		p.UploadRoot,
		p.LazyLLMTempDir,
		p.OCRCacheDir,
		p.SubagentDataDir,
		p.TracesDir,
		p.LazyLLMHome,
		p.EvoDataDir,
		filepath.Dir(p.ScanDBPath),
		p.ScanControlPlaneStateDir,
		p.ScanControlPlaneTempDir,
		p.FileWatcherBaseRoot,
		p.AlgorithmHome,
		p.AlgorithmPIDDir,
		p.MilvusLiteDBPath,
	}
	if !p.buildRootIsBundledResources() {
		dirs = append(dirs,
			p.BinDir,
			p.DepsDir,
			p.PythonRuntimeDir,
			p.NodeRuntimeDir,
			p.AuthServiceVenvDir,
			filepath.Dir(p.AlgorithmVenv),
			p.FrontendNodeModules,
		)
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
