package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrependPathEnv(t *testing.T) {
	pathValue := filepath.Join(string(os.PathSeparator), "bin") + string(os.PathListSeparator) + filepath.Join(string(os.PathSeparator), "usr", "bin")
	env := []string{"HOME=/tmp/home", "PATH=" + pathValue}
	got := prependPathEnv(env, "/opt/ffmpeg/bin")
	want := "PATH=/opt/ffmpeg/bin" + string(os.PathListSeparator) + pathValue
	if got[len(got)-1] != want {
		t.Fatalf("unexpected PATH entry: %q", got[len(got)-1])
	}
}

func TestLoadFFmpegBinDirForRuntimeBundled(t *testing.T) {
	root := t.TempDir()
	paths := RuntimePaths{
		RuntimeRoot: root,
		ConfigDir:   filepath.Join(root, "config"),
	}
	binDir := defaultBundledFFmpegBinDir(root)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(executablePath(binDir, "ffmpeg"), []byte(""), 0o755); err != nil {
		t.Fatalf("write ffmpeg: %v", err)
	}
	if err := os.WriteFile(executablePath(binDir, "ffprobe"), []byte(""), 0o755); err != nil {
		t.Fatalf("write ffprobe: %v", err)
	}
	got := loadFFmpegBinDirForRuntime(paths)
	if got != binDir {
		t.Fatalf("bin dir = %q, want %q", got, binDir)
	}
}
