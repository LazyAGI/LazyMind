package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultProfileEnvVar          = "LAZYMIND_LOCAL_PROFILE"
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
	frontendPortEnvVar            = "LAZYMIND_FRONTEND_PORT"
	defaultProfile                = "linux-browser"
	processComposeVersion         = 1
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
	stateFileName                 = "runtime-state.json"
	composeGeneratedFileName      = "process-compose.generated.yaml"
	tokenFileName                 = "pc-token"
	upLockFileName                = "up.lock"
	logFileName                   = "docker-stack.log"
	localProxyLogFileName         = "local-proxy.log"
	repoComposeFileName           = "docker-compose.yml"
	localComposeOverrideName      = "local/docker-compose.local.yml"
	localProcessComposeBin        = "local/bin/process-compose"
	localProxyConfigName          = "local/local-proxy/configs/cloud-replace-kong.yaml"
	localProxyScriptDirName       = "local/local-proxy/scripts"
	localProxySourceDirName       = "local/local-proxy"
	processComposeServiceName     = "docker-stack"
	localProxyProcessName         = "local-proxy"
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
	LocalProxyBin        string
	LocalProxyConfig     string
	LocalProxyStopScript string
	GeneratedConfig      string
}

type RuntimeConfig struct {
	Profile            string
	RepoRoot           string
	RuntimeRoot        string
	ProcessComposePort int
	FrontendPort       int
	LocalProxy         LocalProxyConfig
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

func defaultProfileValue() string {
	if v := os.Getenv(defaultProfileEnvVar); v != "" {
		return v
	}
	return defaultProfile
}

func defaultProcessComposePortValue() int {
	return envPort(processComposePortEnvVar, defaultProcessComposePort)
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

func envText(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
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
		LocalProxyBin:        filepath.Join(runtimeRoot, "bin", "local-proxy"),
		LocalProxyConfig:     filepath.Join(root, localProxyConfigName),
		LocalProxyStopScript: filepath.Join(root, localProxyScriptDirName, "stop.sh"),
		GeneratedConfig:      filepath.Join(runtimeRoot, "generated", composeGeneratedFileName),
	}
	return RuntimeConfig{
		Profile:            profile,
		RepoRoot:           p.RepoRoot,
		RuntimeRoot:        runtimeRoot,
		ProcessComposePort: defaultProcessComposePortValue(),
		FrontendPort:       envPort(frontendPortEnvVar, defaultFrontendPort),
		LocalProxy: LocalProxyConfig{
			Address:      envText(localProxyAddressEnvVar, defaultLocalProxyAddress),
			Port:         envPort(localProxyPortEnvVar, defaultLocalProxyPort),
			AuthHostPort: envPort(localProxyAuthHostPortEnvVar, defaultLocalProxyAuthHostPort),
			CoreHostPort: envPort(localProxyCoreHostPortEnvVar, defaultLocalProxyCoreHostPort),
			ChatHostPort: envPort(localProxyChatHostPortEnvVar, defaultLocalProxyChatHostPort),
			ScanHostPort: envPort(localProxyScanHostPortEnvVar, defaultLocalProxyScanHostPort),
			EvoHostPort:  envPort(localProxyEvoHostPortEnvVar, defaultLocalProxyEvoHostPort),
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
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
