//go:build darwin

package mcpclient

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMacAppDetectionDoesNotDependOnManagedMCPConfig(t *testing.T) {
	home := t.TempDir()
	applications := t.TempDir()
	setTestHome(t, home)
	t.Setenv("LAZYMIND_HOME", filepath.Join(home, "lazymind"))
	t.Setenv("LAZYMIND_DESKTOP_APPLICATION_DIRS", applications)

	for _, app := range []string{"Cursor.app", "WorkBuddy.app", "商汤小浣熊.app", "TRAE SOLO CN.app"} {
		if err := os.Mkdir(filepath.Join(applications, app), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []Kind{Cursor, WorkBuddy, Raccoon, TRAEWork} {
		status := testAdapter(kind).Status(context.Background())
		if len(status.Requirements) < 1 || !status.Requirements[0].Satisfied {
			t.Errorf("kind=%s requirements=%#v", kind, status.Requirements)
		}
		if _, err := os.Stat(configPath(kind)); !os.IsNotExist(err) {
			t.Errorf("kind=%s unexpectedly depended on config %s: %v", kind, configPath(kind), err)
		}
	}
}
