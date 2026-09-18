package providerconnection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFeishuCLIReleaseCoversSupportedPlatforms(t *testing.T) {
	var release struct {
		Version  string            `json:"version"`
		License  string            `json:"license_sha256"`
		Archives map[string]string `json:"archive_sha256"`
	}
	if err := json.Unmarshal(feishuCLIReleaseJSON, &release); err != nil {
		t.Fatal(err)
	}
	if release.Version == "" || release.Version != FeishuCLIVersion {
		t.Fatal("runtime version must come from the release manifest")
	}
	for _, platform := range []string{"darwin-arm64", "windows-amd64", "linux-amd64", "linux-arm64"} {
		digest, err := hex.DecodeString(release.Archives[platform])
		if err != nil || len(digest) != sha256.Size {
			t.Fatalf("invalid checksum for %s", platform)
		}
	}
	digest, err := hex.DecodeString(release.License)
	if err != nil || len(digest) != sha256.Size {
		t.Fatal("invalid license checksum")
	}
}

func TestFeishuCLIRuntimeChecksManifestVersionAndBinaryChecksum(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX script")
	}
	for _, version := range []string{FeishuCLIVersion, "0.0.0-unapproved"} {
		t.Run(version, func(t *testing.T) {
			root := t.TempDir()
			binary := filepath.Join(root, "lark-cli")
			payload := []byte("#!/bin/sh\nprintf '%s\\n' 'lark-cli version " + version + "'\n")
			if err := os.WriteFile(binary, payload, 0o700); err != nil {
				t.Fatal(err)
			}
			profile := filepath.Join(root, "config")
			if err := os.Mkdir(profile, 0o700); err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(payload)
			runner, err := NewFeishuCLIRunner(binary, hex.EncodeToString(hash[:]))
			if err != nil {
				t.Fatal(err)
			}
			err = runner.VerifyVersion(context.Background(), profile)
			if version == FeishuCLIVersion && err != nil {
				t.Fatal(err)
			}
			if version != FeishuCLIVersion && !errors.Is(err, ErrCLIVersionUnsupported) {
				t.Fatalf("unexpected version result: %v", err)
			}
			if err := os.WriteFile(binary, append(payload, []byte("# altered")...), 0o700); err != nil {
				t.Fatal(err)
			}
			if _, err := NewFeishuCLIRunner(binary, hex.EncodeToString(hash[:])); !errors.Is(err, ErrCLIIntegrityMismatch) {
				t.Fatalf("altered binary was not rejected: %v", err)
			}
		})
	}
}
