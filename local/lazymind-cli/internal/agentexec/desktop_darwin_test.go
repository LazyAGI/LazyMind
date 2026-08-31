//go:build darwin

package agentexec

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspectDesktopApplicationFindsMacAppWithoutBindingsOrState(t *testing.T) {
	applications := t.TempDir()
	t.Setenv("LAZYMIND_DESKTOP_APPLICATION_DIRS", applications)
	if err := os.Mkdir(filepath.Join(applications, "Cursor.app"), 0o700); err != nil {
		t.Fatal(err)
	}

	state, err := InspectDesktopApplication(DesktopApplication{
		BindingTarget: CursorDesktop,
		DisplayNames:  []string{"Cursor"},
		StatePaths:    []string{filepath.Join(t.TempDir(), ".cursor")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !state.Installed || state.Initialized {
		t.Fatalf("state=%#v", state)
	}
}

func TestInspectDesktopApplicationMatchesLocalizedMacAppName(t *testing.T) {
	applications := t.TempDir()
	t.Setenv("LAZYMIND_DESKTOP_APPLICATION_DIRS", applications)
	if err := os.Mkdir(filepath.Join(applications, "商汤小浣熊.app"), 0o700); err != nil {
		t.Fatal(err)
	}

	state, err := InspectDesktopApplication(DesktopApplication{
		BindingTarget: RaccoonDesktop,
		DisplayNames:  []string{"Raccoon", "商汤小浣熊"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !state.Installed {
		t.Fatalf("state=%#v", state)
	}
}

func TestInspectDesktopApplicationDoesNotTreatStaleStateAsFirstLaunch(t *testing.T) {
	applications := t.TempDir()
	statePath := filepath.Join(t.TempDir(), ".workbuddy")
	t.Setenv("LAZYMIND_DESKTOP_APPLICATION_DIRS", applications)
	if err := os.Mkdir(statePath, 0o700); err != nil {
		t.Fatal(err)
	}

	state, err := InspectDesktopApplication(DesktopApplication{
		BindingTarget: WorkBuddyDesktop,
		DisplayNames:  []string{"WorkBuddy"},
		StatePaths:    []string{statePath},
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.Installed || state.Initialized {
		t.Fatalf("stale state must not satisfy installation or first launch: %#v", state)
	}
}

func TestInspectDesktopApplicationAcceptsBoundMacApplicationBundle(t *testing.T) {
	applications := t.TempDir()
	bundle := filepath.Join(applications, "Custom Codex.app")
	if err := os.Mkdir(bundle, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_HOME", t.TempDir())
	if _, err := SetExecutableBinding(CodexDesktop, bundle); err != nil {
		t.Fatal(err)
	}

	state, err := InspectDesktopApplication(DesktopApplication{BindingTarget: CodexDesktop})
	if err != nil || !state.Installed {
		t.Fatalf("state=%#v err=%v", state, err)
	}
}
