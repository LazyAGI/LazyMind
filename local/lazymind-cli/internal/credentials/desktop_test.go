package credentials

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestDesktopRenewalRotatesOnceAndPersistsVerifiedIdentity(t *testing.T) {
	var refreshes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case authPath + "/refresh":
			refreshes.Add(1)
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["refresh_token"] != "synthetic-old-refresh" {
				t.Error("unexpected refresh credential")
			}
			_ = json.NewEncoder(w).Encode(Credentials{AccessToken: "synthetic-new-access", RefreshToken: "synthetic-new-refresh", ExpiresIn: 3600})
		case authPath + "/me":
			if r.Header.Get("Authorization") != "Bearer synthetic-new-access" {
				t.Error("identity must use renewed token")
			}
			_, _ = w.Write([]byte(`{"user_id":"alice","status":"active"}`))
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	store, _ := NewStore(t.TempDir(), "")
	original := Credentials{ServerURL: server.URL, AccessToken: "synthetic-old-access", RefreshToken: "synthetic-old-refresh"}
	if err := store.Save(original); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			result, err := store.RenewDesktop(context.Background(), original, "alice")
			if err != nil || result.AccessToken != "synthetic-new-access" {
				t.Errorf("renew result: %v", err)
			}
		}()
	}
	workers.Wait()
	if refreshes.Load() != 1 {
		t.Fatalf("rotated %d times", refreshes.Load())
	}
	stored, err := store.loadUnlocked()
	if err != nil || stored.RefreshToken != "synthetic-new-refresh" {
		t.Fatalf("new credential not persisted: %v", err)
	}
	info, err := os.Stat(store.path())
	if err != nil {
		t.Fatal(err)
	}
	// Windows does not expose Unix permission bits; match the store tests.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("renewed credential permissions=%o, want 600", info.Mode().Perm())
	}
}

func TestDesktopRenewalFailsClosedWithoutAdministratorFallback(t *testing.T) {
	for _, mode := range []string{"revoked", "forbidden", "different-user", "inactive", "redirect", "network", "logged-out", "different-origin"} {
		t.Run(mode, func(t *testing.T) {
			var forbiddenRequests atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forbiddenRequests.Add(1); w.WriteHeader(500) }))
			defer target.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == localSessionPath {
					forbiddenRequests.Add(1)
					w.WriteHeader(500)
					return
				}
				if r.URL.Path == authPath+"/refresh" {
					switch mode {
					case "revoked":
						w.WriteHeader(401)
						_, _ = w.Write([]byte(`{"message":"synthetic-secret-must-not-escape"}`))
						return
					case "forbidden":
						w.WriteHeader(403)
						return
					case "redirect":
						http.Redirect(w, r, target.URL, 307)
						return
					case "network":
						w.WriteHeader(503)
						return
					}
					_, _ = w.Write([]byte(`{"access_token":"synthetic-new","refresh_token":"synthetic-rotated"}`))
					return
				}
				user, status := "alice", "active"
				if mode == "different-user" {
					user = "bob"
				}
				if mode == "inactive" {
					status = "disabled"
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"user_id": user, "status": status})
			}))
			defer server.Close()
			store, _ := NewStore(t.TempDir(), "")
			original := Credentials{ServerURL: server.URL, AccessToken: "synthetic-old", RefreshToken: "synthetic-refresh"}
			if err := store.Save(original); err != nil {
				t.Fatal(err)
			}
			expected := original
			if mode == "logged-out" {
				if err := store.Clear(); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "different-origin" {
				expected.ServerURL = target.URL
			}
			_, err := store.RenewDesktop(context.Background(), expected, "alice")
			if err == nil {
				t.Fatal("expected fail-closed renewal")
			}
			if err != ErrAuthenticationRequired && err.Error() != "DESKTOP_SESSION_RENEWAL_UNAVAILABLE" {
				t.Fatalf("unsafe error: %v", err)
			}
			if forbiddenRequests.Load() != 0 {
				t.Fatal("followed redirect or bootstrapped administrator")
			}
			stored, loadErr := store.loadUnlocked()
			if mode == "logged-out" {
				if loadErr != ErrAuthenticationRequired {
					t.Fatal("resurrected logout")
				}
			} else if loadErr != nil || stored.AccessToken != original.AccessToken {
				t.Fatal("replaced credentials without verified original identity")
			}
		})
	}
}

func TestDesktopSnapshotKeepsHandoffAndClearsItOnNewLogin(t *testing.T) {
	store, _ := NewStore(t.TempDir(), "")
	original := Credentials{ServerURL: "http://127.0.0.1:8090", AccessToken: "synthetic-old", RefreshToken: "synthetic-refresh"}
	if err := store.Save(original); err != nil {
		t.Fatal(err)
	}
	first, err := store.DesktopSnapshot()
	if err != nil || first.DesktopHandoff != desktopFingerprint(original) {
		t.Fatal("missing original handoff")
	}
	updated := first
	updated.AccessToken = "synthetic-new"
	updated.RefreshToken = "synthetic-rotated"
	if err := store.withLock(func() error { return store.saveUnlocked(updated) }); err != nil {
		t.Fatal(err)
	}
	restarted, _ := NewStore(store.home, "")
	snapshot, err := restarted.DesktopSnapshot()
	if err != nil || snapshot.AccessToken != updated.AccessToken || snapshot.DesktopHandoff != first.DesktopHandoff {
		t.Fatal("handoff lost across process restart")
	}
	if err := store.Save(updated); err != nil {
		t.Fatal(err)
	}
	synchronized, err := store.DesktopSnapshot()
	if err != nil || synchronized.DesktopHandoff != desktopFingerprint(updated) {
		t.Fatal("renderer sync must advance original fingerprint")
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.DesktopSnapshot(); err != ErrAuthenticationRequired {
		t.Fatal("logged-out snapshot restored a session")
	}
}

func TestDesktopRetriesIdentityCheckWithoutConsumingRefreshAgain(t *testing.T) {
	var refreshes atomic.Int32
	var available atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath+"/refresh" {
			if refreshes.Add(1) > 1 {
				w.WriteHeader(401)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"synthetic-candidate","refresh_token":"synthetic-rotated"}`))
			return
		}
		if !available.Load() {
			w.WriteHeader(503)
			return
		}
		_, _ = w.Write([]byte(`{"user_id":"alice","status":"active"}`))
	}))
	defer server.Close()
	store, _ := NewStore(t.TempDir(), "")
	original := Credentials{ServerURL: server.URL, AccessToken: "synthetic-old", RefreshToken: "synthetic-refresh"}
	if err := store.Save(original); err != nil {
		t.Fatal(err)
	}
	candidate, err := store.RenewDesktop(context.Background(), original, "alice")
	if err == nil || IsAuthenticationRequired(err) || candidate.AccessToken != "synthetic-candidate" {
		t.Fatal("temporary identity error must preserve the rotated candidate")
	}
	stored, _ := store.loadUnlocked()
	if stored.AccessToken != original.AccessToken {
		t.Fatal("unverified candidate persisted as active credentials")
	}
	available.Store(true)
	verified, err := store.RenewDesktopCandidate(context.Background(), original, "alice", &candidate)
	if err != nil || verified.AccessToken != candidate.AccessToken || refreshes.Load() != 1 {
		t.Fatal("identity recovery consumed an already rotated refresh token")
	}
}
