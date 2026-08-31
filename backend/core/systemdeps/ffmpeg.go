package systemdeps

import (
	"archive/tar"
	"archive/zip"
	"context"
	"crypto/sha256"
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

	"github.com/ulikunitz/xz"

	appLog "lazymind/core/log"
)

type FFmpegStatus struct {
	Installed        bool     `json:"installed"`
	Source           string   `json:"source"`
	FFmpegPath       string   `json:"ffmpegPath,omitempty"`
	FFprobePath      string   `json:"ffprobePath,omitempty"`
	CustomPath       string   `json:"customPath,omitempty"`
	BundledBinDir    string   `json:"bundledBinDir,omitempty"`
	AffectedFeatures []string `json:"affectedFeatures"`
	RuntimeLocal     bool     `json:"runtimeLocal"`
	InstallSupported bool     `json:"installSupported"`
	Message          string   `json:"message,omitempty"`
}

func DetectFFmpeg(runtimeRoot string) (FFmpegStatus, error) {
	if !IsLocalRuntime() {
		return systemEnabledStatus(), nil
	}
	cfg, err := LoadConfig(runtimeRoot)
	if err != nil {
		return FFmpegStatus{}, err
	}
	return buildStatus(runtimeRoot, cfg), nil
}

func systemEnabledStatus() FFmpegStatus {
	return FFmpegStatus{
		Installed:        true,
		Source:           "system",
		AffectedFeatures: []string{"video_to_gif", "mp4_parsing"},
		RuntimeLocal:     false,
		InstallSupported: false,
	}
}

func buildStatus(runtimeRoot string, cfg DependenciesConfig) FFmpegStatus {
	if !IsLocalRuntime() {
		return systemEnabledStatus()
	}
	status := FFmpegStatus{
		AffectedFeatures: []string{"video_to_gif", "mp4_parsing"},
		RuntimeLocal:     true,
		InstallSupported: true,
		CustomPath:       cfg.FFmpeg.CustomPath,
		BundledBinDir:    cfg.FFmpeg.BundledBinDir,
		Source:           string(cfg.FFmpeg.Source),
	}
	ffmpegPath, ffprobePath, source := resolvePaths(runtimeRoot, cfg.FFmpeg)
	if ffmpegPath != "" && ffprobePath != "" {
		status.Installed = true
		status.FFmpegPath = ffmpegPath
		status.FFprobePath = ffprobePath
		status.Source = source
		return status
	}
	if ffmpegPath != "" {
		status.Message = "ffprobe was not found next to the configured ffmpeg binary"
		return status
	}
	status.Message = "ffmpeg is not installed for the local runtime"
	return status
}

func resolvePaths(runtimeRoot string, cfg FFmpegConfig) (ffmpegPath, ffprobePath, source string) {
	cfg = normalizeFFmpegConfig(cfg, runtimeRoot)
	switch cfg.Source {
	case FFmpegSourceCustom:
		ffmpegPath = resolveCustomFFmpegPath(cfg.CustomPath)
		if ffmpegPath == "" {
			return "", "", string(FFmpegSourceCustom)
		}
		ffprobePath = siblingProbe(ffmpegPath)
		return ffmpegPath, ffprobePath, string(FFmpegSourceCustom)
	case FFmpegSourceBundled:
		ffmpegPath, ffprobePath = binariesInDir(cfg.BundledBinDir)
		return ffmpegPath, ffprobePath, string(FFmpegSourceBundled)
	default:
		// Legacy "auto" configs only look at the LazyMind bundled install.
		// Do not scan PATH — local users must install bundled or pick a custom path.
		ffmpegPath, ffprobePath = binariesInDir(cfg.BundledBinDir)
		if ffmpegPath != "" && ffprobePath != "" {
			return ffmpegPath, ffprobePath, string(FFmpegSourceBundled)
		}
		return "", "", string(FFmpegSourceAuto)
	}
}

func ResolveFFmpegBinDir(runtimeRoot string) string {
	cfg, err := LoadConfig(runtimeRoot)
	if err != nil {
		return ""
	}
	ffmpegPath, ffprobePath, _ := resolvePaths(runtimeRoot, cfg.FFmpeg)
	if ffmpegPath == "" || ffprobePath == "" {
		return ""
	}
	return filepath.Dir(ffmpegPath)
}

func UpdateFFmpegConfig(runtimeRoot string, source FFmpegSource, customPath string) (FFmpegStatus, error) {
	cfg, err := LoadConfig(runtimeRoot)
	if err != nil {
		return FFmpegStatus{}, err
	}
	cfg.FFmpeg.Source = source
	cfg.FFmpeg.CustomPath = strings.TrimSpace(customPath)
	cfg.FFmpeg = normalizeFFmpegConfig(cfg.FFmpeg, runtimeRoot)
	if source == FFmpegSourceCustom {
		execPath := resolveCustomFFmpegPath(cfg.FFmpeg.CustomPath)
		if execPath == "" {
			return FFmpegStatus{}, fmt.Errorf("ffmpeg executable not found: %s", customPath)
		}
		if siblingProbe(execPath) == "" {
			return FFmpegStatus{}, errors.New("ffprobe was not found next to the selected ffmpeg binary")
		}
		cfg.FFmpeg.CustomPath = execPath
	}
	if err := SaveConfig(runtimeRoot, cfg); err != nil {
		return FFmpegStatus{}, err
	}
	return buildStatus(runtimeRoot, cfg), nil
}

func InstallBundledFFmpeg(ctx context.Context, runtimeRoot string) (FFmpegStatus, error) {
	if !IsLocalRuntime() {
		return FFmpegStatus{}, errors.New("bundled ffmpeg install is only supported in local/desktop runtime")
	}
	installStartedAt := time.Now()
	downloads, err := ffmpegDownloads()
	if err != nil {
		return FFmpegStatus{}, err
	}
	appLog.Logger.Info().
		Str("component", "systemdeps.ffmpeg").
		Str("event", "install.started").
		Str("platform", runtime.GOOS).
		Str("arch", runtime.GOARCH).
		Int("archive_count", len(downloads)).
		Str("runtime_root", runtimeRoot).
		Msg("bundled ffmpeg install started")
	depsDir := filepath.Join(runtimeRoot, "deps")
	if err := os.MkdirAll(depsDir, 0o755); err != nil {
		return FFmpegStatus{}, err
	}
	stagingDir, err := os.MkdirTemp(depsDir, ".ffmpeg-install-*")
	if err != nil {
		return FFmpegStatus{}, err
	}
	defer os.RemoveAll(stagingDir)
	stagingBinDir := filepath.Join(stagingDir, "bin")
	if err := os.MkdirAll(stagingBinDir, 0o755); err != nil {
		return FFmpegStatus{}, err
	}

	for index, download := range downloads {
		archivePath := filepath.Join(stagingDir, fmt.Sprintf("download-%d%s", index, download.extension))
		if err := downloadFFmpegArchiveWithFallback(ctx, download, archivePath); err != nil {
			return FFmpegStatus{}, err
		}
		extractStartedAt := time.Now()
		appLog.Logger.Info().
			Str("component", "systemdeps.ffmpeg").
			Str("event", "extract.started").
			Int("archive_index", index).
			Str("format", download.format).
			Msg("ffmpeg archive extraction started")
		if err := extractFFmpegArchive(archivePath, stagingBinDir, download.format); err != nil {
			appLog.Logger.Error().
				Err(err).
				Str("component", "systemdeps.ffmpeg").
				Str("event", "extract.failed").
				Int("archive_index", index).
				Dur("elapsed", time.Since(extractStartedAt)).
				Msg("ffmpeg archive extraction failed")
			return FFmpegStatus{}, fmt.Errorf("extract ffmpeg download failed: %w", err)
		}
		appLog.Logger.Info().
			Str("component", "systemdeps.ffmpeg").
			Str("event", "extract.completed").
			Int("archive_index", index).
			Dur("elapsed", time.Since(extractStartedAt)).
			Msg("ffmpeg archive extraction completed")
		if err := os.Remove(archivePath); err != nil {
			return FFmpegStatus{}, err
		}
	}

	ffmpegPath, ffprobePath := binariesInDir(stagingBinDir)
	if ffmpegPath == "" || ffprobePath == "" {
		return FFmpegStatus{}, errors.New("downloaded ffmpeg archives did not contain ffmpeg and ffprobe binaries")
	}
	if err := validateFFmpegBinaries(ctx, ffmpegPath, ffprobePath); err != nil {
		return FFmpegStatus{}, err
	}

	installDir := filepath.Join(depsDir, "ffmpeg")
	if err := os.RemoveAll(installDir); err != nil {
		return FFmpegStatus{}, err
	}
	if err := os.Rename(stagingDir, installDir); err != nil {
		return FFmpegStatus{}, err
	}
	binDir := filepath.Join(installDir, "bin")

	cfg, err := LoadConfig(runtimeRoot)
	if err != nil {
		return FFmpegStatus{}, err
	}
	cfg.FFmpeg.Source = FFmpegSourceBundled
	cfg.FFmpeg.BundledBinDir = binDir
	cfg.FFmpeg.CustomPath = ""
	if err := SaveConfig(runtimeRoot, cfg); err != nil {
		return FFmpegStatus{}, err
	}
	status := buildStatus(runtimeRoot, cfg)
	if !status.Installed {
		return status, errors.New("ffmpeg install finished but binaries were not detected")
	}
	appLog.Logger.Info().
		Str("component", "systemdeps.ffmpeg").
		Str("event", "install.completed").
		Str("platform", runtime.GOOS).
		Str("arch", runtime.GOARCH).
		Str("ffmpeg", status.FFmpegPath).
		Str("ffprobe", status.FFprobePath).
		Dur("elapsed", time.Since(installStartedAt)).
		Msg("bundled ffmpeg install completed")
	return status, nil
}

func downloadFFmpegArchiveWithFallback(ctx context.Context, download ffmpegDownload, destination string) error {
	primaryErr := downloadFFmpegArchive(ctx, download.url, destination, download.sha256)
	if primaryErr == nil {
		return nil
	}
	if strings.TrimSpace(download.fallbackURL) == "" {
		return primaryErr
	}
	appLog.Logger.Warn().
		Err(primaryErr).
		Str("component", "systemdeps.ffmpeg").
		Str("event", "download.fallback").
		Str("primary_url", download.url).
		Str("fallback_url", download.fallbackURL).
		Msg("primary ffmpeg download failed; trying default upstream")
	if fallbackErr := downloadFFmpegArchive(ctx, download.fallbackURL, destination, ""); fallbackErr != nil {
		return fmt.Errorf("ffmpeg download failed: primary: %v; fallback: %w", primaryErr, fallbackErr)
	}
	return nil
}

func downloadFFmpegArchive(ctx context.Context, downloadURL, destination, expectedSHA256 string) (resultErr error) {
	downloadStartedAt := time.Now()
	var downloadedBytes int64
	appLog.Logger.Info().
		Str("component", "systemdeps.ffmpeg").
		Str("event", "download.started").
		Str("url", downloadURL).
		Bool("sha256_required", strings.TrimSpace(expectedSHA256) != "").
		Msg("ffmpeg archive download started")
	defer func() {
		logEvent := appLog.Logger.Info()
		message := "ffmpeg archive download completed"
		if resultErr != nil {
			logEvent = appLog.Logger.Error().Err(resultErr)
			message = "ffmpeg archive download failed"
		}
		logEvent.
			Str("component", "systemdeps.ffmpeg").
			Str("event", map[bool]string{true: "download.failed", false: "download.completed"}[resultErr != nil]).
			Str("url", downloadURL).
			Int64("bytes", downloadedBytes).
			Dur("elapsed", time.Since(downloadStartedAt)).
			Bool("sha256_verified", resultErr == nil && strings.TrimSpace(expectedSHA256) != "").
			Msg(message)
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("ffmpeg download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ffmpeg download failed: HTTP %s", resp.Status)
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	var copyErr error
	downloadedBytes, copyErr = io.Copy(output, resp.Body)
	closeErr := output.Close()
	if copyErr != nil {
		return fmt.Errorf("write ffmpeg download failed: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("write ffmpeg download failed: %w", closeErr)
	}
	if err := verifyFFmpegArchiveChecksum(destination, expectedSHA256); err != nil {
		return err
	}
	return nil
}

func verifyFFmpegArchiveChecksum(path, expected string) error {
	expected = strings.ToLower(strings.TrimSpace(expected))
	if expected == "" {
		return nil
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
	actual := fmt.Sprintf("%x", hash.Sum(nil))
	if actual != expected {
		return fmt.Errorf("ffmpeg download checksum mismatch: got %s", actual)
	}
	return nil
}

type ffmpegDownload struct {
	url         string
	sha256      string
	fallbackURL string
	format      string
	extension   string
}

const (
	ffmpegArchiveZip   = "zip"
	ffmpegArchiveTarXZ = "tar.xz"

	modelScopeFFmpegBaseURL = "https://modelscope.cn/datasets/CarlosShaoting/lazymind-cst/resolve/master/"
	windowsX64FFmpegSHA     = "99785441c93840109a84967aa9d226c566523ed79d9865570afdac7a12398731"
	darwinX64FFmpegSHA      = "e91df72a1ee7c26606f90dd2dd4dcccc6a75140ff9ea6fdd50faae828b82ba69"
	darwinX64FFprobeSHA     = "399b93f0b9862f69767afa343e90c2f48d7e7958cadbb6deb76a012d0e3b7ce3"
	darwinArm64FFmpegSHA    = "8287a1b2229e05eb41859f073e18e6c52c60a778f2f5e6881070fe51b79407fe"
	darwinArm64FFprobeSHA   = "102a26b8940a053298d9929bfaae71e4b6ef65ba5f19a99a88c433108560741a"
	linuxX64FFmpegSHA       = "baad8e0c2864a7b4045bf44a596376f889e7454988a4e689d2c0f766646c2c22"
)

func ffmpegDownloads() ([]ffmpegDownload, error) {
	return ffmpegDownloadsFor(runtime.GOOS, runtime.GOARCH)
}

func ffmpegDownloadsFor(goos, goarch string) ([]ffmpegDownload, error) {
	const btbNBase = "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/"
	switch goos {
	case "windows":
		switch goarch {
		case "amd64":
			return []ffmpegDownload{{
				url:         modelScopeFFmpegBaseURL + "lazymind-ffmpeg-windows-x64-20260803.zip",
				sha256:      windowsX64FFmpegSHA,
				fallbackURL: btbNBase + "ffmpeg-master-latest-win64-gpl.zip",
				format:      ffmpegArchiveZip,
				extension:   ".zip",
			}}, nil
		case "arm64":
			return []ffmpegDownload{{
				url:       btbNBase + "ffmpeg-master-latest-winarm64-gpl.zip",
				format:    ffmpegArchiveZip,
				extension: ".zip",
			}}, nil
		}
	case "darwin":
		switch goarch {
		case "amd64":
			return []ffmpegDownload{
				{
					url:         modelScopeFFmpegBaseURL + "lazymind-ffmpeg-darwin-x64-8.1.2.zip",
					sha256:      darwinX64FFmpegSHA,
					fallbackURL: "https://evermeet.cx/ffmpeg/getrelease/ffmpeg/zip",
					format:      ffmpegArchiveZip,
					extension:   ".zip",
				},
				{
					url:         modelScopeFFmpegBaseURL + "lazymind-ffprobe-darwin-x64-8.1.2.zip",
					sha256:      darwinX64FFprobeSHA,
					fallbackURL: "https://evermeet.cx/ffmpeg/getrelease/ffprobe/zip",
					format:      ffmpegArchiveZip,
					extension:   ".zip",
				},
			}, nil
		case "arm64":
			return []ffmpegDownload{
				{
					url:         modelScopeFFmpegBaseURL + "lazymind-ffmpeg-darwin-arm64-9.0.1.zip",
					sha256:      darwinArm64FFmpegSHA,
					fallbackURL: "https://ffmpeg.martin-riedl.de/download/macos/arm64/1787073674_9.0.1/ffmpeg.zip",
					format:      ffmpegArchiveZip,
					extension:   ".zip",
				},
				{
					url:         modelScopeFFmpegBaseURL + "lazymind-ffprobe-darwin-arm64-9.0.1.zip",
					sha256:      darwinArm64FFprobeSHA,
					fallbackURL: "https://ffmpeg.martin-riedl.de/download/macos/arm64/1787073674_9.0.1/ffprobe.zip",
					format:      ffmpegArchiveZip,
					extension:   ".zip",
				},
			}, nil
		}
	case "linux":
		switch goarch {
		case "amd64":
			return []ffmpegDownload{{
				url:         modelScopeFFmpegBaseURL + "lazymind-ffmpeg-linux-x64-20260803.tar.xz",
				sha256:      linuxX64FFmpegSHA,
				fallbackURL: btbNBase + "ffmpeg-master-latest-linux64-gpl.tar.xz",
				format:      ffmpegArchiveTarXZ,
				extension:   ".tar.xz",
			}}, nil
		case "arm64":
			return []ffmpegDownload{{
				url:       btbNBase + "ffmpeg-master-latest-linuxarm64-gpl.tar.xz",
				format:    ffmpegArchiveTarXZ,
				extension: ".tar.xz",
			}}, nil
		}
	}
	return nil, fmt.Errorf("bundled ffmpeg install is not supported on %s/%s", goos, goarch)
}

func extractFFmpegArchive(archivePath, binDir, format string) error {
	switch format {
	case ffmpegArchiveZip:
		return extractFFmpegZip(archivePath, binDir)
	case ffmpegArchiveTarXZ:
		return extractFFmpegTarXZ(archivePath, binDir)
	default:
		return fmt.Errorf("unsupported ffmpeg archive format: %s", format)
	}
}

func ffmpegBinaryNames() (string, string) {
	if runtime.GOOS == "windows" {
		return "ffmpeg.exe", "ffprobe.exe"
	}
	return "ffmpeg", "ffprobe"
}

func validateFFmpegBinaries(ctx context.Context, paths ...string) error {
	for _, executablePath := range paths {
		checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		output, err := exec.CommandContext(checkCtx, executablePath, "-version").CombinedOutput()
		cancel()
		if err != nil {
			return fmt.Errorf(
				"downloaded %s binary could not run: %w: %s",
				filepath.Base(executablePath),
				err,
				strings.TrimSpace(string(output)),
			)
		}
	}
	return nil
}

func extractFFmpegTarXZ(archivePath, binDir string) error {
	source, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer source.Close()

	xzReader, err := xz.NewReader(source)
	if err != nil {
		return err
	}
	tarReader := tar.NewReader(xzReader)
	ffmpegName, ffprobeName := ffmpegBinaryNames()
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		base := filepath.Base(header.Name)
		if base != ffmpegName && base != ffprobeName {
			continue
		}
		if err := writeExecutable(tarReader, filepath.Join(binDir, base)); err != nil {
			return err
		}
	}
}

func extractFFmpegZip(zipPath, binDir string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	ffmpegName, ffprobeName := ffmpegBinaryNames()

	for _, file := range reader.File {
		base := filepath.Base(file.Name)
		if base != ffmpegName && base != ffprobeName {
			continue
		}
		if err := writeZipExecutable(file, filepath.Join(binDir, base)); err != nil {
			return err
		}
	}
	return nil
}

func writeZipExecutable(file *zip.File, destPath string) error {
	src, err := file.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	return writeExecutable(src, destPath)
}

func writeExecutable(src io.Reader, destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, src); err != nil {
		return err
	}
	return out.Close()
}

func binariesInDir(dir string) (string, string) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", ""
	}
	ffmpegName := "ffmpeg"
	ffprobeName := "ffprobe"
	if runtime.GOOS == "windows" {
		ffmpegName = "ffmpeg.exe"
		ffprobeName = "ffprobe.exe"
	}
	ffmpegPath := findExecutable(filepath.Join(dir, ffmpegName))
	ffprobePath := findExecutable(filepath.Join(dir, ffprobeName))
	return ffmpegPath, ffprobePath
}

// resolveCustomFFmpegPath accepts either the ffmpeg binary path or a directory
// that contains ffmpeg (+ ffprobe). UI users often paste the bin folder.
func resolveCustomFFmpegPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if execPath := findExecutable(path); execPath != "" {
		return execPath
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return ""
	}
	ffmpegPath, _ := binariesInDir(path)
	return ffmpegPath
}

func siblingProbe(ffmpegPath string) string {
	dir := filepath.Dir(ffmpegPath)
	name := "ffprobe"
	if runtime.GOOS == "windows" {
		name = "ffprobe.exe"
	}
	return findExecutable(filepath.Join(dir, name))
}

func findExecutable(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return ""
	}
	if runtime.GOOS != "windows" {
		if info.Mode()&0o111 == 0 {
			// Allow non-executable bit on some FS; still try running via exec.LookPath semantics.
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}
