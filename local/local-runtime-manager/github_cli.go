package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"debug/macho"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const localGitHubCLIEnvVar = "LAZYMIND_GITHUB_CLI_PATH"

func localGitHubCLIPath(paths RuntimePaths) string {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		return ""
	}
	if explicit := os.Getenv(localGitHubCLIEnvVar); explicit != "" {
		return explicit
	}
	if paths.BuildRoot == "" {
		return ""
	}
	return localGitHubCLIPathForPlatform(paths, runtime.GOOS, runtime.GOARCH)
}

func localGitHubCLIPathForPlatform(paths RuntimePaths, goos, arch string) string {
	name := "gh"
	if goos == "windows" {
		name = "github-auth-helper.exe"
	}
	return filepath.Join(paths.BuildRoot, "deps", "github-auth", goos+"-"+arch, "bin", name)
}

func prepareLocalGitHubCLI(ctx context.Context, runner CommandRunner, paths RuntimePaths) error {
	if localGitHubCLIPath(paths) == "" || os.Getenv(localGitHubCLIEnvVar) != "" {
		return nil
	}
	return prepareLocalGitHubCLIForPlatform(ctx, runner, paths, runtime.GOOS, runtime.GOARCH)
}

func prepareLocalGitHubCLIForPlatform(ctx context.Context, runner CommandRunner, paths RuntimePaths, goos, arch string) error {
	if (goos != "darwin" && goos != "windows") || (arch != "arm64" && arch != "amd64") {
		return fmt.Errorf("unsupported local GitHub authorization platform: %s/%s", goos, arch)
	}
	var release struct {
		Version string            `json:"version"`
		SHA256  map[string]string `json:"archive_sha256"`
	}
	body, err := os.ReadFile(filepath.Join(paths.RepoRoot, "backend", "auth-service", "github-cli-release.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return err
	}
	if release.Version == "" || strings.ContainsAny(release.Version, "/\\") {
		return fmt.Errorf("invalid GitHub CLI release version")
	}
	binary := localGitHubCLIPathForPlatform(paths, goos, arch)
	target := filepath.Dir(filepath.Dir(binary))
	identity := release.Version + "\n" + release.SHA256[goos+"-"+arch] + "\n"
	if goos == "darwin" && localGitHubCLICacheMatches(binary, identity, arch) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(target), ".github-auth-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	stagedBinary := filepath.Join(stage, "bin", filepath.Base(binary))
	if err := os.MkdirAll(filepath.Dir(stagedBinary), 0755); err != nil {
		return err
	}
	if goos == "darwin" {
		err = installLocalGitHubCLIMac(ctx, stage, stagedBinary, release.Version, release.SHA256[goos+"-"+arch], arch)
	} else {
		env := append(goToolEnv(paths), "CGO_ENABLED=0", "GOOS=windows", "GOARCH="+arch)
		result, buildErr := runner.Run(ctx, Command{Name: "go", Args: []string{"build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-o", stagedBinary, "."},
			Dir: filepath.Join(paths.RepoRoot, "backend", "github-auth-helper"), Env: env})
		if buildErr != nil || result.ExitCode != 0 {
			return fmt.Errorf("build GitHub authorization helper: %v (%s)", buildErr, strings.TrimSpace(result.Stderr))
		}
		err = verifyLocalGitHubHelper(stagedBinary, arch, release.Version)
	}
	if err != nil {
		return err
	}
	digest, err := fileSHA256(stagedBinary)
	if err != nil {
		return err
	}
	if err := os.WriteFile(strings.TrimSuffix(stagedBinary, ".exe")+".sha256", []byte(digest+"\n"), 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, ".release"), []byte(identity), 0644); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Keep the complete previous installation until its replacement has been verified.
	backup := stage + ".previous"
	hadPrevious := true
	if err := os.Rename(target, backup); os.IsNotExist(err) {
		hadPrevious = false
	} else if err != nil {
		return err
	}
	if err := os.Rename(stage, target); err != nil {
		if hadPrevious {
			if restoreErr := os.Rename(backup, target); restoreErr != nil {
				return fmt.Errorf("activate GitHub CLI: %v; restore previous installation from %s: %w", err, backup, restoreErr)
			}
		}
		return err
	}
	if hadPrevious {
		return os.RemoveAll(backup)
	}
	return nil
}

func localGitHubCLICacheMatches(binary, identity, arch string) bool {
	root := filepath.Dir(filepath.Dir(binary))
	marker, err := os.ReadFile(filepath.Join(root, ".release"))
	if err != nil || string(marker) != identity {
		return false
	}
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return false
	}
	if info, err := os.Stat(filepath.Join(root, "licenses", "gh", "LICENSE")); err != nil || !info.Mode().IsRegular() {
		return false
	}
	want, err := os.ReadFile(binary + ".sha256")
	if err != nil {
		return false
	}
	got, err := fileSHA256(binary)
	return err == nil && got == strings.TrimSpace(string(want)) && verifyLocalGitHubCLIMacArchitecture(binary, arch) == nil
}

func installLocalGitHubCLIMac(ctx context.Context, stage, binary, version, digest, arch string) error {
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("missing or invalid GitHub CLI archive checksum for darwin/%s", arch)
	}
	address := fmt.Sprintf("https://github.com/cli/cli/releases/download/v%s/gh_%s_macOS_%s.zip", version, version, arch)
	if proxy := strings.TrimSpace(os.Getenv("GITHUB_PROXY")); proxy != "" {
		address = strings.TrimRight(proxy, "/") + "/" + address
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	client := http.Client{Timeout: 3 * time.Minute}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download GitHub CLI: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download GitHub CLI: HTTP %d", response.StatusCode)
	}
	archive := filepath.Join(stage, "gh.zip")
	file, err := os.Create(archive)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, response.Body)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	actual, err := fileSHA256(archive)
	if err != nil || actual != digest {
		return fmt.Errorf("GitHub CLI archive SHA-256 mismatch")
	}
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer reader.Close()
	license := filepath.Join(stage, "licenses", "gh", "LICENSE")
	if err := os.MkdirAll(filepath.Dir(license), 0755); err != nil {
		return err
	}
	for name, destination := range map[string]string{"bin/gh": binary, "LICENSE": license} {
		entry, err := reader.Open("gh_" + version + "_macOS_" + arch + "/" + name)
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(entry)
		entry.Close()
		if readErr != nil {
			return readErr
		}
		if err := os.WriteFile(destination, data, 0644); err != nil {
			return err
		}
	}
	if err := os.Chmod(binary, 0755); err != nil {
		return err
	}
	if err := verifyLocalGitHubCLIMacArchitecture(binary, arch); err != nil {
		return err
	}
	if err := reader.Close(); err != nil {
		return err
	}
	return os.Remove(archive)
}

func verifyLocalGitHubCLIMacArchitecture(binary, arch string) error {
	file, err := macho.Open(binary)
	if err != nil {
		return fmt.Errorf("read GitHub CLI Mach-O: %w", err)
	}
	defer file.Close()
	want := macho.CpuArm64
	if arch == "amd64" {
		want = macho.CpuAmd64
	}
	if file.Cpu != want {
		return fmt.Errorf("GitHub CLI architecture mismatch: got %s, want %s", file.Cpu, want)
	}
	return nil
}

func verifyLocalGitHubHelper(binary, arch, version string) error {
	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		return fmt.Errorf("read GitHub helper build metadata: %w", err)
	}
	settings := map[string]string{}
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["GOOS"] != "windows" || settings["GOARCH"] != arch {
		return fmt.Errorf("GitHub helper target does not match windows/%s", arch)
	}
	for _, dep := range info.Deps {
		if dep.Path == "github.com/cli/cli/v2" && dep.Version == "v"+version && dep.Replace == nil {
			return nil
		}
	}
	return fmt.Errorf("GitHub helper CLI dependency does not match locked version %s", version)
}
