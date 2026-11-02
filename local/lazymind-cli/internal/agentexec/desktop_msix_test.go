package agentexec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWindowsPackagedDesktopCandidatesPreserveOrder(t *testing.T) {
	root := t.TempDir()
	var packages []windowsAppPackage
	var expected []string
	for _, version := range []string{"new", "old"} {
		location := filepath.Join(root, version)
		file := filepath.Join(location, "app", "ChatGPT.exe")
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("fixture"), 0700); err != nil {
			t.Fatal(err)
		}
		packages = append(packages, windowsAppPackage{Name: "OpenAI.Codex", InstallLocation: location, Executables: []string{"app/ChatGPT.exe"}})
		expected = append(expected, file)
	}
	body, err := json.Marshal(packages)
	if err != nil {
		t.Fatal(err)
	}
	spec := DesktopApplication{PackageNames: []string{"OpenAI.Codex"}, ExecutableNames: []string{"ChatGPT.exe"}}
	actual := findWindowsPackagedDesktopApplications(spec, func([]string) ([]byte, error) { return body, nil })
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("got %v, want %v", actual, expected)
	}
}

func TestWindowsDesktopPackageDiscovery(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "OpenAI.Codex_new-version", "app", "ChatGPT.exe")
	cli := filepath.Join(root, "cli", "codex.exe")
	for _, file := range []string{app, cli} {
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("fixture"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", filepath.Dir(cli))
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "redirected-local"))
	t.Setenv("APPDATA", filepath.Join(root, "redirected-roaming"))
	spec := DesktopApplication{PackageNames: []string{"OpenAI.Codex"}, ExecutableNames: []string{"ChatGPT.exe", "Codex.exe"}}
	valid := windowsAppPackage{"OpenAI.Codex", filepath.Dir(filepath.Dir(app)), []string{`app\ChatGPT.exe`}}
	for _, tc := range []struct {
		name        string
		traditional string
		packages    []windowsAppPackage
		queryError  error
		want        string
	}{
		{"msix", "", []windowsAppPackage{valid}, nil, app},
		{"traditional-priority", app, []windowsAppPackage{valid}, context.DeadlineExceeded, app},
		{"only-cli", "", nil, nil, ""},
		{"not-installed", "", []windowsAppPackage{}, nil, ""},
		{"query-failed", "", []windowsAppPackage{valid}, context.DeadlineExceeded, ""},
		{"wrong-package", "", []windowsAppPackage{{"Other.Codex", valid.InstallLocation, valid.Executables}}, nil, ""},
		{"missing-executable", "", []windowsAppPackage{{valid.Name, valid.InstallLocation, []string{`app\Codex.exe`}}}, nil, ""},
		{"missing-manifest-entry", "", []windowsAppPackage{{valid.Name, valid.InstallLocation, nil}}, nil, ""},
		{"version-upgrade", "", []windowsAppPackage{{valid.Name, filepath.Join(root, "removed-old-version"), valid.Executables}, valid}, nil, app},
		{"case-insensitive-identity", "", []windowsAppPackage{{"openai.codex", valid.InstallLocation, valid.Executables}}, nil, app},
		{"escape-package", "", []windowsAppPackage{{valid.Name, valid.InstallLocation, []string{`..\cli\codex.exe`}}}, nil, ""},
		{"absolute-executable", "", []windowsAppPackage{{valid.Name, valid.InstallLocation, []string{app}}}, nil, ""},
		{"drive-relative-executable", "", []windowsAppPackage{{valid.Name, valid.InstallLocation, []string{`C:Codex.exe`}}}, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queried := false
			got := findWindowsDesktopApplication(spec, func(DesktopApplication) string { return tc.traditional }, func(names []string) ([]byte, error) {
				queried = true
				if !reflect.DeepEqual(names, spec.PackageNames) {
					t.Fatalf("query names: %v", names)
				}
				body, _ := json.Marshal(tc.packages)
				return body, tc.queryError
			})
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if tc.traditional != "" && queried {
				t.Fatal("queried MSIX despite traditional installation")
			}
		})
	}
}

func TestWindowsDesktopPackageQueryFailsClosed(t *testing.T) {
	spec := DesktopApplication{PackageNames: []string{"OpenAI.Codex"}}
	for _, body := range []string{"", "not JSON", `{"Name":"OpenAI.Codex"}`, `[{"Executables":"app\\ChatGPT.exe"}]`} {
		got := findWindowsDesktopApplication(spec, func(DesktopApplication) string { return "" }, func([]string) ([]byte, error) { return []byte(body), nil })
		if got != "" {
			t.Fatalf("accepted invalid output %q", body)
		}
	}
	findWindowsDesktopApplication(DesktopApplication{}, func(DesktopApplication) string { return "" }, func([]string) ([]byte, error) {
		t.Fatal("queried packages without an explicit identity")
		return nil, nil
	})
}

func TestDesktopManualBindingPrecedesPackageDiscovery(t *testing.T) {
	t.Setenv("LAZYMIND_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "SelectedDesktop.exe")
	if err := os.WriteFile(path, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	bound, err := SetExecutableBinding(CodexDesktop, path)
	if err != nil {
		t.Fatal(err)
	}
	spec := DesktopApplication{BindingTarget: CodexDesktop, PackageNames: []string{"OpenAI.Codex"}, StatePaths: []string{filepath.Dir(path)}}
	got, err := FindDesktopApplication(spec)
	if err != nil || got != bound {
		t.Fatalf("got %q, err=%v, want binding %q", got, err, bound)
	}
	state, err := InspectDesktopApplication(spec)
	if err != nil || !state.Installed || !state.Initialized {
		t.Fatalf("state=%+v, err=%v", state, err)
	}
}

func TestAppPackageQueryQuotesIdentity(t *testing.T) {
	if script := windowsAppPackageQuery([]string{"OpenAI.Codex", "name'with-quote"}); !strings.Contains(script, "@('OpenAI.Codex','name''with-quote')") {
		t.Fatalf("package identities were not quoted: %s", script)
	}
}
