package connectorconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEnvironmentHomesPreserveExistingOwnerAndIsolateSecondDeployment(t *testing.T) {
	for _, desktopFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "desktop-first", false: "compose-first"}[desktopFirst], func(t *testing.T) {
			root := t.TempDir()
			identity, _ := OriginIdentity("http://localhost:8090")
			legacy := map[string]string{"server_url": "http://localhost:8090", "access_token": "keep", "refresh_token": "keep-refresh"}
			first, second := "desktop", identity
			if desktopFirst {
				legacy["local_runtime_id"] = "desktop-data"
			} else {
				first, second = identity, "desktop"
			}
			body, _ := json.Marshal(legacy)
			if err := os.WriteFile(filepath.Join(root, "credentials.json"), body, 0600); err != nil {
				t.Fatal(err)
			}
			home, err := EnvironmentHome(root, second)
			if err != nil {
				t.Fatal(err)
			}
			if home == root {
				t.Fatal("second deployment reused legacy credentials")
			}
			original, err := EnvironmentHome(root, first)
			if err != nil || original != root {
				t.Fatalf("legacy home=%s err=%v", original, err)
			}
			again, err := EnvironmentHome(root, second)
			if err != nil || again != home {
				t.Fatalf("unstable home %s %v", again, err)
			}
			got, _ := os.ReadFile(filepath.Join(root, "credentials.json"))
			if string(got) != string(body) {
				t.Fatal("legacy credentials modified")
			}
			base, err := RootHome(home)
			if err != nil || base != root {
				t.Fatalf("root=%s err=%v", base, err)
			}
		})
	}
}

func TestEnvironmentHomeSurvivesLogoutAndDoesNotReassignPrimary(t *testing.T) {
	root := t.TempDir()
	first, err := EnvironmentHome(root, "desktop")
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := OriginIdentity("http://localhost:8090")
	second, err := EnvironmentHome(root, identity)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("environments share credentials")
	}
	alias, _ := OriginIdentity("http://127.0.0.1:8090")
	aliased, err := EnvironmentHome(root, alias)
	if err != nil || aliased != second {
		t.Fatal("loopback aliases split the same deployment")
	}
	if _, err := os.Stat(filepath.Join(first, "credentials.json")); !os.IsNotExist(err) {
		t.Fatal("fixture unexpectedly has credentials")
	}
	if got, err := EnvironmentHome(root, "desktop"); err != nil || got != first {
		t.Fatal("logout changed environment ownership")
	}
}

func TestScopedBridgeEndpointOverridesOnlyItsOwnDefault(t *testing.T) {
	t.Setenv(EnvironmentKey, "")
	t.Setenv("LAZYMIND_RUNTIME_PROFILE", "")
	t.Setenv("LAZYMIND_ASSISTANT_BRIDGE_URL", "")
	root := t.TempDir()
	t.Setenv("LAZYMIND_HOME", root)
	if err := WriteBridgeEndpoint(root, BridgeEndpoint{URL: "http://127.0.0.1:19100", Broker: "http://127.0.0.1:19100"}); err != nil {
		t.Fatal(err)
	}
	got, err := URL("LAZYMIND_ASSISTANT_BRIDGE_URL", DefaultBridgeURL)
	if err != nil || got != "http://127.0.0.1:19100" {
		t.Fatalf("url=%s err=%v", got, err)
	}
	t.Setenv("LAZYMIND_ASSISTANT_BRIDGE_URL", "http://127.0.0.1:20200")
	got, err = URL("LAZYMIND_ASSISTANT_BRIDGE_URL", DefaultBridgeURL)
	if err != nil || got != "http://127.0.0.1:20200" {
		t.Fatal("explicit configuration was ignored")
	}
	if err := WriteBridgeEndpoint(root, BridgeEndpoint{URL: "https://remote.example", Broker: DefaultBridgeURL}); err == nil {
		t.Fatal("accepted nonlocal bridge")
	}
}

func TestDesktopHomeDoesNotReNestAnExplicitEnvironment(t *testing.T) {
	t.Setenv(EnvironmentKey, "")
	t.Setenv("LAZYMIND_RUNTIME_PROFILE", "desktop")
	root := t.TempDir()
	identity, _ := OriginIdentity("http://localhost:8090")
	if _, err := EnvironmentHome(root, identity); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_HOME", root)
	home, err := Home()
	if err != nil || home == root {
		t.Fatalf("home=%s err=%v", home, err)
	}
	t.Setenv("LAZYMIND_HOME", home)
	if got, err := Home(); err != nil || got != home {
		t.Fatalf("nested environment home=%s err=%v", got, err)
	}
}
