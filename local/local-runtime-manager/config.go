package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	defaultProfileEnvVar      = "LAZYMIND_LOCAL_PROFILE"
	defaultProfile            = "linux-browser"
	processComposeVersion     = 1
	defaultProcessComposePort = 18080
	stateFileName             = "runtime-state.json"
	composeGeneratedFileName  = "process-compose.generated.yaml"
	tokenFileName             = "pc-token"
	logFileName               = "docker-stack.log"
	repoComposeFileName       = "docker-compose.yml"
	localComposeOverrideName  = "local/docker-compose.local.yml"
	processComposeServiceName = "docker-stack"
)

type RuntimePaths struct {
	RepoRoot        string
	RuntimeRoot     string
	StateDir        string
	LogsDir         string
	RunDir          string
	GeneratedDir    string
	DiagnosticsDir  string
	DataDir         string
	CacheDir        string
	StateFile       string
	RunDirTokenFile string
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
		DiagnosticsDir:  filepath.Join(runtimeRoot, "diagnostics"),
		DataDir:         filepath.Join(runtimeRoot, "data"),
		CacheDir:        filepath.Join(runtimeRoot, "cache"),
		StateFile:       filepath.Join(runtimeRoot, "state", stateFileName),
		RunDirTokenFile: filepath.Join(runtimeRoot, "run", tokenFileName),
		LogFilePath:     filepath.Join(runtimeRoot, "logs", logFileName),
		GeneratedConfig: filepath.Join(runtimeRoot, "generated", composeGeneratedFileName),
	}
	return RuntimeConfig{
		Profile:            profile,
		RepoRoot:           p.RepoRoot,
		RuntimeRoot:        runtimeRoot,
		ProcessComposePort: defaultProcessComposePort,
	}, p, nil
}

func (p RuntimePaths) EnsureAllDirs() error {
	dirs := []string{
		p.StateDir,
		p.LogsDir,
		p.RunDir,
		p.GeneratedDir,
		p.DiagnosticsDir,
		p.DataDir,
		p.CacheDir,
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (p RuntimePaths) WritabilityChecks() error {
	for _, d := range []string{
		p.StateDir,
		p.LogsDir,
		p.RunDir,
		p.GeneratedDir,
		p.DiagnosticsDir,
		p.DataDir,
		p.CacheDir,
	} {
		f, err := os.CreateTemp(d, ".lazymind-local-writable-*")
		if err != nil {
			return errors.New(d + " is not writable")
		}
		_ = f.Close()
		_ = os.Remove(f.Name())
	}
	return nil
}
