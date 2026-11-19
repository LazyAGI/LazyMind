package workbuddyhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAccountFailureDoesNotRemovePersistentStartupChoice(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "workbuddy-workflow"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "workbuddy-workflow", "startup-environment.json")
	body := []byte(`{"previous":"","managed":"9333"}`)
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	Supervise(ctx, home, "http://127.0.0.1:1", func() (string, error) { return "", errors.New("signed out") })
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(body) {
		t.Fatal("account failure changed startup ownership", err)
	}
}
