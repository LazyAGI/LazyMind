package systemdeps

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const configFileName = "system-dependencies.json"

// FFmpegSource selects how ffmpeg/ffprobe are resolved for local runtime processes.
type FFmpegSource string

const (
	FFmpegSourceAuto    FFmpegSource = "auto"
	FFmpegSourceCustom  FFmpegSource = "custom"
	FFmpegSourceBundled FFmpegSource = "bundled"
)

type FFmpegConfig struct {
	Source        FFmpegSource `json:"source"`
	CustomPath    string       `json:"customPath,omitempty"`
	BundledBinDir string       `json:"bundledBinDir,omitempty"`
}

type EditablePPTConfig struct {
	InstalledDir string `json:"installedDir,omitempty"`
}

type DependenciesConfig struct {
	FFmpeg      FFmpegConfig      `json:"ffmpeg"`
	EditablePPT EditablePPTConfig `json:"editablePpt"`
}

func RuntimeRootFromEnv() (string, error) {
	if value := strings.TrimSpace(os.Getenv("LAZYMIND_RUNTIME_ROOT")); value != "" {
		return filepath.Clean(value), nil
	}
	uploadRoot := strings.TrimSpace(os.Getenv("LAZYMIND_UPLOAD_ROOT"))
	if uploadRoot == "" {
		return "", errors.New("runtime root is not configured")
	}
	// .../data/core/uploads -> runtime root
	return filepath.Clean(filepath.Join(uploadRoot, "..", "..", "..")), nil
}

func ConfigPath(runtimeRoot string) string {
	return filepath.Join(runtimeRoot, "config", configFileName)
}

func BundledFFmpegBinDir(runtimeRoot string) string {
	return filepath.Join(runtimeRoot, "deps", "ffmpeg", "bin")
}

func EditablePPTInstallDir(runtimeRoot string) string {
	return filepath.Join(runtimeRoot, "deps", "editable-ppt")
}

func LoadConfig(runtimeRoot string) (DependenciesConfig, error) {
	path := ConfigPath(runtimeRoot)
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return defaultConfig(runtimeRoot), nil
		}
		return DependenciesConfig{}, err
	}
	var cfg DependenciesConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return DependenciesConfig{}, err
	}
	cfg.FFmpeg = normalizeFFmpegConfig(cfg.FFmpeg, runtimeRoot)
	cfg.EditablePPT = normalizeEditablePPTConfig(cfg.EditablePPT, runtimeRoot)
	return cfg, nil
}

func SaveConfig(runtimeRoot string, cfg DependenciesConfig) error {
	cfg.FFmpeg = normalizeFFmpegConfig(cfg.FFmpeg, runtimeRoot)
	cfg.EditablePPT = normalizeEditablePPTConfig(cfg.EditablePPT, runtimeRoot)
	if err := os.MkdirAll(filepath.Join(runtimeRoot, "config"), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	path := ConfigPath(runtimeRoot)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(payload, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func defaultConfig(runtimeRoot string) DependenciesConfig {
	return DependenciesConfig{
		FFmpeg: FFmpegConfig{
			Source:        FFmpegSourceAuto,
			BundledBinDir: BundledFFmpegBinDir(runtimeRoot),
		},
		EditablePPT: EditablePPTConfig{InstalledDir: EditablePPTInstallDir(runtimeRoot)},
	}
}

func normalizeEditablePPTConfig(cfg EditablePPTConfig, runtimeRoot string) EditablePPTConfig {
	cfg.InstalledDir = strings.TrimSpace(cfg.InstalledDir)
	if cfg.InstalledDir == "" {
		cfg.InstalledDir = EditablePPTInstallDir(runtimeRoot)
	}
	return cfg
}

func normalizeFFmpegConfig(cfg FFmpegConfig, runtimeRoot string) FFmpegConfig {
	switch cfg.Source {
	case FFmpegSourceCustom, FFmpegSourceBundled, FFmpegSourceAuto:
	default:
		cfg.Source = FFmpegSourceAuto
	}
	cfg.CustomPath = strings.TrimSpace(cfg.CustomPath)
	cfg.BundledBinDir = strings.TrimSpace(cfg.BundledBinDir)
	if cfg.BundledBinDir == "" {
		cfg.BundledBinDir = BundledFFmpegBinDir(runtimeRoot)
	}
	return cfg
}

func IsLocalRuntime() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("LAZYMIND_RUNTIME_MODE")), "local")
}
