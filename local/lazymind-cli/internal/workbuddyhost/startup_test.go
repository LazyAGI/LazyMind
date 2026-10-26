package workbuddyhost

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type fakeStartupEnvironment struct {
	mu    sync.Mutex
	value string
	fail  bool
}

type publishedStartupEnvironment struct {
	fakeStartupEnvironment
	failNotification bool
	publications     int
}

func (e *publishedStartupEnvironment) Refresh() error {
	e.publications++
	if e.failNotification {
		return errors.New("notification unavailable")
	}
	return nil
}
func (e *publishedStartupEnvironment) Set(value string) error { e.value = value; return e.Refresh() }
func (e *publishedStartupEnvironment) Unset() error           { e.value = ""; return e.Refresh() }

func TestStartupEnvironmentRetriesPublishAndRestoreWithoutLosingOriginal(t *testing.T) {
	home := t.TempDir()
	env := &publishedStartupEnvironment{fakeStartupEnvironment: fakeStartupEnvironment{value: "9444"}, failNotification: true}
	if err := updateStartupEnvironment(home, 9333, true, env); err == nil {
		t.Fatal("expected publication failure")
	}
	env.failNotification = false
	if err := updateStartupEnvironment(home, 9333, true, env); err != nil {
		t.Fatal(err)
	}
	if env.publications != 2 || env.value != "9333" {
		t.Fatalf("enable: %+v", env)
	}
	env.failNotification = true
	if err := updateStartupEnvironment(home, 0, false, env); err == nil {
		t.Fatal("expected restore publication failure")
	}
	env.failNotification = false
	if err := updateStartupEnvironment(home, 0, false, env); err != nil {
		t.Fatal(err)
	}
	if env.publications != 4 || env.value != "9444" {
		t.Fatalf("restore: %+v", env)
	}
}

func TestStartupEnvironmentConcurrentConnectKeepsOriginalValue(t *testing.T) {
	home := t.TempDir()
	env := &fakeStartupEnvironment{value: "9444"}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := updateStartupEnvironment(home, 9333, true, env); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	if err := updateStartupEnvironment(home, 0, false, env); err != nil {
		t.Fatal(err)
	}
	if env.value != "9444" {
		t.Fatalf("lost original value: %q", env.value)
	}
}

func (e *fakeStartupEnvironment) Get() (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.value, nil
}
func (e *fakeStartupEnvironment) Set(value string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fail {
		return errors.New("unavailable")
	}
	e.value = value
	return nil
}
func (e *fakeStartupEnvironment) Unset() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.value = ""
	return nil
}
func TestStartupEnvironmentLifecycle(t *testing.T) {
	home := t.TempDir()
	env := &fakeStartupEnvironment{value: "9444"}
	check := func(port int, enabled bool, expected string) {
		t.Helper()
		if err := updateStartupEnvironment(home, port, enabled, env); err != nil {
			t.Fatal(err)
		}
		if env.value != expected {
			t.Fatalf("value = %q, want %q", env.value, expected)
		}
	}
	check(9333, true, "9333")
	check(9333, true, "9333")
	env.value = "" // a new GUI login; Assistant Bridge restores the managed setting
	check(9333, true, "9333")
	check(9334, true, "9334")
	check(0, false, "9444")
	check(0, false, "9444")
}
func TestStartupEnvironmentDoesNotOwnExistingOrOverwriteExternalChange(t *testing.T) {
	home := t.TempDir()
	env := &fakeStartupEnvironment{value: "9333"}
	if err := updateStartupEnvironment(home, 9333, true, env); err != nil {
		t.Fatal(err)
	}
	if err := updateStartupEnvironment(home, 0, false, env); err != nil || env.value != "9333" {
		t.Fatal("removed a preexisting user setting", err)
	}
	env.value = ""
	if err := updateStartupEnvironment(home, 9333, true, env); err != nil {
		t.Fatal(err)
	}
	env.value = "9555"
	if err := updateStartupEnvironment(home, 9333, true, env); err == nil {
		t.Fatal("expected external change conflict")
	}
	if err := updateStartupEnvironment(home, 0, false, env); err != nil || env.value != "9555" {
		t.Fatal("overwrote external setting", err)
	}
}
func TestStartupEnvironmentRetriesFailedConfiguration(t *testing.T) {
	home := t.TempDir()
	env := &fakeStartupEnvironment{fail: true}
	if err := updateStartupEnvironment(home, 9333, true, env); err == nil {
		t.Fatal("expected failure")
	}
	env.fail = false
	if err := updateStartupEnvironment(home, 9333, true, env); err != nil {
		t.Fatal(err)
	}
	if err := updateStartupEnvironment(home, 0, false, env); err != nil || env.value != "" {
		t.Fatal("cleanup failed", err)
	}
	if _, err := os.Stat(filepath.Join(home, "workbuddy-workflow", "startup-environment.json")); !os.IsNotExist(err) {
		t.Fatal("ownership was not removed", err)
	}
}
