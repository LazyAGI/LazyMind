//go:build darwin

package unsignedupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

func signFixture(t *testing.T, bundle, version string) {
	t.Helper()
	contents := filepath.Join(bundle, "Contents")
	if err := os.MkdirAll(filepath.Join(contents, "MacOS"), 0700); err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile("/usr/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(contents, "MacOS", desktopupdate.TestAppName), binary, 0700); err != nil {
		t.Fatal(err)
	}
	plist := fmt.Sprintf(`<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>%s</string><key>CFBundleExecutable</key><string>%s</string><key>CFBundlePackageType</key><string>APPL</string><key>CFBundleShortVersionString</key><string>%s</string></dict></plist>`, desktopupdate.TestAppID, desktopupdate.TestAppName, version)
	if err = os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", "--timestamp=none", bundle).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
}

func TestUnsignedCurrentPPTLinkDoesNotWeakenTargetVerification(t *testing.T) {
	for _, tampered := range []bool{false, true} {
		t.Run(fmt.Sprint(tampered), func(t *testing.T) {
			i, r := fixture(t, nil)
			if err := os.Rename(filepath.Join(i.Paths.Application, "source-sentinel"), filepath.Join(i.Paths.Application, "Contents", "Resources", "source-sentinel")); err != nil {
				t.Fatal(err)
			}
			exporter := filepath.Join(i.Paths.Application, "Contents", "Resources", "runtime", "app", "workflows", "ppt-workflow", "runtime", "scripts", "export_pptx")
			if err := os.MkdirAll(exporter, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(exporter, "html_to_pptx.mjs"), []byte("// fixture, never executed"), 0600); err != nil {
				t.Fatal(err)
			}
			signFixture(t, i.Paths.Application, i.Config.Version)
			if err := VerifyMacBundle(context.Background(), i.Paths.Application); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(i.Paths.Home, "target", desktopupdate.TestAppName+".app")
			if output, err := exec.Command("/usr/bin/ditto", i.Paths.Application, target).CombinedOutput(); err != nil {
				t.Fatal(err, string(output))
			}
			marker := filepath.Join(target, "Contents", "Resources", "unsigned-test.json")
			b, _ := os.ReadFile(marker)
			var fields map[string]any
			if err := json.Unmarshal(b, &fields); err != nil {
				t.Fatal(err)
			}
			fields["version"] = r.Release.Version
			fields["releaseId"] = r.Release.ID
			b, _ = json.Marshal(fields)
			if err := os.WriteFile(marker, b, 0600); err != nil {
				t.Fatal(err)
			}
			signFixture(t, target, r.Release.Version)
			if tampered {
				if err := os.WriteFile(filepath.Join(target, "Contents", "Resources", "unexpected"), []byte("changed after signing"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if output, err := exec.Command("/usr/bin/ditto", "-c", "-k", "--keepParent", target, r.PackagePath).CombinedOutput(); err != nil {
				t.Fatal(err, string(output))
			}
			b, _ = os.ReadFile(r.PackagePath)
			hash := sha256.Sum256(b)
			r.Release.Package.SHA256 = hex.EncodeToString(hash[:])
			r.Release.Package.Size = int64(len(b))
			deps := filepath.Join(i.Paths.Runtime, "deps", "editable-ppt")
			if err := os.MkdirAll(filepath.Join(deps, "node_modules"), 0700); err != nil {
				t.Fatal(err)
			}
			module, err := filepath.Abs("../../../../workflows/ppt-workflow/runtime/scripts/export_pptx/lib/browser_setup.mjs")
			if err != nil {
				t.Fatal(err)
			}
			script := `import {pathToFileURL} from "node:url"; const {ensureExporterNodeModulesLink}=await import(pathToFileURL(process.argv[1])); process.env.LAZYMIND_PPT_EXPORT_DEPS=process.argv[3]; ensureExporterNodeModulesLink(process.argv[2]);`
			if output, err := exec.Command("node", "--input-type=module", "-e", script, module, exporter, deps).CombinedOutput(); err != nil {
				t.Fatal(err, string(output))
			}
			if _, err := os.Readlink(filepath.Join(exporter, "node_modules")); err != nil {
				t.Fatal("consumer did not create its normal link", err)
			}
			if VerifyMacBundle(context.Background(), i.Paths.Application) == nil {
				t.Fatal("fixture did not exercise mutable signed resources")
			}
			i.Inspect = InspectMacBundle
			i.Verify = VerifyMacBundle
			parent := desktopupdate.ProcessIdentity{PID: 123, StartID: 456}
			id, err := i.Prepare(context.Background(), r, parent)
			if tampered {
				if err == nil {
					t.Fatal("accepted tampered target despite a valid ZIP hash")
				}
				return
			}
			if err != nil {
				t.Fatal("normal source usage blocked update", err)
			}
			if err = i.Install(context.Background(), id, parent, func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			if err = VerifyMacBundle(context.Background(), i.Paths.Application); err != nil {
				t.Fatal("installed target is not intact", err)
			}
			if b, _ := os.ReadFile(filepath.Join(i.Paths.Runtime, "data-sentinel")); string(b) != "user data" {
				t.Fatal("runtime data changed")
			}
		})
	}
}
