package main

import (
	"os"
	"testing"
)

func TestPrependPathEnv(t *testing.T) {
	env := []string{"HOME=/tmp/home", "PATH=/bin:/usr/bin"}
	got := prependPathEnv(env, "/opt/ffmpeg/bin")
	if got[len(got)-1] != "PATH=/opt/ffmpeg/bin:/bin:/usr/bin" {
		t.Fatalf("unexpected PATH entry: %q", got[len(got)-1])
	}
}

func TestLoadFFmpegBinDirForRuntimeBundled(t *testing.T) {
	root := t.TempDir()
	paths := RuntimePaths{
		RuntimeRoot: root,
		ConfigDir:   root + "/config",
	}
	binDir := defaultBundledFFmpegBinDir(root)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(binDir+"/ffmpeg", []byte(""), 0o755); err != nil {
		t.Fatalf("write ffmpeg: %v", err)
	}
	if err := os.WriteFile(binDir+"/ffprobe", []byte(""), 0o755); err != nil {
		t.Fatalf("write ffprobe: %v", err)
	}
	got := loadFFmpegBinDirForRuntime(paths)
	if got != binDir {
		t.Fatalf("bin dir = %q, want %q", got, binDir)
	}
}
