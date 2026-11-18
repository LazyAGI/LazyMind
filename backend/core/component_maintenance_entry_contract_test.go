package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"lazymind/core/systemdeps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestComponentMaintenanceEntrypointChild(t *testing.T) {
	if os.Getenv("LAZYMIND_UNIT_COMPONENT_ENTRY") != "1" {
		return
	}
	if root := os.Getenv("LAZYMIND_UNIT_COMPONENT_HOLD_DIR"); root != "" {
		resolveComponentMaintenanceRoot = func() (string, error) { return os.Getenv("LAZYMIND_UNIT_UPDATER_ROOT"), nil }
		http.DefaultTransport = componentEntryTransport(func(r *http.Request) (*http.Response, error) {
			_ = os.WriteFile(filepath.Join(root, "entered"), []byte(strconv.Itoa(os.Getpid())), 0600)
			deadline := time.NewTimer(30 * time.Second)
			defer deadline.Stop()
			for {
				if _, err := os.Stat(filepath.Join(root, "release")); err == nil {
					break
				}
				select {
				case <-deadline.C:
					return nil, context.DeadlineExceeded
				case <-time.After(10 * time.Millisecond):
				}
			}
			file, err := os.Open(filepath.Join(root, "payload.zip"))
			if err != nil {
				return nil, err
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: file, Request: r}, nil
		})
	}
	os.Args = []string{os.Args[0], "--component-maintenance", "stage"}
	if os.Getenv("LAZYMIND_UNIT_CORE_BOOTSTRAP_ENTRY") == "1" {
		os.Args = []string{os.Args[0]}
	}
	if raw := os.Getenv("LAZYMIND_UNIT_COMPONENT_ARGS"); raw != "" {
		var args []string
		if json.Unmarshal([]byte(raw), &args) != nil {
			os.Exit(4)
		}
		os.Args = append([]string{os.Args[0]}, args...)
	}
	main()
	os.Exit(0)
}

func TestDesktopUpdateBootstrapCoreMainRejectsBeforeOrdinaryInitialization(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestComponentMaintenanceEntrypointChild$")
	cmd.Dir = root
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "SystemRoot=" + os.Getenv("SystemRoot"), "LAZYMIND_UNIT_COMPONENT_ENTRY=1", "LAZYMIND_UNIT_CORE_BOOTSTRAP_ENTRY=1",
		"LAZYMIND_UPDATE_BOOTSTRAP_TRANSACTION=" + strings.Repeat("a", 32), "LAZYMIND_UPDATE_BOOTSTRAP_OWNER=fixture-owner", "LAZYMIND_UPDATE_BOOTSTRAP_STORAGE={broken",
		"LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN_FILE=" + filepath.Join(root, "never-a-real-token"), "ACL_DB_DRIVER=invalid-fixture-driver", "ACL_DB_DSN=invalid://fixture"}
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil || err == nil || !bytes.Contains(output, []byte("update_bootstrap_invalid")) || bytes.Contains(output, []byte("invalid Core internal service token file")) {
		t.Fatalf("Core main did not validate bootstrap before ordinary initialization: %v %s", err, output)
	}
}

type componentEntryTransport func(*http.Request) (*http.Response, error)

func (f componentEntryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestComponentHTTPInstallEntrypointChild(t *testing.T) {
	if os.Getenv("LAZYMIND_UNIT_COMPONENT_ENTRY") != "1" {
		return
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/core/system/python-components/install", bytes.NewBufferString(`{"id":"rag"}`))
	systemdeps.InstallPythonComponent(response, request)
	_, _ = io.Copy(os.Stdout, response.Body)
	os.Exit(0)
}

func TestComponentMaintenanceInvalidRequestIsHandledBeforeCoreInitialization(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestComponentMaintenanceEntrypointChild$")
	cmd.Dir = root
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "SystemRoot=" + os.Getenv("SystemRoot"),
		"LAZYMIND_UNIT_COMPONENT_ENTRY=1",
		"LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN_FILE=" + filepath.Join(root, "does-not-exist"),
		"ACL_DB_DRIVER=invalid-fixture-driver", "ACL_DB_DSN=invalid://fixture",
	}
	cmd.Stdin = bytes.NewBufferString("{invalid")
	output, err := cmd.Output()
	if ctx.Err() != nil {
		t.Fatal("component command fell through into the normal server lifecycle")
	}
	var result struct{ Status, Reason string }
	if json.Unmarshal(output, &result) != nil || result.Status != "blocked" || result.Reason != "update_request_invalid" {
		t.Fatalf("maintenance entry did not handle its own request before service initialization: err=%v stdout=%q", err, output)
	}
	if _, err := os.Stat(filepath.Join(root, "acl.db")); !os.IsNotExist(err) {
		t.Fatal("maintenance touched the business database")
	}
}
