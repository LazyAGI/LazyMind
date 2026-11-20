package desktopupdate

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// VerifyUnsignedMacBundle checks the deliberately distinct bundle identity as
// well as the ad-hoc seal. The signed policy separately binds the ZIP bytes.
func VerifyUnsignedMacBundle(ctx context.Context, bundle string, cfg Config) error {
	invalid := errors.New("unsigned_test_bundle")
	if !UnsignedTestBuild || runtime.GOOS != "darwin" || ValidateConfig(cfg) != nil ||
		filepath.Base(bundle) != TestAppName+".app" || NoUnsignedPathAliases(bundle) != nil {
		return invalid
	}
	plist := filepath.Join(bundle, "Contents", "Info.plist")
	metadata := filepath.Join(bundle, "Contents", "Resources", "app-update.yml")
	if NoUnsignedPathAliases(plist) != nil || NoUnsignedPathAliases(metadata) != nil {
		return invalid
	}
	if err := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", bundle).Run(); err != nil {
		return invalid
	}
	display, err := exec.CommandContext(ctx, "/usr/bin/codesign", "-dv", "--verbose=4", bundle).CombinedOutput()
	if err != nil || !strings.Contains(string(display), "\nSignature=adhoc\n") {
		return invalid
	}
	raw, err := exec.CommandContext(ctx, "/usr/bin/plutil", "-convert", "json", "-o", "-", plist).Output()
	var info struct{ CFBundleIdentifier, CFBundleExecutable, CFBundleShortVersionString string }
	if err != nil || len(raw) > 65536 || json.Unmarshal(raw, &info) != nil || info.CFBundleIdentifier != TestAppID ||
		info.CFBundleExecutable != TestAppName || info.CFBundleShortVersionString != cfg.Version {
		return invalid
	}
	file, err := os.Open(metadata)
	if err != nil {
		return invalid
	}
	defer file.Close()
	raw, err = io.ReadAll(io.LimitReader(file, 65537))
	type identity struct {
		Protocol                                                  int
		ReleaseID, Version, Mode, Platform, Arch, SigningIdentity string
	}
	var meta struct {
		Lazymind            identity
		UpdaterCacheDirName string
	}
	expected := identity{cfg.UpdaterProtocol, cfg.ReleaseID, cfg.Version, cfg.Mode, cfg.Platform, cfg.Arch, cfg.SigningIdentity}
	if err != nil || len(raw) > 65536 || json.Unmarshal(raw, &meta) != nil || meta.Lazymind != expected ||
		meta.UpdaterCacheDirName != "LazyMind-"+cfg.TrustDomain+"-"+cfg.Audience+"-"+cfg.Channel {
		return invalid
	}
	return nil
}
