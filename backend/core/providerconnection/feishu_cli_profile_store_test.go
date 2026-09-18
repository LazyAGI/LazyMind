package providerconnection

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestFeishuCLIProfileStoreIsolatesOwnersAndConnections(t *testing.T) {
	store, err := NewFeishuCLIProfileStore(filepath.Join(t.TempDir(), "profiles"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Ensure(context.Background(), "user-a", "connection-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Ensure(context.Background(), "user-b", "connection-b")
	if err != nil {
		t.Fatal(err)
	}
	if first.Reference == second.Reference || first.ConfigDir == second.ConfigDir || first.DownloadsDir == second.DownloadsDir {
		t.Fatal("Feishu CLI profiles share storage")
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{first.RootDir, first.ConfigDir, first.StateDir, first.DownloadsDir} {
			info, statErr := os.Stat(path)
			if statErr != nil {
				t.Fatal(statErr)
			}
			if info.Mode().Perm() != 0o700 {
				t.Fatalf("profile directory %s mode = %v", path, info.Mode().Perm())
			}
		}
	}
}

func TestFeishuCLIProfileStoreRejectsTraversalAndIdentityReplacement(t *testing.T) {
	store, err := NewFeishuCLIProfileStore(filepath.Join(t.TempDir(), "profiles"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Ensure(context.Background(), "../user", "connection"); !errors.Is(err, ErrCLIProfileNotFound) {
		t.Fatalf("traversal error = %v", err)
	}
	profile, err := store.Ensure(context.Background(), "user-a", "connection-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BindIdentity(context.Background(), profile, "tenant-a", "open-a", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.BindIdentity(context.Background(), profile, "tenant-b", "open-a", time.Now()); !errors.Is(err, ErrCLIProfileTenantMismatch) {
		t.Fatalf("tenant replacement error = %v", err)
	}
}

func TestFeishuCLIProfileStoreRestoresOnlyOwnedSessionState(t *testing.T) {
	store, err := NewFeishuCLIProfileStore(filepath.Join(t.TempDir(), "profiles"))
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.Ensure(context.Background(), "user-a", "connection-a")
	if err != nil {
		t.Fatal(err)
	}
	want := feishuCLISessionState{
		SessionID: "fcli-session", OwnerUserID: "user-a", Status: FeishuCLIStatusAuthWaitingUser,
		AuthConnectionID: "connection-a", DeviceCode: "fixture-device-code", ExpiresAt: time.Now().Add(time.Minute),
	}
	if err := store.WriteState(context.Background(), profile, want.SessionID, want); err != nil {
		t.Fatal(err)
	}
	var got feishuCLISessionState
	restored, err := store.FindState(context.Background(), "user-a", want.SessionID, &got)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Reference != profile.Reference || got.OwnerUserID != "user-a" || got.DeviceCode != want.DeviceCode {
		t.Fatalf("restored profile/state mismatch: %#v %#v", restored, got)
	}
	if _, err := store.FindState(context.Background(), "user-b", want.SessionID, &feishuCLISessionState{}); !errors.Is(err, ErrCLIProfileNotFound) {
		t.Fatalf("cross-owner restore error = %v", err)
	}
}
