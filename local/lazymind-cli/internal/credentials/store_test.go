package credentials

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeServerCandidatesPreferConfiguredServer(t *testing.T) {
	t.Setenv("LAZYMIND_SERVER_URL", "http://127.0.0.1:18090/")
	candidates := runtimeServerCandidates()
	if len(candidates) == 0 || candidates[0] != "http://127.0.0.1:18090" {
		t.Fatalf("candidates=%#v", candidates)
	}
}

func TestSaveAndClearSession(t *testing.T) {
	home := t.TempDir()
	store, err := NewStore(home, "")
	if err != nil {
		t.Fatal(err)
	}
	value := Credentials{
		ServerURL: "http://127.0.0.1:8090/", AccessToken: "access", RefreshToken: "refresh",
	}
	if err := store.Save(value); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(home, credentialFile)); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("saved credential permissions: info=%v err=%v", info, err)
	}
	loaded, err := store.loadUnlocked()
	if err != nil || loaded.ServerURL != "http://127.0.0.1:8090" || loaded.AccessToken != "access" {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, credentialFile)); !os.IsNotExist(err) {
		t.Fatalf("credential was not cleared: %v", err)
	}
}

func TestSaveRejectsInvalidSession(t *testing.T) {
	store, err := NewStore(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []Credentials{
		{ServerURL: "file:///tmp/server", AccessToken: "access", RefreshToken: "refresh"},
		{ServerURL: "http://user:pass@127.0.0.1:8090", AccessToken: "access", RefreshToken: "refresh"},
		{ServerURL: "http://127.0.0.1:8090", AccessToken: "", RefreshToken: "refresh"},
	} {
		if err := store.Save(value); err == nil {
			t.Fatalf("Save(%#v) succeeded", value)
		}
	}
}
