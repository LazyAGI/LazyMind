package workbuddy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoginWaitsForValidSessionInsteadOfApplicationLaunch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workbuddy-desktop.info")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	launched := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- waitForLogin(ctx, path, func(context.Context) error { close(launched); return nil }, time.Millisecond)
	}()
	<-launched
	select {
	case err := <-result:
		t.Fatalf("launch falsely completed login: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	if err := os.WriteFile(path, []byte(sessionFixture(t, time.Now().Add(time.Hour).UnixMilli(), 0)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestLoginPreservesAlreadyAuthenticatedSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workbuddy-desktop.info")
	if err := os.WriteFile(path, []byte(sessionFixture(t, time.Now().Add(time.Hour).UnixMilli(), 0)), 0600); err != nil {
		t.Fatal(err)
	}
	err := waitForLogin(context.Background(), path, func(context.Context) error { t.Fatal("unnecessary application launch"); return nil }, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
}

func TestLoginReportsLaunchFailureAndCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workbuddy-desktop.info")
	launchErr := errors.New("fixture launch failure")
	if err := waitForLogin(context.Background(), path, func(context.Context) error { return launchErr }, time.Millisecond); !errors.Is(err, launchErr) {
		t.Fatalf("got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	err := waitForLogin(ctx, path, func(context.Context) error { cancel(); return nil }, time.Millisecond)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if ready, _ := availability(path); ready {
		t.Fatal("canceled login marked ready")
	}
}
