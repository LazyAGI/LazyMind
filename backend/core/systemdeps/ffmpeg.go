package systemdeps

import (
	"archive/tar"
	"archive/zip"
	"context"
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
	downloads, err := ffmpegDownloads()
	if err != nil {
		return FFmpegStatus{}, err
	}
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
		if err := downloadFFmpegArchive(ctx, download.url, archivePath); err != nil {
			return FFmpegStatus{}, err
		}
		if err := extractFFmpegArchive(archivePath, stagingBinDir, download.format); err != nil {
			return FFmpegStatus{}, fmt.Errorf("extract ffmpeg download failed: %w", err)
		}
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
	return status, nil
}

func downloadFFmpegArchive(ctx context.Context, downloadURL, destination string) error {
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
	defer output.Close()
	if _, err := io.Copy(output, resp.Body); err != nil {
		return fmt.Errorf("write ffmpeg download failed: %w", err)
	}
	return output.Close()
}

type ffmpegDownload struct {
	url       string
	format    string
	extension string
}

const (
	ffmpegArchiveZip   = "zip"
	ffmpegArchiveTarXZ = "tar.xz"
)

func ffmpegDownloads() ([]ffmpegDownload, error) {
	const btbNBase = "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/"
	switch runtime.GOOS {
	case "windows":
		switch runtime.GOARCH {
		case "amd64":
			return []ffmpegDownload{{
				url:       btbNBase + "ffmpeg-master-latest-win64-gpl.zip",
				format:    ffmpegArchiveZip,
				extension: ".zip",
			}}, nil
		case "arm64":
			return []ffmpegDownload{{
				url:       btbNBase + "ffmpeg-master-latest-winarm64-gpl.zip",
				format:    ffmpegArchiveZip,
				extension: ".zip",
			}}, nil
		}
	case "darwin":
		if runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64" {
			return []ffmpegDownload{
				{
					url:       "https://evermeet.cx/ffmpeg/getrelease/ffmpeg/zip",
					format:    ffmpegArchiveZip,
					extension: ".zip",
				},
				{
					url:       "https://evermeet.cx/ffmpeg/getrelease/ffprobe/zip",
					format:    ffmpegArchiveZip,
					extension: ".zip",
				},
			}, nil
		}
	case "linux":
		target := ""
		switch runtime.GOARCH {
		case "amd64":
			target = "linux64"
		case "arm64":
			target = "linuxarm64"
		}
		if target != "" {
			return []ffmpegDownload{{
				url:       btbNBase + "ffmpeg-master-latest-" + target + "-gpl.tar.xz",
				format:    ffmpegArchiveTarXZ,
				extension: ".tar.xz",
			}}, nil
		}
	}
	return nil, fmt.Errorf("bundled ffmpeg install is not supported on %s/%s", runtime.GOOS, runtime.GOARCH)
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
