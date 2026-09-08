package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalWorkspaceHostTokenUsesOwnerOrRunToken(t *testing.T) {
	paths := RuntimePaths{RunDirTokenFile: filepath.Join(t.TempDir(), "token")}
	if err := os.WriteFile(paths.RunDirTokenFile, []byte("run-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := localWorkspaceHostToken(RuntimeConfig{}, paths); got != "run-token" {
		t.Fatalf("run token=%q", got)
	}
	if got := localWorkspaceHostToken(RuntimeConfig{OwnerToken: "desktop-owner"}, paths); got != "desktop-owner" {
		t.Fatalf("owner token=%q", got)
	}
}
func TestWorkspaceTokenIsNotInjectedIntoAlgorithm(t *testing.T) {
	env := algorithmServiceEnv(RuntimeConfig{}, RuntimePaths{}, "chat")
	for _, item := range env {
		if len(item) >= len(localWorkspaceHostTokenEnvVar) && item[:len(localWorkspaceHostTokenEnvVar)] == localWorkspaceHostTokenEnvVar {
			t.Fatalf("algorithm received workspace token")
		}
	}
}
