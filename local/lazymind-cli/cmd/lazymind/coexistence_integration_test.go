package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"lazymind/agentconnector/internal/assistantbridge"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workflowconnector"
	"lazymind/agentconnector/internal/workflowconnector/connectortest"
)

// The subprocess executes the real CLI/HTTP server, including detached launch,
// environment routing and shutdown. No Docker or user runtime is required.
func TestMain(m *testing.M) {
	name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	if name == desktopDSHInstaller || name == "DeepSeek Harness" {
		if err := runDesktopDSHFixture(name, os.Args[1:]); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if name == dshNativeInstallFixture {
		if err := runDSHNativeInstallFixture(os.Args[1:]); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Getenv("LAZYMIND_COEXISTENCE_TEST_HELPER") == "1" && len(os.Args) > 1 && os.Args[1] == "coexistence-owner" {
		time.Sleep(time.Hour)
		os.Exit(0)
	}
	if os.Getenv("LAZYMIND_COEXISTENCE_TEST_HELPER") == "1" && len(os.Args) > 1 && os.Args[1] == "assistant" {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
			_, _ = os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestDesktopComposeSharedBridgeLifecycle(t *testing.T) {
	for _, desktopFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "desktop-first", false: "compose-first"}[desktopFirst], func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("LAZYMIND_HOME", root)
			t.Setenv("LAZYMIND_COEXISTENCE_TEST_HELPER", "1")
			t.Setenv(connectorconfig.EnvironmentKey, "")
			t.Setenv("LAZYMIND_ASSISTANT_BRIDGE_URL", "")
			t.Setenv("LAZYMIND_BRIDGE_ISOLATED", "")
			backend := httptest.NewServer(http.NotFoundHandler())
			defer backend.Close()
			composeBackend := httptest.NewServer(http.NotFoundHandler())
			defer composeBackend.Close()
			origin, _ := connectorconfig.OriginIdentity(composeBackend.URL)
			first := "desktop"
			if !desktopFirst {
				first = origin
			}
			if _, err := connectorconfig.EnvironmentHome(root, first); err != nil {
				t.Fatal(err)
			}
			desktopHome, err := connectorconfig.EnvironmentHome(root, "desktop")
			if err != nil {
				t.Fatal(err)
			}
			store, err := credentials.NewStore(desktopHome, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Save(credentials.Credentials{ServerURL: backend.URL, AccessToken: "desktop", RefreshToken: "refresh", LocalRuntimeID: "desktop-test"}); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			_ = listener.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			desktop := assistantbridge.ClientOptions{ID: "desktop-test", OwnerPID: os.Getpid()}
			compose := assistantbridge.ClientOptions{ID: "compose-test", ServerURL: composeBackend.URL}
			start := func(isDesktop bool) map[string]any {
				profile := ""
				client := compose
				if isDesktop {
					profile = "desktop"
					client = desktop
				}
				t.Setenv("LAZYMIND_RUNTIME_PROFILE", profile)
				status, err := assistantbridge.StartWithClient(ctx, address, client)
				if err != nil {
					t.Fatal(err)
				}
				return status
			}
			t.Cleanup(func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = os.Setenv("LAZYMIND_RUNTIME_PROFILE", "desktop")
				_ = assistantbridge.ReleaseClient(cleanup, desktop)
				_ = os.Setenv("LAZYMIND_RUNTIME_PROFILE", "")
				_ = assistantbridge.ReleaseClient(cleanup, compose)
			})
			initial := start(desktopFirst)
			second := start(!desktopFirst)
			if initial["pid"] != second["pid"] {
				t.Fatalf("shared broker duplicated: %v != %v", initial["pid"], second["pid"])
			}
			if initial["executable"] == os.Args[0] {
				t.Fatal("shared broker was not staged independently")
			}
			// Exercise actual Origin routing through the shared listener.
			body := `{"server_url":` + strconvQuote(composeBackend.URL) + `,"access_token":"compose","refresh_token":"compose-refresh"}`
			request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+address+"/v1/session", strings.NewReader(body))
			request.Header.Set("Origin", composeBackend.URL)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatalf("session status=%d", response.StatusCode)
			}
			if raw, err := os.ReadFile(filepath.Join(desktopHome, "credentials.json")); err != nil || !strings.Contains(string(raw), `"access_token": "desktop"`) {
				t.Fatalf("Desktop session changed: %v", err)
			}
			t.Setenv("LAZYMIND_RUNTIME_PROFILE", "desktop")
			if err := assistantbridge.ReleaseClient(ctx, desktop); err != nil {
				t.Fatal(err)
			}
			if _, err := assistantbridge.Health(ctx, address); err != nil {
				t.Fatal("Desktop release stopped Compose's bridge", err)
			}
			composeHome, _ := connectorconfig.EnvironmentHome(root, origin)
			endpoint, err := connectorconfig.ReadBridgeEndpoint(composeHome)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := assistantbridge.Health(ctx, strings.TrimPrefix(endpoint.URL, "http://")); err != nil {
				t.Fatal("Compose context stopped", err)
			}
			t.Setenv("LAZYMIND_RUNTIME_PROFILE", "")
			if err := assistantbridge.ReleaseClient(ctx, compose); err != nil {
				t.Fatal(err)
			}
			if _, err := assistantbridge.Health(ctx, address); err == nil {
				t.Fatal("unreferenced broker still running")
			}
		})
	}
}

func strconvQuote(value string) string { body, _ := json.Marshal(value); return string(body) }

func TestBridgeSurvivesDesktopCrashOnlyWhileComposeUsesIt(t *testing.T) {
	for _, withCompose := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "shared"}[withCompose], func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("LAZYMIND_HOME", root)
			t.Setenv("LAZYMIND_RUNTIME_PROFILE", "desktop")
			t.Setenv("LAZYMIND_COEXISTENCE_TEST_HELPER", "1")
			t.Setenv(connectorconfig.EnvironmentKey, "")
			t.Setenv("LAZYMIND_ASSISTANT_BRIDGE_URL", "")
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			owner := exec.Command(self, "coexistence-owner")
			if err := owner.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = owner.Process.Kill(); _ = owner.Wait() })
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			_ = listener.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			desktop := assistantbridge.ClientOptions{ID: "desktop-crash", OwnerPID: owner.Process.Pid}
			compose := assistantbridge.ClientOptions{ID: "compose-crash", ServerURL: "http://localhost:28090"}
			t.Cleanup(func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = assistantbridge.ReleaseClient(cleanup, desktop)
				_ = assistantbridge.ReleaseClient(cleanup, compose)
			})
			if _, err := assistantbridge.StartWithClient(ctx, address, desktop); err != nil {
				t.Fatal(err)
			}
			if withCompose {
				if _, err := assistantbridge.StartWithClient(ctx, address, compose); err != nil {
					t.Fatal(err)
				}
			}
			if err := owner.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = owner.Wait()
			deadline := time.Now().Add(8 * time.Second)
			const windowsSharingViolation = syscall.Errno(32) // ERROR_SHARING_VIOLATION
			for {
				body, err := os.ReadFile(filepath.Join(root, "bridge-clients.json"))
				if err == nil {
					var clients map[string]assistantbridge.ClientOptions
					if err := json.Unmarshal(body, &clients); err != nil {
						t.Fatalf("decode bridge clients: %v", err)
					}
					_, hasDesktop := clients[desktop.ID]
					_, healthErr := assistantbridge.Health(ctx, address)
					if !hasDesktop && (withCompose || healthErr != nil) {
						if withCompose && (healthErr != nil || clients[compose.ID].ID == "") {
							t.Fatal("Desktop crash stopped Compose's bridge")
						}
						break
					}
				} else if runtime.GOOS != "windows" || !errors.Is(err, windowsSharingViolation) {
					t.Fatalf("read bridge clients: %v", err)
				}
				if time.Now().After(deadline) {
					t.Fatal("Desktop crash did not release its bridge ownership")
				}
				time.Sleep(50 * time.Millisecond)
			}
			if withCompose {
				if err := assistantbridge.ReleaseClient(ctx, compose); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestAssistantColdStartWithDamagedConnectorPackage(t *testing.T) {
	for _, tc := range []struct {
		name, damaged, healthy, path, diagnostic string
	}{
		{"missing WorkBuddy manifest", "workbuddy", "codex", "connector.json", "installation record"},
		{"corrupt Codex module", "codex", "workbuddy", "main.mjs", "release digest mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			distribution := t.TempDir()
			t.Setenv("LAZYMIND_HOME", root)
			t.Setenv("LAZYMIND_RUNTIME_PROFILE", "")
			t.Setenv("LAZYMIND_COEXISTENCE_TEST_HELPER", "1")
			t.Setenv(connectorconfig.EnvironmentKey, "")
			t.Setenv("LAZYMIND_BRIDGE_ISOLATED", "")
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(distribution, filepath.Base(self))
			// Windows cannot remove a hard link to the test image while this
			// process is still running. Give the distribution its own file.
			body, err := os.ReadFile(self)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(binary, body, 0700); err != nil {
				t.Fatal(err)
			}
			connectortest.WriteMinimalPackage(t, binary, tc.damaged)
			healthy := connectortest.WritePackage(t, binary, tc.healthy, "0.2.0", map[string]string{
				"main.mjs": "export const healthy = true;", "adapter.mjs": "", "presentation.mjs": "",
			})
			badPath := filepath.Join(distribution, "workflow-connectors", tc.damaged, tc.path)
			if tc.path == "connector.json" {
				err = os.Remove(badPath)
			} else {
				err = os.WriteFile(badPath, []byte("modified after packaging"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			_ = listener.Close()
			t.Setenv("LAZYMIND_ASSISTANT_BRIDGE_URL", "http://"+address)
			deployment := httptest.NewServer(http.NotFoundHandler())
			defer deployment.Close()
			browser := httptest.NewServer(http.NotFoundHandler())
			defer browser.Close()

			runAssistant := func(action, origin string) (string, string, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, binary, "assistant", action, "--client-id", "package-test:"+origin, "--server-url", origin)
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				out, err := cmd.Output()
				return string(out), stderr.String(), err
			}
			t.Cleanup(func() {
				// Reuse the broker for the browser origin so its lease can
				// be released through the normal deployment stop command.
				if _, err := assistantbridge.Health(context.Background(), address); err != nil {
					return
				}
				_, _, _ = runAssistant("start", browser.URL)
				_, _, _ = runAssistant("stop", browser.URL)
				_, _, _ = runAssistant("stop", deployment.URL)
			})
			out, diagnostic, err := runAssistant("start", deployment.URL)
			if err != nil {
				t.Fatalf("cold start: %v\n%s\n%s", err, out, diagnostic)
			}
			wantPath := badPath
			if tc.path != "connector.json" {
				wantPath = filepath.Dir(badPath)
			}
			if !strings.Contains(diagnostic, tc.diagnostic) || !strings.Contains(diagnostic, wantPath) {
				t.Fatalf("diagnostic for damaged connector = %q", diagnostic)
			}

			// The browser origin has no prior CLI registration. Only the
			// staged daemon can seed this newly created isolated environment.
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+address+"/v1/session",
				strings.NewReader(`{"server_url":`+strconvQuote(browser.URL)+`,"access_token":"browser","refresh_token":"refresh"}`))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Origin", browser.URL)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("browser session: %v", response.Status)
			}
			identity, err := connectorconfig.OriginIdentity(browser.URL)
			if err != nil {
				t.Fatal(err)
			}
			home, err := connectorconfig.EnvironmentHome(root, identity)
			if err != nil {
				t.Fatal(err)
			}
			installed, err := workflowconnector.Resolve(home, tc.healthy)
			if err != nil {
				t.Fatalf("isolated environment cannot resolve healthy connector: %v", err)
			}
			if revision, err := installed.Revision(); err != nil || revision != healthy.Release {
				t.Fatalf("installed connector revision = %q, want %q (err = %v)", revision, healthy.Release, err)
			}
			if _, err := os.Stat(workflowconnector.RecordPath(home, tc.damaged)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("damaged connector record should be absent: %v", err)
			}
		})
	}
}
