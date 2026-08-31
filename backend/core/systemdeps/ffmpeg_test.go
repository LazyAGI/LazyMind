package systemdeps

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ulikunitz/xz"
)

func TestRuntimeRootFromUploadRoot(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_ROOT", "")
	root := t.TempDir()
	upload := filepath.Join(root, "data", "core", "uploads")
	t.Setenv("LAZYMIND_UPLOAD_ROOT", upload)
	got, err := RuntimeRootFromEnv()
	if err != nil {
		t.Fatalf("RuntimeRootFromEnv: %v", err)
	}
	if got != root {
		t.Fatalf("runtime root = %q, want %q", got, root)
	}
}

func TestSaveAndLoadFFmpegConfig(t *testing.T) {
	root := t.TempDir()
	cfg := defaultConfig(root)
	cfg.FFmpeg.Source = FFmpegSourceCustom
	cfg.FFmpeg.CustomPath = "/tmp/ffmpeg"
	if err := SaveConfig(root, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	loaded, err := LoadConfig(root)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.FFmpeg.Source != FFmpegSourceCustom {
		t.Fatalf("source = %q, want custom", loaded.FFmpeg.Source)
	}
	if loaded.FFmpeg.CustomPath != "/tmp/ffmpeg" {
		t.Fatalf("custom path = %q", loaded.FFmpeg.CustomPath)
	}
	if _, err := os.Stat(ConfigPath(root)); err != nil {
		t.Fatalf("config file missing: %v", err)
	}
}

func TestDetectFFmpegNonLocalDefaultsEnabled(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "cloud")
	status, err := DetectFFmpeg(t.TempDir())
	if err != nil {
		t.Fatalf("DetectFFmpeg: %v", err)
	}
	if !status.Installed {
		t.Fatal("expected non-local runtime to treat ffmpeg as enabled")
	}
	if status.Source != "system" {
		t.Fatalf("source = %q, want system", status.Source)
	}
	if status.InstallSupported {
		t.Fatal("expected installSupported=false outside local runtime")
	}
}

func TestResolveCustomFFmpegPathAcceptsDirectory(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ffmpegName, ffprobeName := ffmpegBinaryNames()
	ffmpegPath := filepath.Join(binDir, ffmpegName)
	ffprobePath := filepath.Join(binDir, ffprobeName)
	for _, path := range []string{ffmpegPath, ffprobePath} {
		if err := os.WriteFile(path, []byte(filepath.Base(path)), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	got := resolveCustomFFmpegPath(binDir)
	absWant, _ := filepath.Abs(ffmpegPath)
	if got != absWant {
		t.Fatalf("resolveCustomFFmpegPath(dir) = %q, want %q", got, absWant)
	}

	status, err := UpdateFFmpegConfig(root, FFmpegSourceCustom, binDir)
	if err != nil {
		t.Fatalf("UpdateFFmpegConfig: %v", err)
	}
	if !status.Installed {
		t.Fatal("expected installed after saving directory path")
	}
	if status.FFmpegPath != absWant {
		t.Fatalf("status.FFmpegPath = %q, want %q", status.FFmpegPath, absWant)
	}
}

func TestExtractFFmpegTarXZ(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "ffmpeg.tar.xz")
	archiveFile, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	xzWriter, err := xz.NewWriter(archiveFile)
	if err != nil {
		t.Fatal(err)
	}
	tarWriter := tar.NewWriter(xzWriter)
	ffmpegName, ffprobeName := ffmpegBinaryNames()
	for _, name := range []string{ffmpegName, ffprobeName} {
		content := []byte(name)
		if err := tarWriter.WriteHeader(&tar.Header{
			Name: "ffmpeg-build/bin/" + name,
			Mode: 0o755,
			Size: int64(len(content)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := xzWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archiveFile.Close(); err != nil {
		t.Fatal(err)
	}

	binDir := filepath.Join(root, "bin")
	if err := extractFFmpegTarXZ(archivePath, binDir); err != nil {
		t.Fatalf("extractFFmpegTarXZ: %v", err)
	}
	for _, name := range []string{ffmpegName, ffprobeName} {
		content, err := os.ReadFile(filepath.Join(binDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != name {
			t.Fatalf("%s content = %q, want %q", name, content, name)
		}
	}
}

func TestFFmpegDownloadsUseModelScopeMirrors(t *testing.T) {
	tests := []struct {
		name      string
		goos      string
		goarch    string
		urls      []string
		checksums []string
		fallbacks []string
	}{
		{
			name:   "windows x64",
			goos:   "windows",
			goarch: "amd64",
			urls: []string{
				modelScopeFFmpegBaseURL + "lazymind-ffmpeg-windows-x64-20260803.zip",
			},
			checksums: []string{windowsX64FFmpegSHA},
			fallbacks: []string{"https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-win64-gpl.zip"},
		},
		{
			name:   "macOS Intel",
			goos:   "darwin",
			goarch: "amd64",
			urls: []string{
				modelScopeFFmpegBaseURL + "lazymind-ffmpeg-darwin-x64-8.1.2.zip",
				modelScopeFFmpegBaseURL + "lazymind-ffprobe-darwin-x64-8.1.2.zip",
			},
			checksums: []string{darwinX64FFmpegSHA, darwinX64FFprobeSHA},
			fallbacks: []string{
				"https://evermeet.cx/ffmpeg/getrelease/ffmpeg/zip",
				"https://evermeet.cx/ffmpeg/getrelease/ffprobe/zip",
			},
		},
		{
			name:   "macOS Apple Silicon native",
			goos:   "darwin",
			goarch: "arm64",
			urls: []string{
				modelScopeFFmpegBaseURL + "lazymind-ffmpeg-darwin-arm64-9.0.1.zip",
				modelScopeFFmpegBaseURL + "lazymind-ffprobe-darwin-arm64-9.0.1.zip",
			},
			checksums: []string{darwinArm64FFmpegSHA, darwinArm64FFprobeSHA},
			fallbacks: []string{
				"https://ffmpeg.martin-riedl.de/download/macos/arm64/1787073674_9.0.1/ffmpeg.zip",
				"https://ffmpeg.martin-riedl.de/download/macos/arm64/1787073674_9.0.1/ffprobe.zip",
			},
		},
		{
			name:   "Linux x64",
			goos:   "linux",
			goarch: "amd64",
			urls: []string{
				modelScopeFFmpegBaseURL + "lazymind-ffmpeg-linux-x64-20260803.tar.xz",
			},
			checksums: []string{linuxX64FFmpegSHA},
			fallbacks: []string{"https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-linux64-gpl.tar.xz"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			downloads, err := ffmpegDownloadsFor(tt.goos, tt.goarch)
			if err != nil {
				t.Fatal(err)
			}
			if len(downloads) != len(tt.urls) {
				t.Fatalf("downloads = %d, want %d", len(downloads), len(tt.urls))
			}
			for index, download := range downloads {
				if download.url != tt.urls[index] {
					t.Fatalf("download[%d].url = %q, want %q", index, download.url, tt.urls[index])
				}
				if download.sha256 != tt.checksums[index] {
					t.Fatalf("download[%d].sha256 = %q, want %q", index, download.sha256, tt.checksums[index])
				}
				if download.fallbackURL != tt.fallbacks[index] {
					t.Fatalf("download[%d].fallbackURL = %q, want %q", index, download.fallbackURL, tt.fallbacks[index])
				}
			}
		})
	}
}

func TestDownloadFFmpegArchiveFallsBackAfterChecksumFailure(t *testing.T) {
	fallbackContent := []byte("fallback archive")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/primary":
			_, _ = w.Write([]byte("corrupt primary archive"))
		case "/fallback":
			_, _ = w.Write(fallbackContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	destination := filepath.Join(t.TempDir(), "ffmpeg.zip")
	expectedPrimarySHA := fmt.Sprintf("%x", sha256.Sum256([]byte("expected primary archive")))
	err := downloadFFmpegArchiveWithFallback(context.Background(), ffmpegDownload{
		url:         server.URL + "/primary",
		sha256:      expectedPrimarySHA,
		fallbackURL: server.URL + "/fallback",
	}, destination)
	if err != nil {
		t.Fatalf("downloadFFmpegArchiveWithFallback: %v", err)
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(fallbackContent) {
		t.Fatalf("downloaded content = %q, want %q", content, fallbackContent)
	}
}

func TestVerifyFFmpegArchiveChecksum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ffmpeg.zip")
	content := []byte("ffmpeg archive")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	expected := fmt.Sprintf("%x", sha256.Sum256(content))
	if err := verifyFFmpegArchiveChecksum(path, expected); err != nil {
		t.Fatalf("verifyFFmpegArchiveChecksum: %v", err)
	}
	if err := verifyFFmpegArchiveChecksum(path, "deadbeef"); err == nil {
		t.Fatal("expected checksum mismatch")
	}
}
