package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundledCredentialHelperEnvironmentKeepsCLIIndependent(t *testing.T) {
	root := t.TempDir()
	paths := RuntimePaths{RuntimeRoot: filepath.Join(root, "runtime"), BinDir: filepath.Join(root, "bin")}
	if err := os.MkdirAll(paths.BinDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"LAZYMIND_FEISHU_CLI_PATH", "LAZYMIND_FEISHU_CLI_SHA256", "LAZYMIND_FEISHU_CLI_CREDENTIAL_HELPER_PATH", "LAZYMIND_FEISHU_CLI_CREDENTIAL_HELPER_SHA256"} {
		t.Setenv(key, "")
	}
	cli := executablePath(paths.BinDir, "lark-cli")
	if err := os.WriteFile(cli, []byte("fixture CLI"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.BinDir, "lark-cli.sha256"), []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	before := feishuCLIEnvMap(feishuCLIRuntimeEnv(paths))
	if before["LAZYMIND_FEISHU_CLI_PATH"] != cli {
		t.Fatal("missing helper disabled the existing CLI runtime")
	}
	helper := executablePath(paths.BinDir, "feishu-credential-helper")
	if err := os.WriteFile(helper, []byte("fixture helper"), 0700); err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("b", 64)
	if err := os.WriteFile(filepath.Join(paths.BinDir, "feishu-credential-helper.sha256"), []byte(digest+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	withHelper := feishuCLIEnvMap(feishuCLIRuntimeEnv(paths))
	if withHelper["LAZYMIND_FEISHU_CLI_CREDENTIAL_HELPER_PATH"] != helper || withHelper["LAZYMIND_FEISHU_CLI_CREDENTIAL_HELPER_SHA256"] != digest {
		t.Fatal("bundled helper is not passed to Core with its checksum")
	}
	if err := os.WriteFile(filepath.Join(paths.BinDir, "feishu-credential-helper.sha256"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	invalid := feishuCLIEnvMap(feishuCLIRuntimeEnv(paths))
	if invalid["LAZYMIND_FEISHU_CLI_CREDENTIAL_HELPER_PATH"] != "" || invalid["LAZYMIND_FEISHU_CLI_PATH"] != cli {
		t.Fatal("invalid helper checksum affected the old CLI runtime or enabled an unverified helper")
	}
}
