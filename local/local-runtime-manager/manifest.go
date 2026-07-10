package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const runtimeManifestFileName = "manifest.json"

type RuntimeManifest struct {
	Version   int                        `json:"version"`
	Profile   string                     `json:"profile"`
	Platform  string                     `json:"platform"`
	Arch      string                     `json:"arch"`
	Binaries  map[string]string          `json:"binaries"`
	Paths     RuntimeManifestPaths       `json:"paths"`
	Services  map[string]ManifestService `json:"services,omitempty"`
	Checksums map[string]string          `json:"checksums,omitempty"`
}

type RuntimeManifestPaths struct {
	AppRoot          string `json:"appRoot"`
	FrontendDist     string `json:"frontendDist"`
	PythonRuntime    string `json:"pythonRuntime"`
	AuthServiceVenv  string `json:"authServiceVenv"`
	AlgorithmVenv    string `json:"algorithmVenv"`
	LocalProxyConfig string `json:"localProxyConfig"`
}

type ManifestService struct {
	HealthPath string `json:"healthPath,omitempty"`
}

func loadRuntimeManifest(resourcesRoot string) (RuntimeManifest, error) {
	path := filepath.Join(resourcesRoot, runtimeManifestFileName)
	body, err := os.ReadFile(path)
	if err != nil {
		return RuntimeManifest{}, err
	}
	var manifest RuntimeManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return RuntimeManifest{}, fmt.Errorf("parse runtime manifest %s: %w", path, err)
	}
	if manifest.Version <= 0 {
		return RuntimeManifest{}, fmt.Errorf("runtime manifest %s has invalid version", path)
	}
	if manifest.Profile != "" && manifest.Profile != "desktop" {
		return RuntimeManifest{}, fmt.Errorf("runtime manifest %s profile = %q, want desktop", path, manifest.Profile)
	}
	if manifest.Platform != "" && manifest.Platform != runtime.GOOS {
		return RuntimeManifest{}, fmt.Errorf("runtime manifest %s platform = %q, want %q", path, manifest.Platform, runtime.GOOS)
	}
	if manifest.Arch != "" && manifest.Arch != runtime.GOARCH {
		return RuntimeManifest{}, fmt.Errorf("runtime manifest %s arch = %q, want %q", path, manifest.Arch, runtime.GOARCH)
	}
	return manifest, nil
}
