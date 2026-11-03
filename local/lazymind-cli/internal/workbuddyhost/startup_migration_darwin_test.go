package workbuddyhost

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/desktopsetup"
	"lazymind/agentconnector/internal/integrationstate"
	"lazymind/agentconnector/internal/workflowhost"
)

func legacyStartupFixture(t *testing.T) (string, workflowhost.Pairing, *fakeStartupEnvironment) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "legacy home")
	oldHome, oldRun := startupUserHome, startupLoginRun
	startupUserHome = func() (string, error) { return root, nil }
	startupLoginRun = func(context.Context, ...string) ([]byte, error) { return nil, fmt.Errorf("no loaded job in fixture") }
	t.Cleanup(func() { startupUserHome, startupLoginRun = oldHome, oldRun })
	pair, err := workflowhost.EnsureForProvider(home, "workbuddy", filepath.Join(root, "profile"), "account")
	if err != nil {
		t.Fatal(err)
	}
	config := Config{Node: filepath.Join(root, "node"), CDPPort: 9333,
		PairingFile: filepath.Join(home, "workflow-hosts", pair.ConnectorID+".json")}
	body, _ := json.Marshal(config)
	if err := writeAtomic(ConfigPath(home), body); err != nil {
		t.Fatal(err)
	}
	env := &fakeStartupEnvironment{value: "9444"}
	if err := updateStartupEnvironment(home, config.CDPPort, true, env); err != nil {
		t.Fatal(err)
	}
	return home, pair, env
}

func TestLegacyStartupMigrationSurvivesLoginAndDoesNotRepeat(t *testing.T) {
	home, _, env := legacyStartupFixture(t)
	env.value = "" // The old launchctl environment disappeared on logout.
	if err := migrateLoginEnvironment(context.Background(), home, "account", env); err != nil {
		t.Fatal(err)
	}
	if env.value != "9333" {
		t.Fatal("migration did not restore the current login environment")
	}
	files, err := desktopsetup.Load(filepath.Join(home, "workbuddy-workflow", "login-files.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := files.Check(); err != nil {
		t.Fatal(err)
	}
	path, _, _ := loginEnvironmentPaths(home)
	if output, err := exec.Command("/usr/bin/plutil", "-lint", path).CombinedOutput(); err != nil {
		t.Fatalf("invalid login job: %s: %v", output, err)
	}
	script, err := exec.Command("/usr/bin/plutil", "-extract", "ProgramArguments.2", "raw", "-o", "-", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	// Execute the installed job as a subsequent login would, without running
	// LazyMind or touching this machine's launchctl environment.
	marker := filepath.Join(t.TempDir(), "login-port")
	command := strings.Replace(string(script), "exec /bin/launchctl setenv WORKBUDDY_REMOTE_DEBUGGING_PORT 9333",
		"printf 9333 > "+desktopsetup.ShellQuote(marker), 1)
	if output, err := exec.Command("/bin/sh", "-c", command).CombinedOutput(); err != nil {
		t.Fatalf("login job: %s: %v", output, err)
	}
	if body, err := os.ReadFile(marker); err != nil || string(body) != "9333" {
		t.Fatalf("login did not restore the port: %q: %v", body, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	env.value = "9555"
	if err := migrateLoginEnvironment(context.Background(), home, "account", env); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) || env.value != "9555" {
		t.Fatal("completed migration overwrote later user changes")
	}
}

func TestLegacyStartupMigrationRetriesIncompleteJournal(t *testing.T) {
	home, _, env := legacyStartupFixture(t)
	env.value, env.fail = "", true
	marker := filepath.Join(home, "workbuddy-workflow", "startup-migration-v1")
	if err := migrateLoginEnvironment(context.Background(), home, "account", env); err == nil {
		t.Fatal("expected environment failure")
	}
	if state, _ := os.ReadFile(marker); string(state) != "pending\n" {
		t.Fatalf("failed migration was not left pending: %q", state)
	}
	env.fail = false
	if err := migrateLoginEnvironment(context.Background(), home, "account", env); err != nil {
		t.Fatal(err)
	}
	if state, _ := os.ReadFile(marker); string(state) != "complete\n" || env.value != "9333" {
		t.Fatalf("migration was not completed: %q, port=%s", state, env.value)
	}
	// Disabling after migration still restores the original pre-install value.
	if err := integrationstate.Set(home, "workbuddy", false); err != nil {
		t.Fatal(err)
	}
	if err := configureLoginEnvironment(context.Background(), home, 0, false); err != nil {
		t.Fatal(err)
	}
	if err := updateStartupEnvironment(home, 0, false, env); err != nil || env.value != "9444" {
		t.Fatalf("original environment was lost: %s: %v", env.value, err)
	}
	if err := migrateLoginEnvironment(context.Background(), home, "account", env); err != nil {
		t.Fatal(err)
	}
	path, _, _ := loginEnvironmentPaths(home)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("disabled migration recreated the login job")
	}
}

func TestLegacyStartupMigrationHonorsDisableAndAccount(t *testing.T) {
	for _, scenario := range []string{"disabled-choice", "disabled-pairing", "different-account", "disabled-pending-migration", "new-install"} {
		t.Run(scenario, func(t *testing.T) {
			home, pair, env := legacyStartupFixture(t)
			account := "account"
			if scenario == "disabled-pending-migration" {
				env.value, env.fail = "", true
				if err := migrateLoginEnvironment(context.Background(), home, account, env); err == nil {
					t.Fatal("expected environment failure")
				}
			}
			if scenario == "disabled-choice" || scenario == "disabled-pending-migration" || scenario == "new-install" {
				if err := integrationstate.Set(home, "workbuddy", scenario == "new-install"); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "disabled-pairing" {
				if err := workflowhost.Disable(home, pair.ConnectorID); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "different-account" {
				account = "other-account"
			}
			env.value, env.fail = "", false
			err := migrateLoginEnvironment(context.Background(), home, account, env)
			if (scenario == "disabled-pairing" || scenario == "different-account") != (err != nil) {
				t.Fatalf("unexpected migration result: %v", err)
			}
			if env.value != "" {
				t.Fatal("migration enabled an ineligible installation")
			}
			if scenario != "disabled-pending-migration" {
				path, _, _ := loginEnvironmentPaths(home)
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("migration created an unexpected login job")
				}
			}
		})
	}
}
