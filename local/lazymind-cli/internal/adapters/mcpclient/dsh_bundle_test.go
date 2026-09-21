package mcpclient

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestMissingDSHNeverInvokesPackageInstaller(t *testing.T) {
	calls := 0
	runtime := dshPluginRuntime{
		findDSH: func() (string, error) { return "", errors.New("not installed") },
		findExecutable: func(name string) (string, error) {
			t.Errorf("unexpected package manager lookup: %s", name)
			return "", errors.New("not installed")
		},
		run: func(context.Context, string, []string, func([]byte) error) error { calls++; return nil },
	}
	err := runDSHPluginWith(context.Background(), runtime, "web", "add", "plugin.tgz")
	if err == nil || !strings.Contains(err.Error(), "DeepSeek Harness CLI was not found") {
		t.Fatalf("missing DSH did not fail explicitly: %v", err)
	}
	if calls != 0 {
		t.Fatalf("executed %d commands without DSH", calls)
	}
}

func TestDSHPluginInstallation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		pnpm, npx bool
		failure   bool
		wantError string
	}{
		{name: "existing pnpm", pnpm: true},
		{name: "missing pnpm uses existing DSH", npx: true},
		{name: "missing package managers", wantError: "pnpm or Node.js/npm is required"},
		{name: "installer failure", pnpm: true, failure: true, wantError: "install LazyMind plugin into DSH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const dsh = "/fixture/existing dsh"
			const npx = "/fixture/npx"
			sentinel := errors.New("installer failed")
			var lookups []string
			calls := 0
			runtime := dshPluginRuntime{
				findDSH: func() (string, error) { return dsh, nil },
				findExecutable: func(name string) (string, error) {
					lookups = append(lookups, name)
					if name == "pnpm" && tc.pnpm {
						return "/fixture/pnpm", nil
					}
					if name == "npx" && tc.npx {
						return npx, nil
					}
					return "", errors.New("not installed")
				},
				run: func(_ context.Context, binary string, args []string, output func([]byte) error) error {
					calls++
					wantBinary := dsh
					wantArgs := []string{"plugin", "--profile", "test-profile", "add", "/fixture/plugin archive.tgz"}
					if !tc.pnpm {
						wantBinary = npx
						wantArgs = append([]string{"--yes", "--package=pnpm@10.0.0", "--", dsh}, wantArgs...)
					}
					if binary != wantBinary || !reflect.DeepEqual(args, wantArgs) {
						t.Errorf("command = %q %q, want %q %q", binary, args, wantBinary, wantArgs)
					}
					if tc.failure {
						if err := output([]byte("installer diagnostic")); err != nil {
							t.Fatal(err)
						}
						return sentinel
					}
					return nil
				},
			}
			err := runDSHPluginWith(context.Background(), runtime, "test-profile", "add", "/fixture/plugin archive.tgz")
			if tc.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("error = %v, want %q", err, tc.wantError)
			}
			if tc.failure && (!errors.Is(err, sentinel) || !strings.Contains(err.Error(), "installer diagnostic")) {
				t.Fatalf("lost installer error or diagnostic: %v", err)
			}
			wantCalls := 1
			if !tc.pnpm && !tc.npx {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Errorf("executed %d commands, want %d", calls, wantCalls)
			}
			wantLookups := []string{"pnpm"}
			if !tc.pnpm {
				wantLookups = append(wantLookups, "npx")
			}
			if !reflect.DeepEqual(lookups, wantLookups) {
				t.Errorf("lookups = %v, want %v", lookups, wantLookups)
			}
		})
	}
}
