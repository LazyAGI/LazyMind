//go:build darwin && lazymind_unsigned_test

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	update "github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

func TestInPlaceVerifyIdentityFromInstalledHelper(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	userApps := filepath.Join(home, "Applications")
	systemApps := filepath.Join(root, "system", "Applications")
	cfg := update.Config{Mode: "unsigned-test", TrustDomain: "inplace-41-" + strings.Repeat("a", 64),
		Audience: "personal", Channel: "personal-test", SigningIdentity: "adhoc", UpdaterProtocol: 2,
		Version: "0.5.301", ReleaseID: "darwin-" + runtime.GOARCH + "-0.5.301", Platform: "darwin", Arch: runtime.GOARCH,
		PolicyURL:      "https://modelscope.cn/datasets/fixture/repo/resolve/master/updates/unsigned-test/demo/in-place/round-41/policy.json",
		AllowedOrigins: []string{"https://modelscope.cn"}, PublicKeys: map[string]string{"fixture": base64.StdEncoding.EncodeToString(make([]byte, 32))}}
	encode := func(v any) []byte {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	write := func(file string, data []byte, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, data, mode); err != nil {
			t.Fatal(err)
		}
	}

	// Compile only the OS roots into the private executable. The command sees
	// its real os.Executable(), the signed bundle and its embedded configuration.
	locator, err := filepath.Abs("../../internal/desktopupdate/unsigned_locations.go")
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(root, "unsigned_locations.go")
	write(replacement, []byte(fmt.Sprintf(`package desktopupdate
func UnsignedApplicationLocations() (UnsignedTestPaths, string, error) {
	return UnsignedTestPaths{Home:%q, Application:%q}, %q, nil
}`, home, filepath.Join(userApps, "LazyMind.app"), systemApps)), 0600)
	overlay := filepath.Join(root, "overlay.json")
	write(overlay, encode(map[string]any{"Replace": map[string]string{locator: replacement}}), 0600)
	binary := filepath.Join(root, "built-helper")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-overlay", overlay,
		"-tags", "lazymind_unsigned_test", "-ldflags", "-X main.embeddedConfig="+base64.StdEncoding.EncodeToString(encode(cfg)), "-o", binary, ".")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build installed helper: %v %s", err, output)
	}
	helperBytes, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.ReadFile("/usr/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, apps, product, id string
		enabled                 bool
	}{
		{"user-install", userApps, "LazyMind", "ai.lazymind.desktop", true},
		{"system-install", systemApps, "LazyMind", "ai.lazymind.desktop", true},
		{"outside-both-roots", filepath.Join(root, "downloads"), "LazyMind", "ai.lazymind.desktop", false},
		{"isolated-product", userApps, "LazyMind Test", "ai.lazymind.desktop.test", false},
		{"isolated-identifier-at-valid-path", userApps, "LazyMind", "ai.lazymind.desktop.test", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := filepath.Join(tc.apps, tc.product+".app")
			t.Cleanup(func() { os.RemoveAll(app) })
			contents := filepath.Join(app, "Contents")
			write(filepath.Join(contents, "MacOS", tc.product), executable, 0700)
			plist := fmt.Sprintf(`<plist version="1.0"><dict><key>CFBundleIdentifier</key><string>%s</string><key>CFBundleExecutable</key><string>%s</string><key>CFBundleShortVersionString</key><string>%s</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>`, tc.id, tc.product, cfg.Version)
			write(filepath.Join(contents, "Info.plist"), []byte(plist), 0600)
			native := map[string]any{"protocol": 2, "releaseId": cfg.ReleaseID, "version": cfg.Version, "platform": cfg.Platform, "arch": cfg.Arch, "mode": cfg.Mode, "signingIdentity": cfg.SigningIdentity}
			write(filepath.Join(contents, "Resources", "app-update.yml"), encode(map[string]any{
				"lazymind": native, "updaterCacheDirName": "LazyMind-" + cfg.TrustDomain + "-" + cfg.Audience + "-" + cfg.Channel}), 0600)
			helper := filepath.Join(contents, "Resources", "runtime", "bin", "lazymind-update-helper")
			write(helper, helperBytes, 0700)
			for _, flags := range [][]string{{"--force", "--deep", "--sign", "-", app}, {"--verify", "--deep", "--strict", app}} {
				if out, err := exec.CommandContext(ctx, "/usr/bin/codesign", flags...).CombinedOutput(); err != nil {
					t.Fatalf("codesign: %v %s", err, out)
				}
			}
			for _, action := range []string{"config", "verify-identity"} {
				out, err := exec.CommandContext(ctx, helper, action).Output()
				var result struct {
					Status, Reason string
					Config         update.Config
				}
				if json.Unmarshal(out, &result) != nil {
					t.Fatalf("%s did not return its structured response: %v %q", action, err, out)
				}
				if action == "config" || tc.enabled {
					if err != nil || result.Status != "enabled" || !reflect.DeepEqual(result.Config, cfg) {
						t.Fatalf("%s must enable the embedded configuration: %v %s", action, err, out)
					}
				} else if err == nil || result.Status != "check_failed" || result.Reason != "build_identity_mismatch" {
					t.Fatalf("unauthorized install accepted or misclassified: %v %s", err, out)
				}
			}
		})
	}
}
