package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestGitHubHostConfigSurvivesServiceEnvironmentScoping(t *testing.T) {
	for _, source := range []string{"default", "xdg", "explicit"} {
		t.Run(source, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv(localHostHomeEnvVar, home)
			t.Setenv("HOME", home)
			t.Setenv("GH_CONFIG_DIR", "")
			t.Setenv("XDG_CONFIG_HOME", "")
			t.Setenv("LAZYMIND_GITHUB_HOST_CONFIG_DIR", "")
			want := filepath.Join(home, ".config", "gh")
			if source == "xdg" {
				t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "custom-xdg"))
				want = filepath.Join(home, "custom-xdg", "gh")
			}
			if source == "explicit" {
				want = filepath.Join(home, "explicit-gh")
				t.Setenv("GH_CONFIG_DIR", want)
			}
			paths := RuntimePaths{ConfigDir: filepath.Join(home, "runtime-config"), ServiceHome: filepath.Join(home, "service-home")}
			env := serviceRuntimeEnv(paths)
			assertEnvContains(t, env, "HOME="+paths.ServiceHome)
			assertEnvContains(t, env, "LAZYMIND_GITHUB_HOST_CONFIG_DIR="+want)
			for _, item := range env {
				key, value, _ := strings.Cut(item, "=")
				t.Setenv(key, value)
			}
			assertEnvContains(t, processComposeRuntimeEnv(paths), "LAZYMIND_GITHUB_HOST_CONFIG_DIR="+want)
		})
	}
}

func TestHostGitXDGConfigSurvivesServiceEnvironmentScoping(t *testing.T) {
	for _, source := range []string{"default", "custom"} {
		t.Run(source, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv(localHostHomeEnvVar, home)
			t.Setenv("HOME", home)
			t.Setenv("LAZYMIND_HOST_XDG_CONFIG_HOME", "")
			t.Setenv("XDG_CONFIG_HOME", "")
			t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "fixture-internal-token")
			t.Setenv("LAZYMIND_CLIENT_INSTANCE_ID", "fixture-client")
			want := filepath.Join(home, ".config")
			if source == "custom" {
				want = filepath.Join(home, "custom-xdg")
				t.Setenv("XDG_CONFIG_HOME", want)
			}
			paths := RuntimePaths{ConfigDir: filepath.Join(home, "runtime-config"), ProcessComposeHome: filepath.Join(home, "process-home"), ServiceHome: filepath.Join(home, "service-home")}
			env := processComposeRuntimeEnv(paths)
			assertEnvContains(t, env, "LAZYMIND_HOST_XDG_CONFIG_HOME="+want)
			for _, item := range env {
				key, value, _ := strings.Cut(item, "=")
				t.Setenv(key, value)
			}
			assertEnvContains(t, serviceRuntimeEnv(paths), "LAZYMIND_HOST_XDG_CONFIG_HOME="+want)
			assertEnvContains(t, serviceRuntimeEnv(paths), "XDG_CONFIG_HOME="+paths.ConfigDir)
		})
	}
}
