package unsignedupdate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

func TestUnsignedMacBundleChecksActualVersion(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS bundle verification")
	}
	for _, version := range []string{"1.1.0", "1.0.0"} {
		t.Run(version, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			bundle := filepath.Join(root, desktopupdate.TestAppName+".app")
			contents := filepath.Join(bundle, "Contents")
			if err := os.MkdirAll(filepath.Join(contents, "MacOS"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(contents, "Resources"), 0700); err != nil {
				t.Fatal(err)
			}
			binary, err := os.ReadFile("/usr/bin/true")
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(contents, "MacOS", desktopupdate.TestAppName), binary, 0700); err != nil {
				t.Fatal(err)
			}
			plist := fmt.Sprintf(`<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>%s</string><key>CFBundleExecutable</key><string>%s</string><key>CFBundlePackageType</key><string>APPL</string><key>CFBundleShortVersionString</key><string>%s</string><key>CFBundleVersion</key><string>%s</string></dict></plist>`, desktopupdate.TestAppID, desktopupdate.TestAppName, version, version)
			if err = os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist), 0600); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(contents, "Resources", "unsigned-test.json"), []byte(`{"version":"1.1.0"}`), 0600); err != nil {
				t.Fatal(err)
			}
			if output, err := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", "--timestamp=none", bundle).CombinedOutput(); err != nil {
				t.Fatalf("sign temporary fixture: %v %s", err, output)
			}
			err = VerifyMacBundle(context.Background(), bundle)
			if (err == nil) != (version == "1.1.0") {
				t.Fatalf("actual version %s: %v", version, err)
			}
			if version == "1.1.0" {
				archive := filepath.Join(root, "bundle.zip")
				if output, err := exec.Command("/usr/bin/ditto", "-c", "-k", "--keepParent", bundle, archive).CombinedOutput(); err != nil {
					t.Fatalf("package fixture: %v %s", err, output)
				}
				destination := filepath.Join(root, "extracted")
				if err := extract(context.Background(), archive, destination); err != nil {
					t.Fatal("extract actual packaging format", err)
				}
				if err := VerifyMacBundle(context.Background(), filepath.Join(destination, desktopupdate.TestAppName+".app")); err != nil {
					detail, _ := exec.Command("/usr/bin/codesign", "--verify", "--deep", "--strict", "--verbose=4", filepath.Join(destination, desktopupdate.TestAppName+".app")).CombinedOutput()
					t.Fatal("extracted fixture lost identity/signature", err, string(detail))
				}
			}
		})
	}
}
