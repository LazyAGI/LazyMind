// Package unsignedupdate is linked only by the separately built test installer.
package unsignedupdate

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/releaseversion"
)

type Request struct {
	Release     desktopupdate.Release `json:"release"`
	PackagePath string                `json:"packagePath"`
}
type Plan struct {
	Request Request                       `json:"request"`
	Source  string                        `json:"source"`
	Parent  desktopupdate.ProcessIdentity `json:"parent"`
	Stage   string                        `json:"stage"`
}
type Installer struct {
	Config      desktopupdate.Config
	Paths       desktopupdate.TestPaths
	Verify      func(context.Context, string) error
	Inspect     func(context.Context, string) error
	Launch      func(context.Context, string) error
	SameProcess func(desktopupdate.ProcessIdentity) (bool, error)
}

func within(name, root string) bool {
	rel, err := filepath.Rel(root, name)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func (i Installer) validate() error {
	if err := desktopupdate.ValidateConfig(i.Config); err != nil {
		return err
	}
	if i.Config.Mode != "unsigned-test" || i.Config.Platform != "darwin" || i.Config.SigningIdentity != "adhoc" {
		return fmt.Errorf("unsigned_test_disabled")
	}
	expected, err := desktopupdate.TestPathsForAccount("darwin", "lazymind-test", i.Paths.Home)
	if err != nil || expected != i.Paths {
		return fmt.Errorf("unsigned_test_scope")
	}
	for _, p := range []string{i.Paths.Application, i.Paths.Updater} {
		if err := desktopupdate.NoSymlinkAncestors(p); err != nil {
			return err
		}
	}
	return nil
}

func verifyFile(file string, artifact desktopupdate.Artifact) error {
	info, err := os.Lstat(file)
	if err != nil || !info.Mode().IsRegular() || artifact.Size <= 0 || info.Size() != artifact.Size {
		return fmt.Errorf("unsigned_test_package")
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, artifact.Size+1))
	if err != nil || n != artifact.Size || hex.EncodeToString(h.Sum(nil)) != artifact.SHA256 {
		return fmt.Errorf("unsigned_test_package")
	}
	return nil
}

func (i Installer) verifyBundle(ctx context.Context, bundle, version, release string, candidate bool) error {
	marker := filepath.Join(bundle, "Contents", "Resources", "unsigned-test.json")
	if err := desktopupdate.NoSymlinkAncestors(marker); err != nil {
		return err
	}
	raw, err := os.ReadFile(marker)
	if err != nil || len(raw) > 8192 {
		return fmt.Errorf("unsigned_test_identity")
	}
	var m struct {
		AppID                                                                                     string `json:"appId"`
		Mode, Version, ReleaseID, Platform, Arch, SigningIdentity, TrustDomain, Audience, Channel string
	}
	if json.Unmarshal(raw, &m) != nil || m.AppID != desktopupdate.TestAppID || m.Mode != "unsigned-test" || m.Version != version || m.ReleaseID != release || m.Platform != i.Config.Platform || m.Arch != i.Config.Arch || m.SigningIdentity != "adhoc" || m.TrustDomain != i.Config.TrustDomain || m.Audience != i.Config.Audience || m.Channel != i.Config.Channel {
		return fmt.Errorf("unsigned_test_identity")
	}
	if candidate {
		return i.Verify(ctx, bundle)
	}
	return i.Inspect(ctx, bundle)
}

func writePlan(dir string, plan Plan) error {
	raw, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".plan-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(name, filepath.Join(dir, "plan.json")); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

// Extract regular files before links. No file is ever created through a ZIP
// symlink; every link is then resolved inside the one permitted bundle.
func extract(ctx context.Context, file, destination string) error {
	z, err := zip.OpenReader(file)
	if err != nil {
		return err
	}
	defer z.Close()
	names := map[string]bool{}
	links := map[string]string{}
	root := filepath.Join(destination, desktopupdate.TestAppName+".app")
	for _, entry := range z.File {
		name := filepath.Clean(filepath.FromSlash(entry.Name))
		if strings.Contains(entry.Name, "\\") || filepath.IsAbs(name) || !within(filepath.Join(destination, name), root) || names[name] {
			return fmt.Errorf("unsigned_test_zip_path")
		}
		names[name] = true
		if entry.Mode()&os.ModeSymlink != 0 {
			r, err := entry.Open()
			if err != nil {
				return err
			}
			b, err := io.ReadAll(io.LimitReader(r, 4097))
			r.Close()
			if err != nil || len(b) > 4096 || filepath.IsAbs(string(b)) || strings.ContainsAny(string(b), "\\\x00") {
				return fmt.Errorf("unsigned_test_zip_link")
			}
			links[name] = string(b)
		} else if !entry.FileInfo().IsDir() && !entry.Mode().IsRegular() {
			return fmt.Errorf("unsigned_test_zip_type")
		}
	}
	for name := range names {
		for parent := filepath.Dir(name); parent != "."; parent = filepath.Dir(parent) {
			if _, ok := links[parent]; ok {
				return fmt.Errorf("unsigned_test_zip_link")
			}
		}
	}
	for name := range links {
		if !internalLink(name, links) {
			return fmt.Errorf("unsigned_test_zip_link")
		}
	}
	if err := desktopupdate.NoSymlinkAncestors(destination); err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		// ditto restores AppleDouble metadata instead of creating unsealed ._
		// resources. All entry names and the complete link graph were checked above.
		return exec.CommandContext(ctx, "/usr/bin/ditto", "-x", "-k", file, destination).Run()
	}
	for _, entry := range z.File {
		name := filepath.Clean(filepath.FromSlash(entry.Name))
		target := filepath.Join(destination, name)
		if _, link := links[name]; link {
			continue
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, entry.Mode().Perm()&0777)
		if err != nil {
			return err
		}
		in, err := entry.Open()
		if err != nil {
			out.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		in.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	for name, value := range links {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(destination, name)), 0700); err != nil {
			return err
		}
		if err := os.Symlink(value, filepath.Join(destination, name)); err != nil {
			return err
		}
	}
	for name := range links {
		resolved, err := filepath.EvalSymlinks(filepath.Join(destination, name))
		if err != nil || !within(resolved, root) {
			return fmt.Errorf("unsigned_test_zip_link")
		}
	}
	return nil
}

func internalLink(name string, links map[string]string) bool {
	pending := strings.Split(filepath.ToSlash(name), "/")
	var stack []string
	hops := 0
	for len(pending) > 0 {
		part := pending[0]
		pending = pending[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			if len(stack) <= 1 {
				return false
			}
			stack = stack[:len(stack)-1]
			continue
		}
		stack = append(stack, part)
		if stack[0] != desktopupdate.TestAppName+".app" {
			return false
		}
		if value, ok := links[filepath.Join(stack...)]; ok {
			hops++
			if hops > 64 {
				return false
			}
			stack = stack[:len(stack)-1]
			pending = append(strings.Split(filepath.ToSlash(value), "/"), pending...)
		}
	}
	return len(stack) > 0
}

func (i Installer) Prepare(ctx context.Context, request Request, parent desktopupdate.ProcessIdentity) (string, error) {
	if err := i.validate(); err != nil {
		return "", err
	}
	order, err := releaseversion.Compare(request.Release.Version, i.Config.Version)
	if err != nil || order <= 0 || request.Release.Platform != i.Config.Platform || request.Release.Arch != i.Config.Arch || request.Release.SigningIdentity != "adhoc" || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`).MatchString(request.Release.ID) {
		return "", fmt.Errorf("unsigned_test_identity")
	}
	if !within(request.PackagePath, filepath.Join(i.Paths.Updater, "downloads")) || desktopupdate.NoSymlinkAncestors(request.PackagePath) != nil {
		return "", fmt.Errorf("unsigned_test_package_scope")
	}
	if err := i.verifyBundle(ctx, i.Paths.Application, i.Config.Version, i.Config.ReleaseID, false); err != nil {
		return "", err
	}
	if err := verifyFile(request.PackagePath, request.Release.Package); err != nil {
		return "", err
	}
	root := filepath.Join(i.Paths.Updater, "installs")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(root, "install-")
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(dir)
		}
	}()
	in, err := os.Open(request.PackagePath)
	if err != nil {
		return "", err
	}
	defer in.Close()
	copyPath := filepath.Join(dir, "package.zip")
	out, err := os.OpenFile(copyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := verifyFile(copyPath, request.Release.Package); err != nil {
		return "", err
	}
	if err := extract(ctx, copyPath, filepath.Join(dir, "staged")); err != nil {
		return "", err
	}
	if err := i.verifyBundle(ctx, filepath.Join(dir, "staged", desktopupdate.TestAppName+".app"), request.Release.Version, request.Release.ID, true); err != nil {
		return "", err
	}
	if err := writePlan(dir, Plan{request, i.Config.Version, parent, "prepared"}); err != nil {
		return "", err
	}
	ok = true
	return filepath.Base(dir), nil
}

func (i Installer) Install(ctx context.Context, id string, caller desktopupdate.ProcessIdentity, accepted func() error) (err error) {
	if err = i.validate(); err != nil {
		return err
	}
	if !regexp.MustCompile(`^install-[0-9]+$`).MatchString(id) {
		return fmt.Errorf("unsigned_test_plan")
	}
	dir := filepath.Join(i.Paths.Updater, "installs", id)
	if err = desktopupdate.NoSymlinkAncestors(filepath.Join(dir, "plan.json")); err != nil {
		return err
	}
	release, err := desktopupdate.LockUnsignedInstaller(i.Paths.Updater)
	if err != nil {
		return err
	}
	defer release()
	raw, err := os.ReadFile(filepath.Join(dir, "plan.json"))
	if err != nil || len(raw) > 16384 {
		return fmt.Errorf("unsigned_test_plan")
	}
	var plan Plan
	if json.Unmarshal(raw, &plan) != nil || plan.Stage != "prepared" || plan.Source != i.Config.Version {
		return fmt.Errorf("unsigned_test_plan")
	}
	order, compareErr := releaseversion.Compare(plan.Request.Release.Version, i.Config.Version)
	if compareErr != nil || order <= 0 || plan.Parent.PID <= 0 || plan.Parent.StartID == 0 || caller != plan.Parent || plan.Request.Release.Platform != i.Config.Platform || plan.Request.Release.Arch != i.Config.Arch || plan.Request.Release.SigningIdentity != "adhoc" {
		return fmt.Errorf("unsigned_test_plan")
	}
	defer func() {
		if err != nil && plan.Stage != "prepared" {
			plan.Stage = "repair"
			_ = writePlan(dir, plan)
		}
	}()
	scope := desktopupdate.UpdateScope{Installation: filepath.Join(i.Paths.Application, "Contents", "Resources", "runtime"), DataRoots: []string{i.Paths.Runtime}}
	err = desktopupdate.WithUnsignedReplacement(ctx, i.Paths.Updater, scope, caller, func() error {
		alive, err := i.SameProcess(caller)
		if err != nil {
			return err
		}
		if !alive {
			return fmt.Errorf("unsigned_test_stale_coordinator")
		}
		plan.Stage = "installing"
		if err = writePlan(dir, plan); err != nil {
			return err
		}
		if err = accepted(); err != nil {
			return err
		}
		for {
			alive, err = i.SameProcess(plan.Parent)
			if err != nil {
				return err
			}
			if !alive {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
		}
		if err = i.verifyBundle(ctx, i.Paths.Application, i.Config.Version, i.Config.ReleaseID, false); err != nil {
			return err
		}
		candidate := filepath.Join(dir, "staged", desktopupdate.TestAppName+".app")
		if err = verifyFile(filepath.Join(dir, "package.zip"), plan.Request.Release.Package); err != nil {
			return err
		}
		if err = os.RemoveAll(filepath.Join(dir, "staged")); err != nil {
			return err
		}
		if err = extract(ctx, filepath.Join(dir, "package.zip"), filepath.Join(dir, "staged")); err != nil {
			return err
		}
		if err = i.verifyBundle(ctx, candidate, plan.Request.Release.Version, plan.Request.Release.ID, true); err != nil {
			return err
		}
		backup := filepath.Join(dir, "previous.app")
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = os.Rename(i.Paths.Application, backup); err != nil {
			return err
		}
		if err = os.Rename(candidate, i.Paths.Application); err != nil {
			_ = os.Rename(backup, i.Paths.Application)
			return err
		}
		plan.Stage = "replaced"
		return writePlan(dir, plan)
	})
	if err != nil {
		return err
	}
	// Release startup admission only after replacement. New packaged code can
	// now start; a stale Electron process is rejected by its cached build ID.
	if err = i.Launch(ctx, i.Paths.Application); err != nil {
		return err
	}
	plan.Stage = "launched"
	return writePlan(dir, plan)
}

func InspectMacBundle(ctx context.Context, bundle string) error {
	plist := filepath.Join(bundle, "Contents", "Info.plist")
	marker := filepath.Join(bundle, "Contents", "Resources", "unsigned-test.json")
	if desktopupdate.NoSymlinkAncestors(plist) != nil || desktopupdate.NoSymlinkAncestors(marker) != nil {
		return fmt.Errorf("unsigned_test_bundle")
	}
	b, err := os.ReadFile(marker)
	var identity struct{ Version string }
	if err != nil || len(b) > 8192 || json.Unmarshal(b, &identity) != nil || identity.Version == "" {
		return fmt.Errorf("unsigned_test_bundle")
	}
	raw, err := exec.CommandContext(ctx, "/usr/bin/plutil", "-convert", "json", "-o", "-", plist).Output()
	var info struct{ CFBundleIdentifier, CFBundleExecutable, CFBundleShortVersionString string }
	if err != nil || len(raw) > 65536 || json.Unmarshal(raw, &info) != nil || info.CFBundleIdentifier != desktopupdate.TestAppID || info.CFBundleExecutable != desktopupdate.TestAppName || info.CFBundleShortVersionString != identity.Version {
		return fmt.Errorf("unsigned_test_bundle")
	}
	return nil
}

func VerifyMacBundle(ctx context.Context, bundle string) error {
	if err := InspectMacBundle(ctx, bundle); err != nil {
		return err
	}
	return exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", bundle).Run()
}

// A cancelled preparation can release its private staged copy. Once handoff
// starts the record is retained for repair and must never be discarded here.
func (i Installer) Discard(id string) error {
	if err := i.validate(); err != nil {
		return err
	}
	if !regexp.MustCompile(`^install-[0-9]+$`).MatchString(id) {
		return fmt.Errorf("unsigned_test_plan")
	}
	dir := filepath.Join(i.Paths.Updater, "installs", id)
	if err := desktopupdate.NoSymlinkAncestors(filepath.Join(dir, "plan.json")); err != nil {
		return err
	}
	release, err := desktopupdate.LockUnsignedInstaller(i.Paths.Updater)
	if err != nil {
		return err
	}
	defer release()
	b, err := os.ReadFile(filepath.Join(dir, "plan.json"))
	if err != nil {
		return err
	}
	var plan Plan
	if len(b) > 16384 || json.Unmarshal(b, &plan) != nil || plan.Stage != "prepared" || plan.Source != i.Config.Version {
		return fmt.Errorf("unsigned_test_plan")
	}
	return os.RemoveAll(dir)
}
