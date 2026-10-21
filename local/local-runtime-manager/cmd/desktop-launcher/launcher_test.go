package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestSanitizedEnvironmentRemovesRunAsNodeCaseInsensitively(t *testing.T) {
	input := []string{
		"PATH=C:\\Windows\\System32",
		"ELECTRON_RUN_AS_NODE=1",
		"electron_run_as_node=true",
		"VALUE=left=right",
	}
	want := []string{"PATH=C:\\Windows\\System32", "VALUE=left=right"}

	got, removed := sanitizedEnvironment(input)
	if !removed {
		t.Fatal("expected inherited ELECTRON_RUN_AS_NODE to be removed")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment=%#v want %#v", got, want)
	}
}

func TestSanitizedEnvironmentPreservesCleanEnvironment(t *testing.T) {
	input := []string{"PATH=C:\\Windows", "LOCALAPPDATA=C:\\Users\\test\\AppData\\Local"}
	got, removed := sanitizedEnvironment(input)
	if removed {
		t.Fatal("clean environment was reported as modified")
	}
	if !reflect.DeepEqual(got, input) {
		t.Fatalf("environment=%#v want %#v", got, input)
	}
}

func TestElectronExecutablePathUsesLauncherDirectory(t *testing.T) {
	launcher := filepath.Join("install", "LazyMind.exe")
	want := filepath.Join("install", "LazyMindApp.exe")
	if got := electronExecutablePath(launcher, "LazyMindApp.exe"); got != want {
		t.Fatalf("path=%q want %q", got, want)
	}
}
