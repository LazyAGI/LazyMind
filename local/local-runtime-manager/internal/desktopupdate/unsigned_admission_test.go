package desktopupdate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnsignedStaleBuildIdentityCannotResumeStartup(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	resources := filepath.Join(root, "Resources")
	_ = os.MkdirAll(resources, 0700)
	marker := filepath.Join(resources, "unsigned-test.json")
	_ = os.WriteFile(marker, []byte(`{"appId":"ai.lazymind.desktop.test","mode":"unsigned-test","releaseId":"n","version":"1.0.0"}`), 0600)
	installation := filepath.Join(resources, "runtime")
	if err := CheckUnsignedCaller(installation, "n", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(marker, []byte(`{"appId":"ai.lazymind.desktop.test","mode":"unsigned-test","releaseId":"n1","version":"1.1.0"}`), 0600)
	if err := CheckUnsignedCaller(installation, "n", "1.0.0"); err == nil {
		t.Fatal("old code admitted against new resources")
	}
	if err := CheckUnsignedCaller(installation, "n1", "1.1.0"); err != nil {
		t.Fatal(err)
	}
	if err := CheckUnsignedCaller(installation, "", ""); err == nil {
		t.Fatal("missing cached identity accepted")
	}
}
