package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"debug/macho"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func githubCLIMacArchive(t *testing.T, version, arch, binaryArch string, license bool) ([]byte, []byte) {
	t.Helper()
	var executable bytes.Buffer
	cpu := macho.CpuArm64
	if binaryArch == "amd64" {
		cpu = macho.CpuAmd64
	}
	if err := binary.Write(&executable, binary.LittleEndian, macho.FileHeader{Magic: macho.Magic64, Cpu: cpu, Type: macho.TypeExec}); err != nil {
		t.Fatal(err)
	}
	executable.Write(make([]byte, 4)) // Reserved field in the 64-bit Mach-O header.
	executable.WriteString("fixture gh " + version)
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	files := map[string][]byte{"bin/gh": executable.Bytes()}
	if license {
		files["LICENSE"] = []byte("fixture GitHub CLI license")
	}
	for name, content := range files {
		entry, err := writer.Create("gh_" + version + "_macOS_" + arch + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes(), executable.Bytes()
}

func githubCLIReleaseFixture(t *testing.T, paths RuntimePaths, version, arch string, archive []byte) {
	t.Helper()
	digest := sha256.Sum256(archive)
	data, err := json.Marshal(map[string]interface{}{"version": version, "archive_sha256": map[string]string{"darwin-" + arch: hex.EncodeToString(digest[:])}})
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(paths.RepoRoot, "backend", "auth-service")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "github-cli-release.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLocalGitHubCLIMacInstallsVerifiedArchiveAndReusesOffline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macOS executable permission checks require a Unix filesystem")
	}
	for _, arch := range []string{"arm64", "amd64"} {
		t.Run(arch, func(t *testing.T) {
			paths := RuntimePaths{RepoRoot: t.TempDir(), BuildRoot: t.TempDir()}
			archive, executable := githubCLIMacArchive(t, "2.102.1", arch, arch, true)
			githubCLIReleaseFixture(t, paths, "2.102.1", arch, archive)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				want := "/mirror/https://github.com/cli/cli/releases/download/v2.102.1/gh_2.102.1_macOS_" + arch + ".zip"
				if r.RequestURI != want {
					t.Errorf("release URL=%s, want %s", r.RequestURI, want)
				}
				w.Write(archive)
			}))
			t.Cleanup(server.Close)
			t.Setenv("GITHUB_PROXY", server.URL+"/mirror/")
			if err := prepareLocalGitHubCLIForPlatform(context.Background(), nil, paths, "darwin", arch); err != nil {
				t.Fatal(err)
			}
			binary := localGitHubCLIPathForPlatform(paths, "darwin", arch)
			got, err := os.ReadFile(binary)
			if err != nil || !bytes.Equal(got, executable) {
				t.Fatalf("installed executable=%q, error=%v", got, err)
			}
			digest := sha256.Sum256(executable)
			marker, err := os.ReadFile(binary + ".sha256")
			if err != nil || strings.TrimSpace(string(marker)) != hex.EncodeToString(digest[:]) {
				t.Fatalf("binary checksum=%q, error=%v", marker, err)
			}
			info, err := os.Stat(binary)
			if err != nil || info.Mode().Perm()&0111 == 0 {
				t.Fatal("installed executable is not executable")
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(binary)), "licenses", "gh", "LICENSE")); err != nil {
				t.Fatal(err)
			}
			server.Close()
			if err := prepareLocalGitHubCLIForPlatform(context.Background(), nil, paths, "darwin", arch); err != nil {
				t.Fatalf("valid cache requires network: %v", err)
			}
		})
	}
}

func TestLocalGitHubCLIMacRepairsCorruptCacheAndUpgradesRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macOS executable permission checks require a Unix filesystem")
	}
	paths := RuntimePaths{RepoRoot: t.TempDir(), BuildRoot: t.TempDir()}
	archive, _ := githubCLIMacArchive(t, "2.102.1", "arm64", "arm64", true)
	githubCLIReleaseFixture(t, paths, "2.102.1", "arm64", archive)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.Write(archive) }))
	defer server.Close()
	t.Setenv("GITHUB_PROXY", server.URL)
	prepare := func() {
		t.Helper()
		if err := prepareLocalGitHubCLIForPlatform(context.Background(), nil, paths, "darwin", "arm64"); err != nil {
			t.Fatal(err)
		}
	}
	prepare()
	binary := localGitHubCLIPathForPlatform(paths, "darwin", "arm64")
	if err := os.WriteFile(binary, []byte("corrupt"), 0755); err != nil {
		t.Fatal(err)
	}
	prepare()
	if err := os.Chmod(binary, 0644); err != nil {
		t.Fatal(err)
	}
	prepare()
	archive, _ = githubCLIMacArchive(t, "2.103.0", "arm64", "arm64", true)
	githubCLIReleaseFixture(t, paths, "2.103.0", "arm64", archive)
	prepare()
	if requests != 4 {
		t.Fatalf("download requests=%d, want initial, corruption, permissions, upgrade", requests)
	}
	data, err := os.ReadFile(binary)
	if err != nil || !bytes.Contains(data, []byte("2.103.0")) {
		t.Fatalf("release was not upgraded: %q (%v)", data, err)
	}
}

func TestLocalGitHubCLIFailedPreparationPreservesPreviousInstallation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macOS executable permission checks require a Unix filesystem")
	}
	for _, failure := range []string{"checksum", "architecture", "license", "network", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			paths := RuntimePaths{RepoRoot: t.TempDir(), BuildRoot: t.TempDir()}
			archive, oldExecutable := githubCLIMacArchive(t, "2.102.1", "arm64", "arm64", true)
			githubCLIReleaseFixture(t, paths, "2.102.1", "arm64", archive)
			status := http.StatusOK
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); w.Write(archive) }))
			defer server.Close()
			t.Setenv("GITHUB_PROXY", server.URL)
			if err := prepareLocalGitHubCLIForPlatform(context.Background(), nil, paths, "darwin", "arm64"); err != nil {
				t.Fatal(err)
			}
			binaryArch := "arm64"
			if failure == "architecture" {
				binaryArch = "amd64"
			}
			archive, _ = githubCLIMacArchive(t, "2.103.0", "arm64", binaryArch, failure != "license")
			githubCLIReleaseFixture(t, paths, "2.103.0", "arm64", archive)
			if failure == "checksum" {
				archive = []byte("corrupted in transit")
			}
			if failure == "network" {
				status = http.StatusBadGateway
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if failure == "cancelled" {
				cancel()
			}
			if err := prepareLocalGitHubCLIForPlatform(ctx, nil, paths, "darwin", "arm64"); err == nil {
				t.Fatal("invalid preparation succeeded")
			}
			binary := localGitHubCLIPathForPlatform(paths, "darwin", "arm64")
			got, err := os.ReadFile(binary)
			if err != nil || !bytes.Equal(got, oldExecutable) {
				t.Fatalf("previous valid installation changed: %v", err)
			}
			temporary, err := filepath.Glob(filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(binary))), ".github-auth-*"))
			if err != nil || len(temporary) != 0 {
				t.Fatalf("left partial installation: %v (%v)", temporary, err)
			}
		})
	}
}

type githubCLICommandRunner func(context.Context, Command) (CommandResult, error)

func (r githubCLICommandRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	return r(ctx, command)
}

func TestLocalGitHubCLIWindowsBuildUsesHelperAndRejectsFailedOrInvalidOutput(t *testing.T) {
	for _, failedBuild := range []bool{true, false} {
		t.Run(map[bool]string{true: "build-failed", false: "invalid-metadata"}[failedBuild], func(t *testing.T) {
			paths := RuntimePaths{RepoRoot: t.TempDir(), BuildRoot: t.TempDir()}
			githubCLIReleaseFixture(t, paths, "2.102.1", "arm64", nil)
			binary := localGitHubCLIPathForPlatform(paths, "windows", "amd64")
			if err := os.MkdirAll(filepath.Dir(binary), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(binary, []byte("previous verified helper"), 0755); err != nil {
				t.Fatal(err)
			}
			runner := githubCLICommandRunner(func(ctx context.Context, command Command) (CommandResult, error) {
				if command.Name != "go" || command.Dir != filepath.Join(paths.RepoRoot, "backend", "github-auth-helper") ||
					len(command.Args) != 7 || !reflect.DeepEqual(command.Args[:5], []string{"build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-o"}) || command.Args[6] != "." {
					t.Fatalf("wrong helper build command: %+v", command)
				}
				for _, want := range []string{"CGO_ENABLED=0", "GOOS=windows", "GOARCH=amd64"} {
					assertEnvContains(t, command.Env, want)
				}
				for _, item := range goToolEnv(paths) {
					assertEnvContains(t, command.Env, item)
				}
				if err := os.WriteFile(command.Args[5], []byte("invalid helper executable"), 0755); err != nil {
					t.Fatal(err)
				}
				if failedBuild {
					return CommandResult{ExitCode: 1, Stderr: "fixture build unavailable"}, errors.New("build failed")
				}
				return CommandResult{}, nil
			})
			if err := prepareLocalGitHubCLIForPlatform(context.Background(), runner, paths, "windows", "amd64"); err == nil {
				t.Fatal("failed or unverified helper was installed")
			}
			got, err := os.ReadFile(binary)
			if err != nil || string(got) != "previous verified helper" {
				t.Fatalf("previous helper changed: %q (%v)", got, err)
			}
		})
	}
}

func TestLocalGitHubCLIExplicitPathAndEmptyBuildRootSkipPreparation(t *testing.T) {
	t.Setenv(localGitHubCLIEnvVar, "")
	if localGitHubCLIPath(RuntimePaths{}) != "" {
		t.Fatal("empty build root must not create a relative CLI path")
	}
	runner := githubCLICommandRunner(func(context.Context, Command) (CommandResult, error) {
		t.Fatal("explicit CLI path must skip managed preparation")
		return CommandResult{}, nil
	})
	explicit := filepath.Join(t.TempDir(), "custom-cli")
	t.Setenv(localGitHubCLIEnvVar, explicit)
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		if localGitHubCLIPath(RuntimePaths{}) != explicit {
			t.Fatal("explicit CLI path was changed")
		}
	}
	if err := prepareLocalGitHubCLI(context.Background(), runner, RuntimePaths{}); err != nil {
		t.Fatal(err)
	}
}
