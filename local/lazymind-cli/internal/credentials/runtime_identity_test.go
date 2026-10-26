package credentials

import "testing"

func TestDesktopScopeSurvivesPortChangeButNotAccountOrRuntimeChange(t *testing.T) {
	store, _ := NewStore(t.TempDir(), "")
	root := t.TempDir()
	value := Credentials{ServerURL: "http://127.0.0.1:8090", Username: "alice", TenantID: "tenant", AccessToken: "access", RefreshToken: "refresh"}
	save := func() string {
		t.Helper()
		if err := store.SaveDesktop(value, root); err != nil {
			t.Fatal(err)
		}
		scope, err := store.AccountScope()
		if err != nil {
			t.Fatal(err)
		}
		return scope
	}
	first := save()
	value.ServerURL = "http://127.0.0.1:8097"
	value.AccessToken = "new-token"
	if save() != first {
		t.Fatal("port/token rotation changed pairing scope")
	}
	value.Username = "bob"
	if save() == first {
		t.Fatal("account switch retained pairing")
	}
	value.Username = "alice"
	root = t.TempDir()
	if save() == first {
		t.Fatal("another desktop runtime retained pairing")
	}
	value.ServerURL = "https://remote.example:443"
	remote := save()
	value.ServerURL = "https://remote.example:8443"
	if save() == remote {
		t.Fatal("remote server change retained pairing")
	}
}
