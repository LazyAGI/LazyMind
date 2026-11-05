package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLocalPythonPathsPreserveLegacyEnvironment(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	legacy := filepath.Join(repo, "local", "build", "deps", "python", "algorithm")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(legacy, "keep-existing-dependencies")
	if err := os.WriteFile(marker, []byte("preserve me"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, paths, err := NewRuntimeConfig("local", repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatal(err)
	}
	if paths.AlgorithmVenv == legacy {
		t.Fatal("legacy environment was reused")
	}
	if raw, err := os.ReadFile(marker); err != nil || string(raw) != "preserve me" {
		t.Fatalf("legacy dependencies changed: %q, %v", raw, err)
	}
}

func TestLocalPythonPathsRejectForeignDirectoryLinks(t *testing.T) {
	for _, name := range []string{"algorithm", "auth-service", "channel-gateway", "python-deps", "runtime"} {
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			writeComposeFixture(t, repo)
			platform := runtime.GOOS + "-" + runtime.GOARCH
			link := filepath.Join(repo, "local", "build", "deps", "python", platform, name)
			if name == "runtime" {
				link = filepath.Join(repo, "local", "build", "runtimes", "python", platform)
			} else if name == "python-deps" {
				link = filepath.Join(repo, "local", "build", "deps", "python")
			}
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			foreign := t.TempDir()
			if err := createDirectoryLink(foreign, link); err != nil {
				t.Fatal(err)
			}
			cfg, paths, err := NewRuntimeConfig("local", repo)
			if err != nil {
				t.Fatalf("unusable Python must not block configuration/maintenance: %v", err)
			}
			if err := paths.EnsureAllDirs(); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			cli := NewCLI(&out, &out, &fakeRunner{t: t}, filepath.Join(repo, "manager"))
			for _, args := range [][]string{{"status", "--json"}, {"internal", "channel-gateway-down"}} {
				args = append(args, "--repo-root", repo, "--runtime-root", filepath.Join(repo, "runtime"))
				if err := cli.Run(context.Background(), args); err != nil {
					t.Fatalf("maintenance %v depends on Python: %v", args[:2], err)
				}
			}
			runner := &fakeRunner{t: t}
			switch name {
			case "runtime":
				_, err = ensureLocalPythonRuntime(context.Background(), runner, paths, defaultLocalPythonVersion)
			case "algorithm", "python-deps":
				err = NewAlgorithmServiceManager(runner).preparePython(context.Background(), cfg, paths, false)
			case "auth-service":
				err = NewAuthServiceManager(runner).preparePythonEnv(context.Background(), cfg, paths)
			case "channel-gateway":
				err = NewChannelGatewayManager(runner).preparePythonEnv(context.Background(), cfg, paths)
			}
			if err == nil || !strings.Contains(err.Error(), "outside") {
				t.Fatalf("foreign environment was prepared: %v", err)
			}
			runner.assertCommandCount(0)
			if entries, err := os.ReadDir(foreign); err != nil || len(entries) != 0 {
				t.Fatalf("foreign environment was modified: %v, %v", entries, err)
			}
		})
	}
}

func TestPythonEnvironmentProbe(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		python, err = exec.LookPath("python")
	}
	if err != nil {
		t.Skip("Python is required to exercise the real interpreter/SSL probe")
	}
	venv := filepath.Join(t.TempDir(), "venv")
	if out, err := exec.Command(python, "-m", "venv", "--without-pip", venv).CombinedOutput(); err != nil {
		t.Fatalf("create isolated test venv: %v (%s)", err, out)
	}
	python = venvExecutable(venv, "python")
	query := func(code string) string {
		t.Helper()
		out, err := exec.Command(python, "-I", "-c", code).CombinedOutput()
		if err != nil {
			t.Fatalf("read test Python paths: %v (%s)", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	base := query("import sys; print(sys.base_prefix)")
	site := query("import sysconfig; print(sysconfig.get_path('purelib'))")
	certifi := filepath.Join(site, "certifi")
	if err := os.MkdirAll(certifi, 0o755); err != nil {
		t.Fatal(err)
	}
	// Synthetic certifi only supplies a path; certificate parsing uses real OpenSSL.
	if err := os.WriteFile(filepath.Join(certifi, "__init__.py"), []byte("from pathlib import Path\ndef where(): return str(Path(__file__).with_name('cacert.pem'))\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certifi, "cacert.pem"), []byte("invalid certificate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, goos, arch, prefix, base, certificates, want string }{
		{"valid identity", runtime.GOOS, runtime.GOARCH, venv, base, "false", ""},
		{"wrong platform", "foreign-os", runtime.GOARCH, venv, base, "false", "Python platform"},
		{"wrong architecture", runtime.GOOS, "foreign-arch", venv, base, "false", "Python platform"},
		{"wrong venv", runtime.GOOS, runtime.GOARCH, t.TempDir(), base, "false", "does not match venv"},
		{"wrong runtime", runtime.GOOS, runtime.GOARCH, venv, t.TempDir(), "false", "outside runtime"},
		{"invalid certificate", runtime.GOOS, runtime.GOARCH, venv, base, "true", "ssl.SSLError"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := exec.Command(python, "-I", "-c", pythonEnvironmentProbe, tc.goos, tc.arch, tc.prefix, tc.base, tc.certificates).CombinedOutput()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("valid interpreter rejected: %v (%s)", err, out)
				}
			} else if err == nil || !strings.Contains(string(out), tc.want) {
				t.Fatalf("want %q, got %v (%s)", tc.want, err, out)
			}
		})
	}
	// Controlled Windows identity inputs; the interpreter/SSL execution is real,
	// but these cases do not claim to run a Windows interpreter on this host.
	for _, tc := range []struct{ name, build, bits, host, want string }{
		{"WOW64 x86 Python on AMD64", "win32", "4", "AMD64", "Python platform"},
		{"x64 Python on ARM64 host", "win-amd64", "8", "ARM64", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wrapper := `import platform, struct, sys, sysconfig, ssl, pathlib
build, bits, host, prefix, base, code = sys.argv[1:]
sys.platform = 'win32'
sysconfig.get_platform = lambda: build
struct.calcsize = lambda _: int(bits)
platform.machine = lambda: host
sys.argv = ['probe', 'windows', 'amd64', prefix, base, 'false']
exec(code)
`
			out, err := exec.Command(python, "-I", "-c", wrapper, tc.build, tc.bits, tc.host, venv, base, pythonEnvironmentProbe).CombinedOutput()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("matching interpreter rejected: %v (%s)", err, out)
				}
			} else if err == nil || !strings.Contains(string(out), tc.want) {
				t.Fatalf("want %q, got %v (%s)", tc.want, err, out)
			}
		})
	}
}
