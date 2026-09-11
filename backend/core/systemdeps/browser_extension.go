package systemdeps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	browserExtensionBundlePathEnv = "LAZYMIND_BROWSER_EXTENSION_BUNDLE_PATH"
	browserExtensionBundleURLEnv  = "LAZYMIND_BROWSER_EXTENSION_BUNDLE_URL"
	browserExtensionBundleSHAEnv  = "LAZYMIND_BROWSER_EXTENSION_BUNDLE_SHA256"
	browserExtensionSourceDirEnv  = "LAZYMIND_BROWSER_EXTENSION_SOURCE_DIR"
)

type browserExtensionBundleConfig struct {
	ArchivePath string
	URL         string
	SHA256      string
	SourceDir   string
}

type BrowserExtensionStatus struct {
	Installed               bool                     `json:"installed"`
	InstallDir              string                   `json:"installDir,omitempty"`
	ManifestPath            string                   `json:"manifestPath,omitempty"`
	Version                 string                   `json:"version,omitempty"`
	AvailableVersion        string                   `json:"availableVersion,omitempty"`
	UpdateAvailable         bool                     `json:"updateAvailable"`
	AffectedFeatures        []string                 `json:"affectedFeatures"`
	RuntimeLocal            bool                     `json:"runtimeLocal"`
	InstallSupported        bool                     `json:"installSupported"`
	BrowserApprovalRequired bool                     `json:"browserApprovalRequired"`
	BrowserSettingsURL      string                   `json:"browserSettingsUrl"`
	SupportedBrowsers       []BrowserExtensionTarget `json:"supportedBrowsers"`
	Message                 string                   `json:"message,omitempty"`
}

type BrowserExtensionTarget struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	SettingsURL string `json:"settingsUrl"`
}

type browserExtensionManifest struct {
	ManifestVersion int    `json:"manifest_version"`
	Name            string `json:"name"`
	Version         string `json:"version"`
	Background      struct {
		ServiceWorker string `json:"service_worker"`
	} `json:"background"`
	Action struct {
		DefaultPopup string `json:"default_popup"`
	} `json:"action"`
}

func DetectBrowserExtension(runtimeRoot string) (BrowserExtensionStatus, error) {
	if !IsLocalRuntime() {
		return BrowserExtensionStatus{
			Installed: true, AffectedFeatures: browserExtensionFeatures(),
			BrowserApprovalRequired: true, BrowserSettingsURL: "chrome://extensions",
			SupportedBrowsers: supportedBrowserExtensionTargets(),
		}, nil
	}
	cfg, err := LoadConfig(runtimeRoot)
	if err != nil {
		return BrowserExtensionStatus{}, err
	}
	return buildBrowserExtensionStatus(cfg.BrowserExtension), nil
}

func buildBrowserExtensionStatus(cfg BrowserExtensionConfig) BrowserExtensionStatus {
	installDir := filepath.Clean(cfg.InstalledDir)
	bundle := currentBrowserExtensionBundleConfig()
	status := BrowserExtensionStatus{
		InstallDir: installDir, ManifestPath: filepath.Join(installDir, "manifest.json"),
		AffectedFeatures: browserExtensionFeatures(), RuntimeLocal: true,
		InstallSupported: bundle.configured(), BrowserApprovalRequired: true,
		BrowserSettingsURL: "chrome://extensions", SupportedBrowsers: supportedBrowserExtensionTargets(),
	}
	status.AvailableVersion = browserExtensionAvailableVersion(bundle)
	manifest, err := validateBrowserExtension(installDir)
	if err != nil {
		if !status.InstallSupported {
			status.Message = "Browser extension dependency bundle source is not configured"
		} else {
			status.Message = "LazyMind Browser extension is not installed: " + err.Error()
		}
		return status
	}
	status.Installed = true
	status.Version = manifest.Version
	status.UpdateAvailable = status.AvailableVersion != "" && status.AvailableVersion != status.Version
	status.Message = "Load this directory from chrome://extensions or edge://extensions and confirm browser permissions"
	return status
}

func InstallBrowserExtension(ctx context.Context, runtimeRoot string) (BrowserExtensionStatus, error) {
	if !IsLocalRuntime() {
		return BrowserExtensionStatus{}, errors.New("browser extension dependency install is only supported in local/desktop runtime")
	}
	bundle := currentBrowserExtensionBundleConfig()
	if !bundle.configured() {
		return BrowserExtensionStatus{}, errors.New("browser extension dependency bundle source is not configured")
	}

	depsDir := filepath.Join(runtimeRoot, "deps")
	if err := os.MkdirAll(depsDir, 0o755); err != nil {
		return BrowserExtensionStatus{}, err
	}
	stagingDir, err := os.MkdirTemp(depsDir, ".browser-extension-install-*")
	if err != nil {
		return BrowserExtensionStatus{}, err
	}
	defer os.RemoveAll(stagingDir)

	payloadDir := filepath.Join(stagingDir, "payload")
	if strings.TrimSpace(bundle.URL) == "" && strings.TrimSpace(bundle.ArchivePath) == "" {
		if err := copyBrowserExtensionSource(bundle.SourceDir, payloadDir); err != nil {
			return BrowserExtensionStatus{}, fmt.Errorf("copy browser extension dependency source: %w", err)
		}
	} else {
		archivePath := filepath.Join(stagingDir, "browser-extension.zip")
		if err := acquireBrowserExtensionBundle(ctx, archivePath, bundle); err != nil {
			return BrowserExtensionStatus{}, err
		}
		extractedDir := filepath.Join(stagingDir, "extracted")
		if err := os.MkdirAll(extractedDir, 0o755); err != nil {
			return BrowserExtensionStatus{}, err
		}
		// Reuse the hardened ZIP path/symlink validation used by editable-ppt.
		if err := extractEditablePPTZip(archivePath, extractedDir); err != nil {
			return BrowserExtensionStatus{}, fmt.Errorf("extract browser extension dependency bundle: %w", err)
		}
		payloadDir, err = locateBrowserExtensionRoot(extractedDir)
		if err != nil {
			return BrowserExtensionStatus{}, err
		}
	}

	if _, err := validateBrowserExtension(payloadDir); err != nil {
		return BrowserExtensionStatus{}, fmt.Errorf("browser extension dependency validation failed: %w", err)
	}
	installDir := BrowserExtensionInstallDir(runtimeRoot)
	if err := replaceBrowserExtensionDirectory(payloadDir, installDir); err != nil {
		return BrowserExtensionStatus{}, err
	}

	cfg, err := LoadConfig(runtimeRoot)
	if err != nil {
		return BrowserExtensionStatus{}, err
	}
	cfg.BrowserExtension.InstalledDir = installDir
	if err := SaveConfig(runtimeRoot, cfg); err != nil {
		return BrowserExtensionStatus{}, err
	}
	status := buildBrowserExtensionStatus(cfg.BrowserExtension)
	if !status.Installed {
		return status, errors.New("browser extension dependency install completed but manifest was not detected")
	}
	return status, nil
}

func browserExtensionFeatures() []string {
	return []string{"browser_current_page_capture", "browser_managed_page_control"}
}

func supportedBrowserExtensionTargets() []BrowserExtensionTarget {
	return []BrowserExtensionTarget{
		{ID: "chrome", Name: "Google Chrome", SettingsURL: "chrome://extensions"},
		{ID: "edge", Name: "Microsoft Edge", SettingsURL: "edge://extensions"},
	}
}

func currentBrowserExtensionBundleConfig() browserExtensionBundleConfig {
	return browserExtensionBundleConfig{
		ArchivePath: strings.TrimSpace(os.Getenv(browserExtensionBundlePathEnv)),
		URL:         strings.TrimSpace(os.Getenv(browserExtensionBundleURLEnv)),
		SHA256:      strings.ToLower(strings.TrimSpace(os.Getenv(browserExtensionBundleSHAEnv))),
		SourceDir:   strings.TrimSpace(os.Getenv(browserExtensionSourceDirEnv)),
	}
}

func (cfg browserExtensionBundleConfig) configured() bool {
	if cfg.ArchivePath != "" {
		return true
	}
	if cfg.URL != "" || cfg.SHA256 != "" {
		return cfg.URL != "" && cfg.SHA256 != ""
	}
	return cfg.SourceDir != ""
}

func browserExtensionAvailableVersion(cfg browserExtensionBundleConfig) string {
	if cfg.SourceDir == "" || cfg.ArchivePath != "" || cfg.URL != "" {
		return ""
	}
	manifest, err := validateBrowserExtension(cfg.SourceDir)
	if err != nil {
		return ""
	}
	return manifest.Version
}

func acquireBrowserExtensionBundle(ctx context.Context, destination string, cfg browserExtensionBundleConfig) error {
	if cfg.ArchivePath != "" {
		if err := copyFile(cfg.ArchivePath, destination); err != nil {
			return err
		}
		if cfg.SHA256 != "" {
			return verifyBrowserExtensionBundleChecksum(destination, cfg.SHA256)
		}
		return nil
	}
	if cfg.URL == "" || cfg.SHA256 == "" {
		return errors.New("browser extension dependency URL and SHA256 must be configured together")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return fmt.Errorf("download browser extension dependency bundle: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download browser extension dependency bundle: HTTP %s", resp.Status)
	}
	if err := writeReaderToFile(resp.Body, destination); err != nil {
		return err
	}
	return verifyBrowserExtensionBundleChecksum(destination, cfg.SHA256)
}

func verifyBrowserExtensionBundleChecksum(path, expected string) error {
	if expected == "" {
		return errors.New("browser extension dependency bundle SHA256 is not configured")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != strings.ToLower(expected) {
		return fmt.Errorf("browser extension dependency bundle checksum mismatch: got %s", actual)
	}
	return nil
}

func locateBrowserExtensionRoot(extractedDir string) (string, error) {
	if _, err := os.Stat(filepath.Join(extractedDir, "manifest.json")); err == nil {
		return extractedDir, nil
	}
	entries, err := os.ReadDir(extractedDir)
	if err != nil {
		return "", err
	}
	var roots []string
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "__MACOSX" {
			continue
		}
		candidate := filepath.Join(extractedDir, entry.Name())
		if _, err := os.Stat(filepath.Join(candidate, "manifest.json")); err == nil {
			roots = append(roots, candidate)
		}
	}
	if len(roots) != 1 {
		return "", errors.New("browser extension dependency bundle must contain exactly one manifest.json root")
	}
	return roots[0], nil
}

func validateBrowserExtension(root string) (browserExtensionManifest, error) {
	var manifest browserExtensionManifest
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return manifest, fmt.Errorf("invalid browser extension manifest: %w", err)
	}
	if manifest.ManifestVersion != 3 {
		return manifest, fmt.Errorf("browser extension manifest_version is %d, want 3", manifest.ManifestVersion)
	}
	if strings.TrimSpace(manifest.Name) == "" || strings.TrimSpace(manifest.Version) == "" {
		return manifest, errors.New("browser extension manifest name and version are required")
	}
	for _, relative := range []string{manifest.Background.ServiceWorker, manifest.Action.DefaultPopup} {
		if relative == "" {
			return manifest, errors.New("browser extension manifest is missing its service worker or popup")
		}
		target, err := safeEditablePPTArchiveTarget(root, relative)
		if err != nil {
			return manifest, err
		}
		info, err := os.Stat(target)
		if err != nil || !info.Mode().IsRegular() {
			return manifest, fmt.Errorf("browser extension entry file is missing: %s", relative)
		}
	}
	return manifest, nil
}

func copyBrowserExtensionSource(source, destination string) error {
	source = filepath.Clean(strings.TrimSpace(source))
	if source == "." || source == "" {
		return errors.New("browser extension source directory is required")
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("browser extension source contains a symlink: %s", relative)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("browser extension source contains a non-regular file: %s", relative)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			input.Close()
			return err
		}
		copyErr := writeReaderToFile(input, target)
		closeErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func replaceBrowserExtensionDirectory(stagedDir, installDir string) error {
	backupDir := installDir + ".previous"
	if err := os.RemoveAll(backupDir); err != nil {
		return err
	}
	installed := false
	if _, err := os.Stat(installDir); err == nil {
		if err := os.Rename(installDir, backupDir); err != nil {
			return fmt.Errorf("stage existing browser extension dependency: %w", err)
		}
		installed = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(stagedDir, installDir); err != nil {
		if installed {
			_ = os.Rename(backupDir, installDir)
		}
		return fmt.Errorf("activate browser extension dependency: %w", err)
	}
	return os.RemoveAll(backupDir)
}
