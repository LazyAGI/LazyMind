package codexruntime

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSharedRegistrationControlsCancellationAndFailsClosed(t *testing.T) {
	profile := t.TempDir()
	if err := makePrivate(profile); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(profile, RegistrationName)
	if got, err := Cancellation(profile); err != nil || got != "none" {
		t.Fatalf("legacy: %s %v", got, err)
	}
	valid := Registration{Version: 1, Profile: profile, Endpoint: "ws+unix://localhost/private/tmp/test.sock:/rpc"}
	if runtime.GOOS == "windows" {
		valid.Endpoint = "ws://127.0.0.1:49152"
		valid.TokenFile = filepath.Join(profile, "token")
		if err := writePrivateJSON(valid.TokenFile, "fixture"); err != nil {
			t.Fatal(err)
		}
	}
	write := func(value Registration) {
		t.Helper()
		if err := writePrivateJSON(file, value); err != nil {
			t.Fatal(err)
		}
	}
	write(valid)
	if got, err := Cancellation(profile); err != nil || got != "session" {
		t.Fatalf("native: %s %v", got, err)
	}
	for _, endpoint := range []string{"ws://127.0.0.1:9000/rpc", "ws+unix://localhost/tmp/a%00.sock:/rpc", "ws+unix://localhost/tmp/a?x:/rpc"} {
		bad := valid
		bad.Endpoint = endpoint
		write(bad)
		if _, err := Cancellation(profile); err == nil {
			t.Fatalf("accepted endpoint %s", endpoint)
		}
	}
	bad := valid
	bad.Profile = t.TempDir()
	write(bad)
	if _, err := Cancellation(profile); err == nil {
		t.Fatal("accepted another profile")
	}
	write(valid)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(file, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := Cancellation(profile); err == nil {
			t.Fatal("accepted public registration")
		}
	}
}

func TestSharedRegistrationAcceptsProfileAliasWithoutChangingPairingPath(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "profile")
	alias := filepath.Join(root, "alias")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(profile, alias); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink unavailable: %v", err)
		}
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(profile)
	if err != nil {
		t.Fatal(err)
	}
	valid := Registration{Version: 1, Endpoint: "ws+unix://localhost/private/tmp/test.sock:/rpc"}
	if runtime.GOOS == "windows" {
		valid.Endpoint = "ws://127.0.0.1:49152"
		valid.TokenFile = filepath.Join(profile, "token")
		if err := writePrivateJSON(valid.TokenFile, "fixture"); err != nil {
			t.Fatal(err)
		}
	}
	for _, recorded := range []string{canonical, alias} {
		valid.Profile = recorded
		if err := writePrivateJSON(filepath.Join(profile, RegistrationName), valid); err != nil {
			t.Fatal(err)
		}
		for _, lookup := range []string{canonical, alias} {
			registration, err := Read(lookup)
			if err != nil || registration.Profile != recorded {
				t.Fatalf("read %s: %+v %v", lookup, registration, err)
			}
		}
	}
	other := t.TempDir()
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(canonical); err == nil {
		t.Fatal("accepted an alias retargeted to another profile")
	}
}
