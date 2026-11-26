//go:build lazymind_unsigned_test

package desktopupdate

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func unsignedConfig(t *testing.T) Config {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return Config{
		Mode: "unsigned-test", TrustDomain: "inplace-417-" + strings.Repeat("a", 64), Audience: "fork-test", Channel: "personal-test",
		SigningIdentity: "adhoc", Platform: "darwin", Arch: "arm64", ReleaseID: "darwin-arm64-0.5.210", Version: "0.5.210", UpdaterProtocol: 2,
		PolicyURL:      "https://modelscope.cn/datasets/example/releases/resolve/master/updates/unsigned-test/in-place/round-417/policy.json",
		AllowedOrigins: []string{"https://modelscope.cn"}, PublicKeys: map[string]string{"test": base64.StdEncoding.EncodeToString(public)},
	}
}

func TestUnsignedV2ConfigRequiresInPlaceTestPolicy(t *testing.T) {
	valid := unsignedConfig(t)
	if err := ValidateConfig(valid); err != nil {
		t.Fatalf("in-place v2 config rejected: %v", err)
	}
	for name, change := range map[string]func(*Config){
		"official":  func(c *Config) { c.Mode, c.Audience, c.Channel = "official", "official", "stable" },
		"version 1": func(c *Config) { c.UpdaterProtocol = 1 },
		"legacy isolated policy": func(c *Config) {
			c.TrustDomain = "lazymind-test"
			c.PolicyURL = "https://modelscope.cn/datasets/example/releases/resolve/master/updates/unsigned-test/policy.json"
		},
		"isolated round at same run": func(c *Config) {
			c.TrustDomain = "round-417-" + strings.Repeat("a", 64)
			c.PolicyURL = strings.Replace(c.PolicyURL, "/in-place/", "/", 1)
		},
		"old domain at new URL": func(c *Config) { c.TrustDomain = "round-417-" + strings.Repeat("a", 64) },
		"different round URL":   func(c *Config) { c.PolicyURL = strings.Replace(c.PolicyURL, "round-417", "round-418", 1) },
		"production URL": func(c *Config) {
			c.PolicyURL = "https://modelscope.cn/datasets/example/releases/resolve/master/updates/policy.json"
		},
		"path alias": func(c *Config) {
			c.PolicyURL = "https://modelscope.cn/datasets/example/releases/resolve/master/updates/unsigned-test/../policy.json"
		},
		"HTTP": func(c *Config) {
			c.PolicyURL = "http://modelscope.cn/datasets/example/releases/resolve/master/updates/unsigned-test/in-place/round-417/policy.json"
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := valid
			change(&c)
			if err := ValidateConfig(c); err == nil {
				t.Fatal("test helper accepted configuration outside its in-place test round")
			}
		})
	}
}

func TestVerifyUnsignedV2MacBundle(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS code-signing tools are required")
	}
	c := unsignedConfig(t)
	c.Arch = runtime.GOARCH
	// /var itself is a symlink on macOS, so create the fixture under its
	// canonical path so the signature subject has no directory aliases.
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(directory, "LazyMind.app")
	signInPlaceFixtureBundle(t, bundle, c)
	if err := VerifyUnsignedMacBundle(t.Context(), bundle, c); err != nil {
		t.Fatal(err)
	}
	c.ReleaseID = "alternate-id"
	if err := VerifyUnsignedMacBundle(t.Context(), bundle, c); err == nil {
		t.Fatal("valid ad-hoc signature did not enforce signed release identity")
	}
}

func signInPlaceFixtureBundle(t *testing.T, bundle string, c Config) {
	t.Helper()
	contents := filepath.Join(bundle, "Contents")
	executable := filepath.Join(contents, "MacOS", "LazyMind")
	write := func(filename string, bytes []byte, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, bytes, mode); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile("/bin/sleep")
	if err != nil {
		t.Fatal(err)
	}
	write(executable, raw, 0700)
	write(filepath.Join(contents, "MacOS", "LazyMindApp"), raw, 0700)
	write(filepath.Join(contents, "Info.plist"), []byte(fmt.Sprintf(
		`<plist version="1.0"><dict><key>CFBundleIdentifier</key><string>%s</string><key>CFBundleExecutable</key><string>%s</string><key>CFBundleShortVersionString</key><string>%s</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>`,
		"ai.lazymind.desktop", "LazyMind", c.Version)), 0600)
	metadata := filepath.Join(contents, "Resources", "app-update.yml")
	write(metadata, []byte(fmt.Sprintf(
		`{"updaterCacheDirName":"LazyMind-%s-%s-%s","lazymind":{"protocol":2,"releaseId":"%s","version":"%s","mode":"unsigned-test","platform":"darwin","arch":"%s","signingIdentity":"adhoc"}}`,
		c.TrustDomain, c.Audience, c.Channel, c.ReleaseID, c.Version, c.Arch)), 0600)
	if output, err := exec.Command("/usr/bin/codesign", "--force", "--deep", "--sign", "-", bundle).CombinedOutput(); err != nil {
		t.Fatalf("sign test fixture: %v: %s", err, output)
	}
}
