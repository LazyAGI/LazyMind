//go:build !windows

package unsignedupdate

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

func fixture(t *testing.T, extra map[string]string) (Installer, Request) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, _ := desktopupdate.TestPathsForAccount("darwin", "lazymind-test", home)
	c := desktopupdate.Config{TrustDomain: "fixture", Audience: "fixture", Channel: "personal-test", Mode: "unsigned-test", ReleaseID: "source", Version: "1.0.0", Platform: "darwin", Arch: "arm64", SigningIdentity: "adhoc", UpdaterProtocol: 1, PolicyURL: "http://127.0.0.1:18080/policy.json", AllowedOrigins: []string{"http://127.0.0.1:18080"}, PublicKeys: map[string]string{"test": base64.StdEncoding.EncodeToString(make([]byte, 32))}}
	marker := func(version, release string) []byte {
		b, _ := json.Marshal(map[string]string{"appId": desktopupdate.TestAppID, "mode": "unsigned-test", "version": version, "releaseId": release, "platform": "darwin", "arch": "arm64", "signingIdentity": "adhoc", "trustDomain": "fixture", "audience": "fixture", "channel": "personal-test"})
		return b
	}
	dir := filepath.Join(p.Application, "Contents", "Resources")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unsigned-test.json"), marker(c.Version, c.ReleaseID), 0600); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(p.Application, "source-sentinel"), []byte("old app"), 0600)
	_ = os.MkdirAll(p.Runtime, 0700)
	_ = os.WriteFile(filepath.Join(p.Runtime, "data-sentinel"), []byte("user data"), 0600)
	var buffer bytes.Buffer
	z := zip.NewWriter(&buffer)
	entries := map[string]string{desktopupdate.TestAppName + ".app/Contents/Resources/unsigned-test.json": string(marker("1.1.0", "target")), desktopupdate.TestAppName + ".app/new-sentinel": "new app"}
	for name, value := range extra {
		entries[name] = value
	}
	for name, value := range entries {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(0600)
		w, e := z.CreateHeader(h)
		if e != nil {
			t.Fatal(e)
		}
		_, _ = w.Write([]byte(value))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(p.Updater, "downloads")
	_ = os.MkdirAll(cache, 0700)
	file := filepath.Join(cache, "package.zip")
	_ = os.WriteFile(file, buffer.Bytes(), 0600)
	hash := sha256.Sum256(buffer.Bytes())
	request := Request{Release: desktopupdate.Release{ID: "target", Version: "1.1.0", Platform: "darwin", Arch: "arm64", SigningIdentity: "adhoc", Package: desktopupdate.Artifact{URL: "http://127.0.0.1:18080/package.zip", Size: int64(buffer.Len()), SHA256: hex.EncodeToString(hash[:])}}, PackagePath: file}
	checks := 0
	i := Installer{Config: c, Paths: p, Verify: func(context.Context, string) error { return nil }, Inspect: func(context.Context, string) error { return nil }, SameProcess: func(desktopupdate.ProcessIdentity) (bool, error) { checks++; return checks == 1, nil }, Launch: func(context.Context, string) error { return nil }}
	return i, request
}

func TestUnsignedReplacementWaitsAndOnlyTouchesTestApplication(t *testing.T) {
	i, r := fixture(t, nil)
	ctx := context.Background()
	parent := desktopupdate.ProcessIdentity{PID: 123, StartID: 456}
	id, err := i.Prepare(ctx, r, parent)
	if err != nil {
		t.Fatal(err)
	}
	checks, launches := 0, 0
	i.SameProcess = func(got desktopupdate.ProcessIdentity) (bool, error) {
		if got != parent {
			t.Fatal("parent identity changed")
		}
		checks++
		if checks <= 2 {
			if _, e := os.Stat(filepath.Join(i.Paths.Application, "source-sentinel")); e != nil {
				t.Fatal("replaced before parent exit")
			}
			return true, nil
		}
		return false, nil
	}
	i.Launch = func(_ context.Context, app string) error {
		launches++
		if app != i.Paths.Application {
			t.Fatal("wrong launch target")
		}
		return nil
	}
	accepted := 0
	if err = i.Install(ctx, id, parent, func() error { accepted++; return nil }); err != nil {
		t.Fatal(err)
	}
	if accepted != 1 || checks != 3 || launches != 1 {
		t.Fatal(accepted, checks, launches)
	}
	if b, e := os.ReadFile(filepath.Join(i.Paths.Application, "new-sentinel")); e != nil || string(b) != "new app" {
		t.Fatal(e, string(b))
	}
	if b, _ := os.ReadFile(filepath.Join(i.Paths.Runtime, "data-sentinel")); string(b) != "user data" {
		t.Fatal("data changed")
	}
	if b, _ := os.ReadFile(filepath.Join(i.Paths.Updater, "installs", id, "previous.app", "source-sentinel")); string(b) != "old app" {
		t.Fatal("backup missing")
	}
	if err = i.Install(ctx, id, parent, func() error { t.Fatal("replayed handoff"); return nil }); err == nil {
		t.Fatal("replayed installation")
	}
}

func TestUnsignedRejectsUntrustedScopeIdentityAndArchive(t *testing.T) {
	for _, scenario := range []string{"official-mode", "official-target", "wrong-id", "wrong-audience", "changed-package", "outside-cache", "zip-escape"} {
		t.Run(scenario, func(t *testing.T) {
			extra := map[string]string{}
			if scenario == "zip-escape" {
				extra["../outside"] = "must not escape"
			}
			i, r := fixture(t, extra)
			original := i.Paths.Application
			switch scenario {
			case "official-mode":
				i.Config.Mode = "official"
				i.Config.Audience = "official"
				i.Config.Channel = "stable"
			case "official-target":
				i.Paths.Application = filepath.Join(i.Paths.Home, "Applications", "LazyMind.app")
			case "wrong-id", "wrong-audience":
				file := filepath.Join(original, "Contents", "Resources", "unsigned-test.json")
				b, _ := os.ReadFile(file)
				if scenario == "wrong-id" {
					b = bytes.ReplaceAll(b, []byte(desktopupdate.TestAppID), []byte("ai.lazymind.desktop"))
				} else {
					b = bytes.ReplaceAll(b, []byte(`"audience":"fixture"`), []byte(`"audience":"official"`))
				}
				_ = os.WriteFile(file, b, 0600)
			case "changed-package":
				b, _ := os.ReadFile(r.PackagePath)
				b[0] ^= 1
				_ = os.WriteFile(r.PackagePath, b, 0600)
			case "outside-cache":
				other := filepath.Join(i.Paths.Home, "outside.zip")
				b, _ := os.ReadFile(r.PackagePath)
				_ = os.WriteFile(other, b, 0600)
				r.PackagePath = other
			}
			if _, err := i.Prepare(context.Background(), r, desktopupdate.ProcessIdentity{PID: 123, StartID: 456}); err == nil {
				t.Fatal("accepted", scenario)
			}
			if b, _ := os.ReadFile(filepath.Join(original, "source-sentinel")); string(b) != "old app" {
				t.Fatal("source changed")
			}
			if _, err := os.Stat(filepath.Join(i.Paths.Home, "outside")); !os.IsNotExist(err) {
				t.Fatal("archive escaped")
			}
		})
	}
}

func TestUnsignedUnknownExitAndChangedPrivateArchiveNeverReplace(t *testing.T) {
	for _, scenario := range []string{"alive", "identity-error", "tampered", "launch-failed"} {
		t.Run(scenario, func(t *testing.T) {
			i, r := fixture(t, nil)
			id, err := i.Prepare(context.Background(), r, desktopupdate.ProcessIdentity{PID: 123, StartID: 456})
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "alive":
				i.SameProcess = func(desktopupdate.ProcessIdentity) (bool, error) { return true, nil }
			case "identity-error":
				checks := 0
				i.SameProcess = func(desktopupdate.ProcessIdentity) (bool, error) {
					checks++
					if checks == 1 {
						return true, nil
					}
					return false, errors.New("unknown process")
				}
			case "tampered":
				file := filepath.Join(i.Paths.Updater, "installs", id, "package.zip")
				b, _ := os.ReadFile(file)
				b[0] ^= 1
				_ = os.WriteFile(file, b, 0600)
			case "launch-failed":
				i.Launch = func(context.Context, string) error { return errors.New("cannot launch") }
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			if err = i.Install(ctx, id, desktopupdate.ProcessIdentity{PID: 123, StartID: 456}, func() error { return nil }); err == nil {
				t.Fatal("unexpected success")
			}
			b, _ := os.ReadFile(filepath.Join(i.Paths.Updater, "installs", id, "plan.json"))
			if !strings.Contains(string(b), `"stage":"repair"`) {
				t.Fatal(string(b))
			}
			if scenario != "launch-failed" {
				if b, _ := os.ReadFile(filepath.Join(i.Paths.Application, "source-sentinel")); string(b) != "old app" {
					t.Fatal("old app changed")
				}
			}
			if b, _ := os.ReadFile(filepath.Join(i.Paths.Runtime, "data-sentinel")); string(b) != "user data" {
				t.Fatal("data changed")
			}
		})
	}
}

func TestUnsignedZipLinksCannotEscapeOrBecomeWriteParents(t *testing.T) {
	for _, target := range []string{"../../outside", "Versions/A"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			var b bytes.Buffer
			z := zip.NewWriter(&b)
			h := &zip.FileHeader{Name: "LazyMind Test.app/link"}
			h.SetMode(os.ModeSymlink | 0777)
			w, _ := z.CreateHeader(h)
			_, _ = w.Write([]byte(target))
			w, _ = z.Create("LazyMind Test.app/link/payload")
			_, _ = w.Write([]byte("bad"))
			_ = z.Close()
			file := filepath.Join(dir, "bad.zip")
			_ = os.WriteFile(file, b.Bytes(), 0600)
			if err := extract(context.Background(), file, filepath.Join(dir, "staged")); err == nil {
				t.Fatal("link used as extraction parent")
			}
		})
	}
}

func TestUnsignedDiscardOnlyRemovesPreparedCopy(t *testing.T) {
	i, r := fixture(t, nil)
	id, err := i.Prepare(context.Background(), r, desktopupdate.ProcessIdentity{PID: 123, StartID: 456})
	if err != nil {
		t.Fatal(err)
	}
	if err = i.Discard(id); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(i.Paths.Updater, "installs", id)); !os.IsNotExist(err) {
		t.Fatal("staged directory retained")
	}
	if err = verifyFile(r.PackagePath, r.Release.Package); err != nil {
		t.Fatal("original download removed", err)
	}
	if b, _ := os.ReadFile(filepath.Join(i.Paths.Application, "source-sentinel")); string(b) != "old app" {
		t.Fatal("source changed")
	}
	id, err = i.Prepare(context.Background(), r, desktopupdate.ProcessIdentity{PID: 123, StartID: 456})
	if err != nil {
		t.Fatal(err)
	}
	if err = i.Install(context.Background(), id, desktopupdate.ProcessIdentity{PID: 123, StartID: 456}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err = i.Discard(id); err == nil {
		t.Fatal("discarded a handed-off installation")
	}
}

func TestUnsignedZipPreservesInternalFrameworkLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macOS framework links")
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	h := &zip.FileHeader{Name: "LazyMind Test.app/Contents/Frameworks/F.framework/Versions/A/Resources/data"}
	h.SetMode(0600)
	w, _ := z.CreateHeader(h)
	_, _ = w.Write([]byte("framework data"))
	for name, target := range map[string]string{"Versions/Current": "A", "Resources": "Versions/Current/Resources"} {
		h = &zip.FileHeader{Name: "LazyMind Test.app/Contents/Frameworks/F.framework/" + name}
		h.SetMode(os.ModeSymlink | 0777)
		w, _ = z.CreateHeader(h)
		_, _ = w.Write([]byte(target))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "fixture.zip")
	_ = os.WriteFile(file, b.Bytes(), 0600)
	if err := extract(context.Background(), file, filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "out", "LazyMind Test.app", "Contents", "Frameworks", "F.framework", "Resources", "data"))
	if err != nil || string(data) != "framework data" {
		t.Fatal(err, string(data))
	}
}

func TestUnsignedLinksResolveParentsAfterSymlinks(t *testing.T) {
	root := desktopupdate.TestAppName + ".app"
	for _, links := range []map[string]string{
		{root + "/a": "../../outside"},
		{root + "/a": "b", root + "/b": "a"},
		{root + "/a": "dir/link/../../outside", root + "/dir/link": ".."},
	} {
		for name := range links {
			if name == root+"/a" && internalLink(name, links) {
				t.Fatal("accepted escaping/cyclic link", links)
			}
		}
	}
}
