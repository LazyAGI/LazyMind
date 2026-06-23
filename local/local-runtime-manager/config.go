package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

const (
	defaultProfileEnvVar      = "LAZYMIND_LOCAL_PROFILE"
	processComposePortEnvVar  = "LAZYMIND_PROCESS_COMPOSE_PORT"
	localUpTimeoutEnvVar      = "LAZYMIND_LOCAL_UP_TIMEOUT"
	localDownTimeoutEnvVar    = "LAZYMIND_LOCAL_DOWN_TIMEOUT"
	defaultProfile            = "linux-browser"
	processComposeVersion     = 1
	defaultProcessComposePort = 19080
	defaultLocalUpTimeout     = 30 * 60
	defaultLocalDownTimeout   = 2 * 60
	stateFileName             = "runtime-state.json"
	composeGeneratedFileName  = "process-compose.generated.yaml"
	tokenFileName             = "pc-token"
	upLockFileName            = "up.lock"
	logFileName               = "docker-stack.log"
	repoComposeFileName       = "docker-compose.yml"
	localComposeOverrideName  = "local/docker-compose.local.yml"
	localProcessComposeBin    = "local/bin/process-compose"
	processComposeServiceName = "docker-stack"
)

type RuntimePaths struct {
	RepoRoot        string
	RuntimeRoot     string
	StateDir        string
	LogsDir         string
	RunDir          string
	GeneratedDir    string
	StateFile       string
	RunDirTokenFile string
	UpLockFile      string
	LogFilePath     string
	GeneratedConfig string
}

type RuntimeConfig struct {
	Profile            string
	RepoRoot           string
	RuntimeRoot        string
	ProcessComposePort int
}

func defaultProfileValue() string {
	if v := os.Getenv(defaultProfileEnvVar); v != "" {
		return v
	}
	return defaultProfile
}

func defaultProcessComposePortValue() int {
	if v := os.Getenv(processComposePortEnvVar); v != "" {
		port, err := strconv.Atoi(v)
		if err == nil && port > 0 && port < 65536 {
			return port
		}
	}
	return defaultProcessComposePort
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
		RepoRoot:        root,
		RuntimeRoot:     runtimeRoot,
		StateDir:        filepath.Join(runtimeRoot, "state"),
		LogsDir:         filepath.Join(runtimeRoot, "logs"),
		RunDir:          filepath.Join(runtimeRoot, "run"),
		GeneratedDir:    filepath.Join(runtimeRoot, "generated"),
		StateFile:       filepath.Join(runtimeRoot, "state", stateFileName),
		RunDirTokenFile: filepath.Join(runtimeRoot, "run", tokenFileName),
		UpLockFile:      filepath.Join(runtimeRoot, "run", upLockFileName),
		LogFilePath:     filepath.Join(runtimeRoot, "logs", logFileName),
		GeneratedConfig: filepath.Join(runtimeRoot, "generated", composeGeneratedFileName),
	}
	return RuntimeConfig{
		Profile:            profile,
		RepoRoot:           p.RepoRoot,
		RuntimeRoot:        runtimeRoot,
		ProcessComposePort: defaultProcessComposePortValue(),
	}, p, nil
}

func (p RuntimePaths) EnsureAllDirs() error {
	dirs := []string{
		p.StateDir,
		p.LogsDir,
		p.RunDir,
		p.GeneratedDir,
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
