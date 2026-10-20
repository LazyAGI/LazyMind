package mcpclient

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDSHBundledPackageManagerLayouts(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required to test the embedded runtime resolver")
	}
	const legacy = "runtime/pnpm/bin/pnpm.mjs"
	const primary = "runtime/primary-runtime/dependencies/pnpm/bin/pnpm.mjs"
	for _, tc := range []struct {
		name    string
		entries []string
		want    string
	}{
		{"baseline", []string{legacy}, legacy},
		{"primary-runtime", []string{primary}, primary},
		{"prefer-primary", []string{legacy, primary}, primary},
		{"missing", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resources := filepath.Join(t.TempDir(), "中文 resources")
			for _, entry := range tc.entries {
				writeTestFile(t, filepath.Join(resources, entry), "process.stdout.write('fixture-store')")
			}
			// Exercise the actual embedded resolver and execute its selected entry with
			// the same store command.
			profile := filepath.Join(t.TempDir(), "web")
			if err := os.MkdirAll(profile, 0700); err != nil {
				t.Fatal(err)
			}
			script := dshRuntimePaths + `
const entry = resolveDSHPnpm(process.argv[1]);
const check = require('node:child_process').spawnSync(process.execPath, [entry, 'store', 'path'], {cwd: process.argv[2], encoding: 'utf8'});
if (check.status !== 0 || check.stdout !== 'fixture-store') throw new Error('package manager failed');
process.stdout.write(entry);
`
			output, err := exec.Command(node, "-e", script, resources, profile).CombinedOutput()
			if tc.want == "" {
				if err == nil || !strings.Contains(string(output), "Desktop bundled pnpm entry is missing") {
					t.Fatalf("missing entry: %s %v", output, err)
				}
			} else if err != nil || string(output) != filepath.Join(resources, tc.want) {
				t.Fatalf("package manager: %s %v", output, err)
			}
		})
	}
}
