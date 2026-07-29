package systemdeps

import (
	"archive/tar"
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
