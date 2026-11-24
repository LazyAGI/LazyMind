package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	update "github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

func TestV1InterruptedMarkerConsumption(t *testing.T) {
	for _, next := range []string{"same target", "other release", "independent edit", "previously current"} {
		t.Run(next, func(t *testing.T) {
			dir := t.TempDir()
			r := update.V1ComponentHandoff{SourceID: "old", SourceVersion: "1.0.0", TargetID: "new", TargetVersion: "1.1.0", Runtime: dir, Installation: dir,
				PolicyURL: "https://updates.example.invalid/policy.json", DataProfile: "known-v1", SourceOwner: strings.Repeat("a", 64), EdgeDigest: strings.Repeat("b", 64), SelectionDigest: strings.Repeat("c", 64)}
			if err := r.Save(dir); err != nil {
				t.Fatal(err)
			}
			cluster, rag := filepath.Join(dir, "cluster-component-app-version"), filepath.Join(dir, "rag-component-app-version")
			original := "1.0.0\n"
			if next == "previously current" {
				original = "1.1.0\n"
			}
			if err := os.WriteFile(cluster, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(rag, 0700); err != nil {
				t.Fatal(err)
			}
			ids := []string{"cluster", "rag"}
			if err := r.Consume(dir, ids); err == nil {
				t.Fatal("invalid second marker did not interrupt consumption")
			}
			got, err := os.ReadFile(cluster)
			if err != nil || string(got) != "1.1.0\n" {
				t.Fatal("fixture did not reach first target marker")
			}
			pending, err := update.ReadV1ComponentHandoff(dir)
			if err != nil || pending == nil {
				t.Fatalf("lost recoverable marker receipt: %v", err)
			}
			if err := os.Remove(rag); err != nil {
				t.Fatal(err)
			}
			if next == "same target" {
				if err := pending.Consume(dir, ids); err != nil {
					t.Fatal(err)
				}
				if marker, err := os.ReadFile(rag); err != nil || string(marker) != "1.1.0\n" {
					t.Fatal("target did not complete its second marker")
				}
			} else {
				if next == "independent edit" {
					original = "2.0.0\n"
					if err := os.WriteFile(cluster, []byte(original), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := pending.UndoMarkers(dir); err != nil {
					t.Fatal(err)
				}
				if marker, err := os.ReadFile(cluster); err != nil || string(marker) != original {
					t.Fatalf("rollback clobbered marker or left wrong target selected: %q %v", marker, err)
				}
				if err := update.ClearV1ComponentHandoff(dir); err != nil {
					t.Fatal(err)
				}
			}
			if left, err := update.ReadV1ComponentHandoff(dir); err != nil || left != nil {
				t.Fatalf("stale receipt: %v %v", left, err)
			}
		})
	}
}

func TestVerifiedSourceCanReopenWhileV1HandoffIsUncertain(t *testing.T) {
	f := newUpdateStateFixtureLayout(t)
	r := update.V1ComponentHandoff{SourceID: "a", SourceVersion: "1.0.0", TargetID: "b", TargetVersion: "1.1.0", Runtime: f.paths.RuntimeRoot, Installation: f.paths.ResourcesRoot,
		PolicyURL: "https://updates.example.invalid/policy.json", DataProfile: "known-v1", SourceOwner: strings.Repeat("a", 64), EdgeDigest: strings.Repeat("b", 64), SelectionDigest: strings.Repeat("c", 64)}
	if err := r.Save(f.paths.StateDir); err != nil {
		t.Fatal(err)
	}
	platform := runtime.GOOS
	if platform == "linux" {
		platform = "darwin"
	}
	identity := update.Config{ReleaseID: r.SourceID, Version: r.SourceVersion, TrustDomain: "fixture", Audience: "official", Channel: "stable", Mode: "official",
		SigningIdentity: "team", Platform: platform, Arch: runtime.GOARCH, UpdaterProtocol: 1, PolicyURL: r.PolicyURL, AllowedOrigins: []string{"https://updates.example.invalid"},
		PublicKeys: map[string]string{"fixture": base64.StdEncoding.EncodeToString(make([]byte, 32))}}
	raw, err := json.Marshal(map[string]any{"status": "enabled", "config": identity})
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(f.paths.ResourcesRoot, "bin", executableName("lazymind-update-helper"))
	if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		source := filepath.Join(t.TempDir(), "helper.go")
		code := `package main
import "os"
func main(){if len(os.Args)!=2 || os.Args[1]!="verify-identity"{os.Exit(2)};f,_:=os.Executable();b,e:=os.ReadFile(f+".json");if e!=nil{panic(e)};os.Stdout.Write(b)}`
		if err := os.WriteFile(source, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, source).CombinedOutput(); err != nil {
			t.Fatalf("%s %v", out, err)
		}
	} else if err := os.WriteFile(binary, []byte("#!/bin/sh\n[ \"$1\" = verify-identity ] || exit 2\ncat \"$0.json\""), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary+".json", raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(desktopAppVersionEnvVar, r.SourceVersion)
	if err := prepareDesktopPythonComponents(t.Context(), f.cfg, f.paths); err != nil {
		t.Fatal(err)
	}
	pending, err := update.ReadV1ComponentHandoff(f.paths.StateDir)
	if err != nil || pending == nil || pending.TargetID != r.TargetID || pending.EdgeDigest != r.EdgeDigest {
		t.Fatalf("source consumed the pending target decision: %+v %v", pending, err)
	}
}
