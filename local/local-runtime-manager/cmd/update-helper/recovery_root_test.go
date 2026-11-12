package main

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestWindowsRecoveryRootUsesLocalAppDataAndNeverRoaming(t *testing.T) {
	root := t.TempDir()
	local := filepath.Join(root, "Local")
	roaming := filepath.Join(root, "Roaming")
	environment := map[string]string{"LOCALAPPDATA": local, "APPDATA": roaming}
	called := false
	getConfig := func() (string, error) { called = true; return roaming, nil }
	got, err := recoveryBaseForPlatform("windows", func(key string) string { return environment[key] }, getConfig)
	if err != nil || got != local || called {
		t.Fatalf("root=%q err=%v used roaming resolver=%v", got, err, called)
	}
	for _, value := range []string{"", "relative-path"} {
		environment["LOCALAPPDATA"] = value
		if got, err := recoveryBaseForPlatform("windows", func(key string) string { return environment[key] }, getConfig); err == nil || got != "" {
			t.Fatalf("invalid local path %q fell back to %q", value, got)
		}
	}
	if called {
		t.Fatal("Windows failure consulted roaming fallback")
	}
}

func TestMacRecoveryRootKeepsApplicationSupportAndPropagatesErrors(t *testing.T) {
	base := filepath.Join(t.TempDir(), "Application Support")
	got, err := recoveryBaseForPlatform("darwin", func(string) string { t.Fatal("macOS consulted Windows environment"); return "" }, func() (string, error) { return base, nil })
	if err != nil || got != base {
		t.Fatalf("root=%q err=%v", got, err)
	}
	want := errors.New("directory unavailable")
	if _, err := recoveryBaseForPlatform("darwin", func(string) string { return "" }, func() (string, error) { return "", want }); !errors.Is(err, want) {
		t.Fatalf("lookup failure hidden: %v", err)
	}
}
