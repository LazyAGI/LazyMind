package credentials

import (
	"os"
	"testing"
)

func TestClearIfCurrentProtectsNewCredentials(t *testing.T) {
	for _, scenario := range []string{"matching", "new-login", "renewed-strict", "renewed-logout", "other-server", "missing-expectation"} {
		t.Run(scenario, func(t *testing.T) {
			s, err := NewStore(t.TempDir(), "")
			if err != nil {
				t.Fatal(err)
			}
			old := Credentials{ServerURL: "http://127.0.0.1:8090", AccessToken: "old", RefreshToken: "old-refresh"}
			if err := s.Save(old); err != nil {
				t.Fatal(err)
			}
			request := ClearRequest{ExpectedSession: old}
			wantClear := scenario == "matching" || scenario == "renewed-logout"
			switch scenario {
			case "new-login":
				newLogin := old
				newLogin.AccessToken = "new"
				newLogin.RefreshToken = "new-refresh"
				if err := s.Save(newLogin); err != nil {
					t.Fatal(err)
				}
				request.AllowRenewed = true
			case "renewed-strict", "renewed-logout":
				// A CLI renewal retains its renderer's handoff; a new renderer
				// login above installs a different handoff instead.
				err := s.withLock(func() error {
					current, err := s.loadUnlocked()
					if err != nil {
						return err
					}
					current.AccessToken, current.RefreshToken = "renewed", "renewed-refresh"
					return s.saveUnlocked(current)
				})
				if err != nil {
					t.Fatal(err)
				}
				request.AllowRenewed = scenario == "renewed-logout"
			case "other-server":
				request.ExpectedSession.ServerURL = "http://127.0.0.1:8091"
			case "missing-expectation":
				request.ExpectedSession = Credentials{}
			}
			cleared, err := s.ClearIfCurrent(request)
			if scenario == "missing-expectation" {
				if err == nil {
					t.Fatal("unconditional bridge clear accepted")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if cleared != wantClear {
				t.Fatalf("cleared=%v want %v", cleared, wantClear)
			}
			_, statErr := os.Stat(s.path())
			if os.IsNotExist(statErr) != wantClear {
				t.Fatalf("wrong credential file effect: %v", statErr)
			}
		})
	}
}
