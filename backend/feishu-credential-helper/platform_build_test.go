package main

import (
	"debug/buildinfo"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Compile the real pinned CLI entrypoint, even before the helper's implementation
// imports cmd. Mocked packaging commands cannot detect missing platform sums.
func TestPinnedCLIEntrypointBuildsForShippedPlatforms(t *testing.T) {
	for _, target := range []string{"darwin/arm64", "windows/amd64", "linux/amd64", "linux/arm64"} {
		t.Run(target, func(t *testing.T) {
			parts := strings.Split(target, "/")
			binary := filepath.Join(t.TempDir(), "entrypoint")
			command := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-mod=readonly", "-buildvcs=false", "-o", binary, "./testdata/cli-entrypoint")
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				if key != "GOOS" && key != "GOARCH" && key != "CGO_ENABLED" && key != "GOTOOLCHAIN" {
					command.Env = append(command.Env, entry)
				}
			}
			command.Env = append(command.Env, "GOOS="+parts[0], "GOARCH="+parts[1], "CGO_ENABLED=0", "GOTOOLCHAIN=local")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("pinned CLI platform build failed: %v\n%s", err, output)
			}
			info, err := buildinfo.ReadFile(binary)
			if err != nil {
				t.Fatal(err)
			}
			settings := map[string]string{}
			for _, setting := range info.Settings {
				settings[setting.Key] = setting.Value
			}
			if settings["GOOS"] != parts[0] || settings["GOARCH"] != parts[1] {
				t.Fatal("platform check did not build the requested target")
			}
		})
	}
}
