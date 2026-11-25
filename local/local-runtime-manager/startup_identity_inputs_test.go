//go:build darwin || linux || windows

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func startupIdentityTestInstallation(t *testing.T) (RuntimePaths, string, string) {
	t.Helper()
	app := filepath.Join(t.TempDir(), "LazyMind.app")
	resources := filepath.Join(app, "Contents", "Resources", "runtime")
	changed := filepath.Join(resources, "deps", "nested-resource")
	if runtime.GOOS == "windows" {
		resources = filepath.Join(app, "resources", "runtime")
		changed = filepath.Join(app, "LazyMind.exe")
	}
	paths := RuntimePaths{ResourcesRoot: resources}
	helper := filepath.Join(resources, "bin", executableName("lazymind-update-helper"))
	for _, name := range []string{helper, changed} {
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("first"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return paths, changed, helper
}

func TestStartupIdentityInputsDetectInPlaceEdit(t *testing.T) {
	paths, changed, _ := startupIdentityTestInstallation(t)
	snapshot := func() [32]byte {
		t.Helper()
		sum, err := snapshotStartupIdentityInputs(t.Context(), paths)
		if err != nil {
			t.Fatal(err)
		}
		return sum
	}
	before := snapshot()
	if snapshot() != before {
		t.Fatal("reading an unchanged installation changed its fingerprint")
	}
	info, err := os.Stat(changed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(changed, []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(changed, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if snapshot() == before {
		t.Fatal("same-size edit with restored modification time was not detected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := snapshotStartupIdentityInputs(ctx, paths); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled verification snapshot returned %v", err)
	}
}

func TestStartupIdentityRejectsRestoredFile(t *testing.T) {
	paths, applicationInput, helper := startupIdentityTestInstallation(t)
	for _, file := range []string{applicationInput, helper} {
		t.Run(filepath.Base(file), func(t *testing.T) {
			stable, release, err := watchStartupIdentityInputs(paths)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			before, err := snapshotStartupIdentityInputs(t.Context(), paths)
			if err != nil || !stable() {
				t.Fatalf("undisturbed installation refused: %v", err)
			}
			// The path-based verifier sees a different file, but a consumer
			// examining only bytes and object IDs after it returns sees A again.
			if err := os.Rename(file, file+".original"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("other"), 0600); err != nil {
				t.Fatal(err)
			}
			seen, err := os.ReadFile(file)
			if err != nil || string(seen) != "other" {
				t.Fatalf("verifier did not see the replacement: %v", err)
			}
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(file+".original", file); err != nil {
				t.Fatal(err)
			}
			after, err := snapshotStartupIdentityInputs(t.Context(), paths)
			if err != nil {
				t.Fatal(err)
			}
			if before == after && stable() {
				t.Fatal("restoring the original file hid the intervening replacement")
			}
		})
	}
}
