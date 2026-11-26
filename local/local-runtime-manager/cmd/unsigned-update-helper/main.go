//go:build darwin && lazymind_unsigned_test

package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	update "github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

var embeddedConfig string

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		_, _ = io.WriteString(os.Stderr, "unsigned test update failed\n")
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	invalid := errors.New("unsigned_test_identity")
	p, systemApplications, err := update.UnsignedApplicationLocations()
	if err != nil {
		return err
	}
	raw, err := base64.StdEncoding.DecodeString(embeddedConfig)
	var cfg update.Config
	if err != nil || json.Unmarshal(raw, &cfg) != nil || update.ValidateConfig(cfg) != nil {
		return invalid
	}
	if len(args) == 2 && (args[0] == "recover" || args[0] == "recover-on-exit") {
		if args[0] == "recover-on-exit" {
			var one [1]byte
			n, e := os.Stdin.Read(one[:])
			if e == nil && n == 1 && one[0] == 1 {
				return nil
			}
			if e != io.EOF || n != 0 {
				return invalid
			}
		}
		return update.RecoverInterruptedUnsignedInstall(ctx, cfg, p, systemApplications, args[1], func(undo, backup, expectedSeal string) error {
			proof := p
			proof.Application = undo
			if err := checkCachedHelper(ctx, proof); err != nil {
				return err
			}
			source, err := sourceSeal(ctx, undo, cfg)
			if err != nil {
				return err
			}
			saved, err := sourceSeal(ctx, backup, cfg)
			if err != nil {
				return err
			}
			if source != saved || source != expectedSeal {
				return invalid
			}
			return nil
		})
	}
	parent, err := update.IdentifyProcess(os.Getppid())
	if err != nil {
		return err
	}
	p.Application, err = unsignedSourceApplication(p, systemApplications, parent)
	if err != nil {
		return err
	}
	if err := checkCachedHelper(ctx, p); err != nil {
		return err
	}
	phase := "pending"
	if len(args) == 2 && args[0] == "install" {
		phase = "installing"
	} else if len(args) != 1 || args[0] != "prepare" {
		return invalid
	}
	r, err := update.AcceptedUnsignedHandoff(ctx, cfg, p, parent, phase)
	if err != nil {
		return err
	}
	p.Runtime = r.RuntimeRoot
	if phase == "pending" {
		if err := update.WithAcceptedUnsignedPreparation(ctx, cfg, p, parent, r, func() error { return prepare(ctx, p, cfg, r) }); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]string{"status": "prepared", "id": r.TransactionID, "releaseId": r.TargetReleaseID})
	}
	if r.TransactionID != args[1] {
		return invalid
	}
	var replacement *localReplacement
	defer func() {
		if replacement != nil {
			replacement.close()
		}
	}()
	if err := update.WithAcceptedUnsignedV2Handoff(ctx, cfg, p, parent, r, func(locked *update.NativeRecord) error {
		var e error
		replacement, e = prepareInstall(ctx, p, cfg, locked)
		return e
	}, func(locked *update.NativeRecord) error {
		return replacement.dispatch(ctx, p, cfg, locked, parent, out, func() error { return update.CheckUnsignedHandoffOffer(ctx, cfg, p.Updater, locked) })
	}); err != nil {
		return err
	}
	if err := exec.CommandContext(ctx, "/usr/bin/open", "-n", p.Application).Run(); err != nil {
		body, _ := json.Marshal(map[string]string{"transactionId": args[1], "reason": "native_handoff_unknown"})
		_, _ = update.NativeUpdateStateCommand(ctx, p.Updater, "repair", body)
		return err
	}
	return nil
}

// The Electron copy in the OS-account updater cache and the helper in the
// selected source bundle must still be the same content-addressed binary.
func checkCachedHelper(ctx context.Context, p update.UnsignedTestPaths) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	source := filepath.Join(p.Application, "Contents", "Resources", "runtime", "bin", "unsigned-update-helper")
	if update.NoUnsignedPathAliases(source) != nil || update.NoUnsignedPathAliases(exe) != nil {
		return errors.New("unsigned_test_identity")
	}
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	digest := sha256.New()
	size, err := io.Copy(digest, file)
	if err != nil {
		return err
	}
	hash := fmt.Sprintf("%x", digest.Sum(nil))
	if exe != filepath.Join(p.Updater, "helpers", hash, "unsigned-update-helper") {
		return errors.New("unsigned_test_identity")
	}
	return update.VerifyArtifact(ctx, exe, update.Artifact{Size: size, SHA256: hash})
}

func stagePath(p update.UnsignedTestPaths, r *update.NativeRecord) string {
	return filepath.Join(p.Updater, "installs", r.TransactionID)
}

func packageArtifact(r *update.NativeRecord) update.Artifact {
	return update.Artifact{Size: r.PackageSize, SHA256: r.PackageSHA256}
}

func prepare(ctx context.Context, p update.UnsignedTestPaths, cfg update.Config, r *update.NativeRecord) error {
	workspace, _, err := localInstallDirectory(p, r.TransactionID)
	if err != nil {
		return err
	}
	defer workspace.Close()
	seal, err := sourceSeal(ctx, p.Application, cfg)
	if err != nil {
		return err
	}
	source, err := os.Stat(p.Application)
	if err != nil {
		return err
	}
	if err := workspace.BindSource(seal, source); err != nil {
		return err
	}
	dir := stagePath(p, r)
	if err := update.NoUnsignedPathAliases(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	bundle := filepath.Join(dir, "package.zip")
	if err := update.NoUnsignedPathAliases(bundle); err != nil {
		return err
	}
	if err := copyVerifiedPackage(ctx, r.PackagePath, bundle, packageArtifact(r)); err != nil {
		return err
	}
	_, err = candidate(ctx, p, cfg, r, filepath.Join(workspace.Directory, "target"))
	return err
}

func copyVerifiedPackage(ctx context.Context, from, to string, artifact update.Artifact) error {
	info, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != artifact.Size {
		return errors.New("unsigned_test_package")
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(to), ".package-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	size, copyErr := io.Copy(out, io.LimitReader(in, artifact.Size+1))
	err = out.Close()
	if copyErr != nil {
		return copyErr
	}
	if err != nil || size != artifact.Size {
		return errors.New("unsigned_test_package")
	}
	if err := update.VerifyArtifact(ctx, out.Name(), artifact); err != nil {
		return err
	}
	return os.Rename(out.Name(), to)
}

// Re-extract the verified ZIP after the source exits. The staged copy is not a
// separate authority over the bundle that will replace the source.
func candidate(ctx context.Context, p update.UnsignedTestPaths, cfg update.Config, r *update.NativeRecord, destination string) (string, error) {
	dir := stagePath(p, r)
	archive := filepath.Join(dir, "package.zip")
	if err := update.NoUnsignedPathAliases(archive); err != nil {
		return "", err
	}
	if err := update.VerifyArtifact(ctx, archive, packageArtifact(r)); err != nil {
		return "", err
	}
	if err := update.NoUnsignedPathAliases(destination); err != nil {
		return "", err
	}
	if err := os.RemoveAll(destination); err != nil {
		return "", err
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		return "", err
	}
	if err := extract(ctx, archive, destination, r.PackageUnpackedBytes); err != nil {
		return "", err
	}
	app := filepath.Join(destination, update.TestAppName+".app")
	cfg.ReleaseID, cfg.Version = r.TargetReleaseID, r.TargetVersion
	if err := update.VerifyUnsignedMacBundle(ctx, app, cfg); err != nil {
		return "", err
	}
	catalog := filepath.Join(app, "Contents", "Resources", "runtime", "config", "python-components.json")
	if err := update.NoUnsignedPathAliases(catalog); err != nil {
		return "", err
	}
	if err := update.VerifyArtifact(ctx, catalog, update.Artifact{SHA256: r.ComponentsSHA256, Size: r.ComponentsSize}); err != nil {
		return "", err
	}
	// The ad-hoc seal confirms bundle integrity, while the verified package
	// digest authenticates the executable that supplies the embedded config.
	// Keep the target on the same isolated policy and test trust keys.
	helper := filepath.Join(app, "Contents", "Resources", "runtime", "bin", "lazymind-update-helper")
	if err := update.NoUnsignedPathAliases(helper); err != nil {
		return "", err
	}
	raw, err := exec.CommandContext(ctx, helper, "config").Output()
	var result struct {
		Status string
		Config update.Config
	}
	if err != nil || len(raw) > 65536 || json.Unmarshal(raw, &result) != nil || result.Status != "enabled" ||
		!reflect.DeepEqual(result.Config, cfg) {
		return "", errors.New("unsigned_test_target_config")
	}
	return app, nil
}

// Validate AppleDouble and runtime link layout before letting ditto preserve
// macOS metadata. All paths and the complete link graph must stay within the
// one signed application bundle.
func extract(ctx context.Context, archive, destination string, limit int64) error {
	invalid := errors.New("unsigned_test_zip")
	z, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer z.Close()
	if limit <= 0 {
		return invalid
	}
	root := update.TestAppName + ".app"
	seen := map[string]bool{}
	links := map[string]string{}
	var size uint64
	for _, entry := range z.File {
		name := strings.TrimSuffix(entry.Name, "/")
		key := strings.ToLower(name)
		if filepath.Clean(name) != name || strings.ContainsAny(name, "\\\x00") ||
			(name != root && !strings.HasPrefix(name, root+"/")) || seen[key] || entry.UncompressedSize64 > uint64(limit)-size {
			return invalid
		}
		seen[key] = true
		size += entry.UncompressedSize64
		if entry.Mode()&os.ModeSymlink != 0 {
			r, err := entry.Open()
			if err != nil {
				return err
			}
			raw, err := io.ReadAll(io.LimitReader(r, 4097))
			_ = r.Close()
			target := string(raw)
			if err != nil || target == "" || len(raw) > 4096 || filepath.IsAbs(target) || strings.ContainsAny(target, "\\\x00") {
				return invalid
			}
			links[key] = strings.ToLower(target)
		} else if !entry.FileInfo().IsDir() && !entry.Mode().IsRegular() {
			return invalid
		}
	}
	for name := range seen {
		for parent := filepath.Dir(name); parent != "."; parent = filepath.Dir(parent) {
			if _, isLink := links[parent]; isLink {
				return invalid
			}
		}
	}
	for name := range links {
		if !internalLink(name, strings.ToLower(root), links) {
			return invalid
		}
	}
	return exec.CommandContext(ctx, "/usr/bin/ditto", "-x", "-k", archive, destination).Run()
}

func internalLink(name, root string, links map[string]string) bool {
	pending := strings.Split(name, "/")
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
		if stack[0] != root {
			return false
		}
		if target, ok := links[strings.Join(stack, "/")]; ok {
			hops++
			if hops > 64 {
				return false
			}
			stack = stack[:len(stack)-1]
			pending = append(strings.Split(target, "/"), pending...)
		}
	}
	return len(stack) != 0
}
