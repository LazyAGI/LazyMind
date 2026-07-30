package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type ffmpegDependencyConfig struct {
	Source        string `json:"source"`
	CustomPath    string `json:"customPath,omitempty"`
	BundledBinDir string `json:"bundledBinDir,omitempty"`
}

type systemDependenciesConfig struct {
	FFmpeg ffmpegDependencyConfig `json:"ffmpeg"`
}

func ffmpegConfigPath(configDir string) string {
	return filepath.Join(configDir, "system-dependencies.json")
}

func defaultBundledFFmpegBinDir(runtimeRoot string) string {
	return filepath.Join(runtimeRoot, "deps", "ffmpeg", "bin")
}

func loadFFmpegBinDirForRuntime(paths RuntimePaths) string {
	path := ffmpegConfigPath(paths.ConfigDir)
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return firstExistingFFmpegBinDir([]string{defaultBundledFFmpegBinDir(paths.RuntimeRoot)})
		}
		return ""
	}
	var cfg systemDependenciesConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return ""
	}
	bundledDir := strings.TrimSpace(cfg.FFmpeg.BundledBinDir)
	if bundledDir == "" {
		bundledDir = defaultBundledFFmpegBinDir(paths.RuntimeRoot)
	}
	switch strings.TrimSpace(cfg.FFmpeg.Source) {
	case "custom":
		customPath := strings.TrimSpace(cfg.FFmpeg.CustomPath)
		if customPath == "" {
			return ""
		}
		if info, err := os.Stat(customPath); err == nil && info.IsDir() {
			return firstExistingFFmpegBinDir([]string{customPath})
		}
		if !fileExists(customPath) {
			return ""
		}
		return filepath.Dir(customPath)
	case "bundled":
		return firstExistingFFmpegBinDir([]string{bundledDir})
	default:
		if dir := firstExistingFFmpegBinDir([]string{bundledDir}); dir != "" {
			return dir
		}
		return ""
	}
}

func firstExistingFFmpegBinDir(dirs []string) string {
	ffmpegName := "ffmpeg"
	ffprobeName := "ffprobe"
	if os.PathSeparator == '\\' {
		ffmpegName = "ffmpeg.exe"
		ffprobeName = "ffprobe.exe"
	}
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		ffmpegPath := filepath.Join(dir, ffmpegName)
		ffprobePath := filepath.Join(dir, ffprobeName)
		if fileExists(ffmpegPath) && fileExists(ffprobePath) {
			return dir
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func prependPathEnv(env []string, dir string) []string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return env
	}
	current := strings.TrimSpace(os.Getenv("PATH"))
	for _, item := range env {
		if strings.HasPrefix(item, "PATH=") {
			current = strings.TrimPrefix(item, "PATH=")
			break
		}
	}
	merged := dir + string(os.PathListSeparator) + current
	return withEnvOverrides(env, "PATH="+merged)
}
