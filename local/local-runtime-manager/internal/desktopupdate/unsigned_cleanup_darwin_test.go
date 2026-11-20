//go:build darwin && lazymind_unsigned_test

package desktopupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompletedUnsignedInstallRemovesOnlyCommittedTransaction(t *testing.T) {
	f := newNativeStateFixture(t)
	root, err := filepath.EvalSymlinks(f.root)
	if err != nil {
		t.Fatal(err)
	}
	stages := filepath.Join(root, "installs")
	stage := filepath.Join(stages, f.tx)
	other := filepath.Join(stages, strings.Repeat("2", 32))
	files := []string{
		filepath.Join(stage, "package.zip"),
		filepath.Join(stage, "previous.app", "Contents", "Info.plist"),
		filepath.Join(other, "package.zip"),
	}
	for _, name := range files {
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("private fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	f.installing(t)
	f.command(t, "activated", map[string]any{
		"transactionId": f.tx, "releaseId": "target", "dataProfile": "format-v2-fixture",
		"componentMode": "absent", "components": map[string]string{},
	}, "activated", "")
	proof := map[string]any{
		"transactionId": f.tx, "releaseId": "target", "dataProfile": "format-v2-fixture",
		"components": map[string]string{}, "peers": nativePeerProofs(f.tx), "dirty": true,
	}
	f.command(t, "complete", proof, "blocked", "update_startup_failed")
	if err := discardCompletedUnsignedInstall(t.Context(), root, f.tx); ErrorCode(err) != "update_incomplete" {
		t.Fatalf("active transaction cleanup: %v", err)
	}
	for _, name := range files {
		if _, err := os.Stat(name); err != nil {
			t.Fatalf("failed update lost its recovery material: %v", err)
		}
	}
	proof["dirty"] = false
	f.command(t, "complete", proof, "completed", "")
	if err := discardCompletedUnsignedInstall(t.Context(), root, f.tx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stage); !os.IsNotExist(err) {
		t.Fatalf("committed update left its private install directory: %v", err)
	}
	if _, err := os.Stat(files[2]); err != nil {
		t.Fatalf("another transaction's package was affected: %v", err)
	}
	if err := discardCompletedUnsignedInstall(t.Context(), root, f.tx); err != nil {
		t.Fatalf("completion retry: %v", err)
	}
}

func TestCompletedUnsignedInstallRejectsAliasedStagesAndInvalidTransaction(t *testing.T) {
	f := newNativeStateFixture(t)
	root, err := filepath.EvalSymlinks(f.root)
	if err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(filepath.Dir(root), "foreign")
	keep := filepath.Join(foreign, f.tx, "package.zip")
	if err := os.MkdirAll(filepath.Dir(keep), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keep, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, filepath.Join(root, "installs")); err != nil {
		t.Fatal(err)
	}
	for _, tx := range []string{f.tx, "../foreign/" + f.tx} {
		if err := discardCompletedUnsignedInstall(t.Context(), root, tx); err == nil {
			t.Fatal("invalid install path accepted")
		}
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("cleanup affected a file outside private stages: %v", err)
		}
	}
}
