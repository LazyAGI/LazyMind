package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func coreModelKeyForTest(t *testing.T, profile string) string {
	t.Helper()
	for _, entry := range mustCoreServiceEnv(t, RuntimeConfig{Profile: profile}, RuntimePaths{}) {
		if key, value, ok := strings.Cut(entry, "="); ok && key == "LAZYMIND_MODEL_PROVIDER_SECRET_KEY" {
			return value
		}
	}
	t.Fatal("model credential key is missing")
	return ""
}

func TestLocalCoreReusesExistingDesktopModelCredentialKey(t *testing.T) {
	identityPath := fixtureCredentialDevicePath(t)
	writeCredentialIdentityFixture(t, identityPath, fixtureDesktopIdentity)
	mainPath, err := filepath.Abs(filepath.Join("..", "..", "desktop", "electron", "src", "main.js"))
	if err != nil {
		t.Fatal(err)
	}
	// Execute the existing Electron identity/derivation functions on a virtual identity.
	script := `
const fs = require('node:fs'), path = require('node:path'), crypto = require('node:crypto'), vm = require('node:vm');
const source = fs.readFileSync(process.argv[2], 'utf8');
const start = source.indexOf('function loadOrCreateDesktopCredentialIdentity()');
const marker = 'const desktopCredentialIdentity = loadOrCreateDesktopCredentialIdentity();';
const end = source.indexOf(marker, start) + marker.length;
if (start < 0 || end < marker.length) throw Error('Desktop identity implementation is missing');
const context = vm.createContext({ fs, path, Buffer, ...crypto, desktopCredentialIdentityPath: process.argv[1] });
vm.runInContext(source.slice(start, end), context);
process.stdout.write(vm.runInContext("deriveDesktopCredentialKey(desktopCredentialIdentity, 'model-provider')", context));
`
	output, err := exec.Command("node", "-e", script, identityPath, mainPath).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(string(output))
	if want == "" {
		t.Fatal("Desktop derivation returned no key")
	}
	t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", want)
	if got := coreModelKeyForTest(t, "desktop"); got != want {
		t.Fatal("Desktop override was not forwarded")
	}
	t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", "")
	if got := coreModelKeyForTest(t, "local"); got != want {
		t.Fatal("local mode cannot read model credentials encrypted by Desktop")
	}
	if raw, err := os.ReadFile(identityPath); err != nil || string(raw) != fixtureDesktopIdentity {
		t.Fatal("existing device identity was replaced")
	}
}

func TestCoreModelCredentialExplicitOverrideDoesNotRequireDeviceIdentity(t *testing.T) {
	identityPath := fixtureCredentialDevicePath(t)
	writeCredentialIdentityFixture(t, identityPath, "invalid identity")
	t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", "fixture-explicit-key")
	if got := coreModelKeyForTest(t, "local"); got != "fixture-explicit-key" {
		t.Fatal("explicit model key was not preserved")
	}
}

func TestCoreModelCredentialInvalidIdentityFailsClosed(t *testing.T) {
	identityPath := fixtureCredentialDevicePath(t)
	writeCredentialIdentityFixture(t, identityPath, "invalid identity")
	t.Setenv(modelCredentialKeyEnvVar, "")
	env, err := coreServiceEnv(RuntimeConfig{Profile: "local"}, RuntimePaths{})
	if err == nil || env != nil {
		t.Fatal("invalid identity must not fall back to a default model key")
	}
	if raw, readErr := os.ReadFile(identityPath); readErr != nil || string(raw) != "invalid identity" {
		t.Fatal("invalid existing identity was replaced")
	}
}
