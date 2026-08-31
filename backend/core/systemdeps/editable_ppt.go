package systemdeps

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	editablePPTBundlePathEnv = "LAZYMIND_EDITABLE_PPT_BUNDLE_PATH"

	modelScopeEditablePPTBaseURL = "https://modelscope.cn/datasets/CarlosShaoting/lazymind-cst/resolve/master/"
	windowsX64EditablePPTSHA     = "39af59d6c4126d93ca23a475b0a72266823ace2f74e45e0def9cf33b8e0272b3"
	darwinArm64EditablePPTSHA    = "ada8928192074ad9b586653aff6cf2d650f4c188f6dab547e1d2d23008935591"
	linuxX64EditablePPTSHA       = "1e2135cc4345b91aece29a4c623030f445020527819526970afbc7a1d5faf675"
)

type editablePPTBundleConfig struct {
	URL            string
	SHA256         string
	FallbackURL    string
	FallbackSHA256 string
	Supported      bool
}

type EditablePPTStatus struct {
	Installed        bool     `json:"installed"`
	InstallDir       string   `json:"installDir,omitempty"`
	ChromiumPath     string   `json:"chromiumPath,omitempty"`
	AffectedFeatures []string `json:"affectedFeatures"`
	RuntimeLocal     bool     `json:"runtimeLocal"`
	InstallSupported bool     `json:"installSupported"`
	Message          string   `json:"message,omitempty"`
}

type editablePPTBundleManifest struct {
	Platform string `json:"platform"`
	Arch     string `json:"arch"`
}

func DetectEditablePPT(runtimeRoot string) (EditablePPTStatus, error) {
	if !IsLocalRuntime() {
		return EditablePPTStatus{
			Installed: true, AffectedFeatures: []string{"editable_pptx_export"},
		}, nil
	}
	cfg, err := LoadConfig(runtimeRoot)
	if err != nil {
		return EditablePPTStatus{}, err
	}
	status := buildEditablePPTStatus(cfg.EditablePPT)
	if status.Installed {
		// Heal stale/broken absolute links after install without requiring restart.
		_ = EnsureEditablePPTNodeModulesLink(status.InstallDir)
	}
	return status, nil
}

func buildEditablePPTStatus(cfg EditablePPTConfig) EditablePPTStatus {
	installDir := filepath.Clean(cfg.InstalledDir)
	status := EditablePPTStatus{
		InstallDir:       installDir,
		AffectedFeatures: []string{"editable_pptx_export"},
		RuntimeLocal:     true,
		InstallSupported: resolveNodeExecutable() != "" && editablePPTBundleConfigured(),
	}
	markers := []string{
		filepath.Join(installDir, "bundle-manifest.json"),
		filepath.Join(installDir, "node_modules", "pptxgenjs"),
		filepath.Join(installDir, "node_modules", "playwright"),
		filepath.Join(installDir, "node_modules", "echarts"),
	}
	for _, marker := range markers {
		if _, err := os.Stat(marker); err != nil {
			switch {
			case resolveNodeExecutable() == "":
				status.Message = "Desktop Electron Node runtime is unavailable"
			case !editablePPTBundleConfigured():
				status.Message = fmt.Sprintf("Editable PPTX dependency bundle is not configured for %s/%s", runtime.GOOS, runtime.GOARCH)
			default:
				status.Message = "Editable PPTX exporter is not installed"
			}
			return status
		}
	}
	if err := validateEditablePPTBundleManifest(installDir); err != nil {
		status.Message = err.Error()
		return status
	}
	chromiumPath, err := playwrightChromiumPath(installDir)
	if err != nil || chromiumPath == "" {
		status.Message = "Playwright Chromium is not installed"
		return status
	}
	if _, err := os.Stat(chromiumPath); err != nil {
		status.Message = "Playwright Chromium executable was not found"
		return status
	}
	if err := validateEditablePPTChromium(chromiumPath, installDir); err != nil {
		status.Message = err.Error()
		return status
	}
	status.Installed = true
	status.ChromiumPath = chromiumPath
	return status
}

func validateEditablePPTBundleManifest(installDir string) error {
	raw, err := os.ReadFile(filepath.Join(installDir, "bundle-manifest.json"))
	if err != nil {
		return err
	}
	var manifest editablePPTBundleManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return fmt.Errorf("invalid editable PPTX bundle manifest: %w", err)
	}
	expectedArch := runtime.GOARCH
	if expectedArch == "amd64" {
		expectedArch = "x64"
	}
	if manifest.Platform != runtime.GOOS || manifest.Arch != expectedArch {
		return fmt.Errorf(
			"editable PPTX dependency bundle targets %s/%s, current runtime is %s/%s",
			manifest.Platform, manifest.Arch, runtime.GOOS, expectedArch,
		)
	}
	return nil
}

func InstallEditablePPT(ctx context.Context, runtimeRoot string) (EditablePPTStatus, error) {
	if !IsLocalRuntime() {
		return EditablePPTStatus{}, errors.New("editable PPTX dependency install is only supported in local/desktop runtime")
	}
	if resolveNodeExecutable() == "" {
		return EditablePPTStatus{}, errors.New("Desktop Electron Node runtime is unavailable")
	}
	if !editablePPTBundleConfigured() {
		return EditablePPTStatus{}, fmt.Errorf("editable PPTX dependency bundle is not configured for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	depsDir := filepath.Join(runtimeRoot, "deps")
	if err := os.MkdirAll(depsDir, 0o755); err != nil {
		return EditablePPTStatus{}, err
	}
	stagingDir, err := os.MkdirTemp(depsDir, ".editable-ppt-install-*")
	if err != nil {
		return EditablePPTStatus{}, err
	}
	defer os.RemoveAll(stagingDir)

	archivePath := filepath.Join(stagingDir, "editable-ppt.zip")
	if err := acquireEditablePPTBundle(ctx, archivePath); err != nil {
		return EditablePPTStatus{}, err
	}
	extractedDir := filepath.Join(stagingDir, "payload")
	if err := os.MkdirAll(extractedDir, 0o755); err != nil {
		return EditablePPTStatus{}, err
	}
	if err := extractEditablePPTZip(archivePath, extractedDir); err != nil {
		return EditablePPTStatus{}, fmt.Errorf("extract editable PPTX dependency bundle: %w", err)
	}

	stagedStatus := buildEditablePPTStatus(EditablePPTConfig{InstalledDir: extractedDir})
	if !stagedStatus.Installed {
		return stagedStatus, fmt.Errorf("editable PPTX dependency validation failed: %s", stagedStatus.Message)
	}
	installDir := EditablePPTInstallDir(runtimeRoot)
	if err := os.RemoveAll(installDir); err != nil {
		return EditablePPTStatus{}, err
	}
	if err := os.Rename(extractedDir, installDir); err != nil {
		return EditablePPTStatus{}, err
	}

	cfg, err := LoadConfig(runtimeRoot)
	if err != nil {
		return EditablePPTStatus{}, err
	}
	cfg.EditablePPT.InstalledDir = installDir
	if err := SaveConfig(runtimeRoot, cfg); err != nil {
		return EditablePPTStatus{}, err
	}
	status := buildEditablePPTStatus(cfg.EditablePPT)
	if !status.Installed {
		return status, errors.New("editable PPTX install completed but Chromium was not detected")
	}
	// Rebuild the exporter's node_modules link immediately so export works
	// without requiring a full local-up restart.
	if err := EnsureEditablePPTNodeModulesLink(installDir); err != nil {
		status.Message = strings.TrimSpace(status.Message + "; node_modules link: " + err.Error())
	}
	return status, nil
}

// EnsureEditablePPTNodeModulesLink points the repo exporter's node_modules at
// the installed deps ZIP. Safe to call when deps are missing (returns error).
func EnsureEditablePPTNodeModulesLink(installDir string) error {
	exportSrc := editablePPTExporterSourceDir()
	if exportSrc == "" {
		return errors.New("LAZYMIND_PPT_EXPORT_CLI / LAZYMIND_WORKFLOWS_DIR is not set")
	}
	target := filepath.Join(installDir, "node_modules")
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		if err != nil {
			return err
		}
		return fmt.Errorf("editable PPT deps node_modules missing: %s", target)
	}
	link := filepath.Join(exportSrc, "node_modules")
	if current, ok := readDirectorySymlink(link); ok {
		if filepath.Clean(current) == filepath.Clean(target) {
			return nil
		}
		_ = os.Remove(link)
	} else if info, err := os.Lstat(link); err == nil {
		if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			// Real directory from a local npm install — leave alone.
			return nil
		}
		_ = os.Remove(link)
	}
	if err := createEditablePPTDirectoryLink(target, link); err != nil {
		return fmt.Errorf("create exporter node_modules link: %w", err)
	}
	return nil
}

func createEditablePPTDirectoryLink(target, link string) error {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", link, target)
		configureNodeCommand(cmd)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("mklink /J failed: %w (%s)", err, strings.TrimSpace(string(output)))
		}
		return nil
	}
	return os.Symlink(target, link)
}

func editablePPTExporterSourceDir() string {
	if cli := strings.TrimSpace(os.Getenv("LAZYMIND_PPT_EXPORT_CLI")); cli != "" {
		return filepath.Dir(cli)
	}
	if workflows := strings.TrimSpace(os.Getenv("LAZYMIND_WORKFLOWS_DIR")); workflows != "" {
		return filepath.Join(workflows, "ppt-workflow", "runtime", "scripts", "export_pptx")
	}
	return ""
}

func readDirectorySymlink(path string) (string, bool) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return "", false
	}
	target, err := os.Readlink(path)
	if err != nil {
		return "", false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	return filepath.Clean(target), true
}

func editablePPTBundleConfigured() bool {
	if strings.TrimSpace(os.Getenv(editablePPTBundlePathEnv)) != "" {
		return true
	}
	cfg := currentEditablePPTBundleConfig()
	return cfg.Supported && cfg.URL != "" && cfg.SHA256 != ""
}

func acquireEditablePPTBundle(ctx context.Context, destination string) error {
	if sourcePath := strings.TrimSpace(os.Getenv(editablePPTBundlePathEnv)); sourcePath != "" {
		return copyFile(sourcePath, destination)
	}
	cfg := currentEditablePPTBundleConfig()
	if !cfg.Supported {
		return fmt.Errorf("editable PPTX dependency bundle is not supported on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return acquireEditablePPTBundleFromConfig(ctx, destination, cfg)
}

func acquireEditablePPTBundleFromConfig(ctx context.Context, destination string, cfg editablePPTBundleConfig) error {
	primaryErr := downloadEditablePPTBundle(ctx, cfg.URL, destination, cfg.SHA256)
	if primaryErr == nil {
		return nil
	}
	if cfg.FallbackURL == "" || cfg.FallbackURL == cfg.URL {
		return primaryErr
	}
	if fallbackErr := downloadEditablePPTBundle(ctx, cfg.FallbackURL, destination, cfg.FallbackSHA256); fallbackErr != nil {
		return fmt.Errorf("editable PPTX dependency download failed: ModelScope: %v; GitHub fallback: %w", primaryErr, fallbackErr)
	}
	return nil
}

func downloadEditablePPTBundle(ctx context.Context, downloadURL, destination, expectedSHA256 string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return fmt.Errorf("download editable PPTX dependency bundle: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download editable PPTX dependency bundle: HTTP %s", resp.Status)
	}
	if err := writeReaderToFile(resp.Body, destination); err != nil {
		return err
	}
	return verifyEditablePPTBundleChecksum(destination, expectedSHA256)
}

func currentEditablePPTBundleConfig() editablePPTBundleConfig {
	return editablePPTBundleConfigFor(runtime.GOOS, runtime.GOARCH)
}

func editablePPTBundleConfigFor(goos, goarch string) editablePPTBundleConfig {
	var prefix, filename, checksum string
	switch {
	case goos == "windows" && goarch == "amd64":
		prefix = "LAZYMIND_EDITABLE_PPT_WINDOWS_X64"
		filename = "lazymind-editable-ppt-windows-x64-1.0.0.zip"
		checksum = windowsX64EditablePPTSHA
	case goos == "darwin" && goarch == "arm64":
		prefix = "LAZYMIND_EDITABLE_PPT_DARWIN_ARM64"
		filename = "lazymind-editable-ppt-darwin-arm64-1.0.0.zip"
		checksum = darwinArm64EditablePPTSHA
	case goos == "linux" && goarch == "amd64":
		prefix = "LAZYMIND_EDITABLE_PPT_LINUX_X64"
		filename = "lazymind-editable-ppt-linux-x64-1.0.0.zip"
		checksum = linuxX64EditablePPTSHA
	default:
		return editablePPTBundleConfig{}
	}
	return editablePPTBundleConfig{
		URL:            modelScopeEditablePPTBaseURL + filename,
		SHA256:         checksum,
		FallbackURL:    strings.TrimSpace(os.Getenv(prefix + "_URL")),
		FallbackSHA256: strings.ToLower(strings.TrimSpace(os.Getenv(prefix + "_SHA256"))),
		Supported:      true,
	}
}

func verifyEditablePPTBundleChecksum(path, expected string) error {
	if expected == "" {
		return errors.New("editable PPTX dependency bundle SHA256 is not configured")
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
	if actual != expected {
		return fmt.Errorf("editable PPTX dependency bundle checksum mismatch: got %s", actual)
	}
	return nil
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	return writeReaderToFile(input, destination)
}

func writeReaderToFile(input io.Reader, destination string) error {
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func extractEditablePPTZip(archivePath, destination string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, item := range reader.File {
		target, err := safeEditablePPTArchiveTarget(destination, item.Name)
		if err != nil {
			return err
		}
		if item.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if item.Mode()&os.ModeSymlink != 0 {
			input, err := item.Open()
			if err != nil {
				return err
			}
			linkRaw, readErr := io.ReadAll(io.LimitReader(input, 4096))
			input.Close()
			if readErr != nil {
				return readErr
			}
			linkTarget := string(linkRaw)
			if filepath.IsAbs(linkTarget) {
				return fmt.Errorf("unsafe absolute symlink in archive: %s", item.Name)
			}
			resolvedTarget := filepath.Clean(filepath.Join(filepath.Dir(target), linkTarget))
			resolvedRelative, relErr := filepath.Rel(destination, resolvedTarget)
			if relErr != nil || resolvedRelative == ".." || strings.HasPrefix(resolvedRelative, ".."+string(filepath.Separator)) {
				return fmt.Errorf("unsafe symlink target in archive: %s", item.Name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(linkTarget, target); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		input, err := item.Open()
		if err != nil {
			return err
		}
		mode := item.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		input.Close()
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func safeEditablePPTArchiveTarget(root, name string) (string, error) {
	target := filepath.Join(root, filepath.FromSlash(name))
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe archive path: %s", name)
	}
	return target, nil
}

func playwrightChromiumPath(installDir string) (string, error) {
	node := resolveNodeExecutable()
	if node == "" {
		return "", errors.New("Desktop Electron Node runtime is unavailable")
	}
	cmd := exec.Command(node, "-e", "process.stdout.write(require('playwright').chromium.executablePath())")
	cmd.Dir = installDir
	cmd.Env = editablePPTProcessEnv(installDir)
	configureNodeCommand(cmd)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func validateEditablePPTChromium(chromiumPath, installDir string) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, chromiumPath, "--version")
	cmd.Env = editablePPTProcessEnv(installDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("Playwright Chromium cannot start: %s", detail)
	}
	return nil
}

func editablePPTProcessEnv(installDir string) []string {
	env := append(os.Environ(), "PLAYWRIGHT_BROWSERS_PATH="+filepath.Join(installDir, "browsers"))
	if runtime.GOOS != "linux" {
		return env
	}
	libraryDirs := []string{
		filepath.Join(installDir, "linux-sysroot", "usr", "lib", "x86_64-linux-gnu"),
		filepath.Join(installDir, "linux-sysroot", "lib", "x86_64-linux-gnu"),
	}
	existing := make([]string, 0, len(libraryDirs)+1)
	for _, dir := range libraryDirs {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			existing = append(existing, dir)
		}
	}
	if current := strings.TrimSpace(os.Getenv("LD_LIBRARY_PATH")); current != "" {
		existing = append(existing, current)
	}
	if len(existing) > 0 {
		env = append(env, "LD_LIBRARY_PATH="+strings.Join(existing, string(os.PathListSeparator)))
	}
	return env
}

func resolveNodeExecutable() string {
	configured := strings.TrimSpace(os.Getenv("LAZYMIND_NODE_EXECUTABLE"))
	if configured != "" {
		if info, err := os.Stat(configured); err == nil && !info.IsDir() {
			return configured
		}
		if resolved, err := exec.LookPath(configured); err == nil {
			return resolved
		}
		return ""
	}
	resolved, _ := exec.LookPath("node")
	return resolved
}

func configureNodeCommand(cmd *exec.Cmd) {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("LAZYMIND_NODE_RUN_AS_NODE")), "true") {
		cmd.Env = append(cmd.Env, "ELECTRON_RUN_AS_NODE=1")
	}
}
