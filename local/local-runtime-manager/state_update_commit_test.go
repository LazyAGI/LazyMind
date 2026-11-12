package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeCommitReceiptSurvivesStaleRuntimeWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime-state.json")
	tx := strings.Repeat("a", 32)
	stale := RuntimeState{OwnerToken: "fixture-owner", RuntimeRoot: filepath.Dir(path), ResourcesRoot: "/fixture/resources", Config: RuntimeConfigSnapshot{UpdateBootstrap: &RuntimeUpdateBootstrap{TransactionID: tx}}}
	committed := stale
	committed.Config.NativeCommittedTx = tx
	if err := writeRuntimeState(path, committed); err != nil {
		t.Fatal(err)
	}
	if err := writeRuntimeState(path, stale); err != nil {
		t.Fatal(err)
	}
	got, err := readRuntimeState(path)
	if err != nil || got.Config.NativeCommittedTx != tx || got.Config.UpdateBootstrap == nil {
		t.Fatalf("lost committed receipt before peer ACK: %+v, %v", got.Config, err)
	}
	committed.Config.UpdateBootstrap = nil
	if err := writeRuntimeState(path, committed); err != nil {
		t.Fatal(err)
	}
	if err := writeRuntimeState(path, stale); err != nil {
		t.Fatal(err)
	}
	got, err = readRuntimeState(path)
	if err != nil || got.Config.NativeCommittedTx != tx || got.Config.UpdateBootstrap != nil {
		t.Fatalf("resurrected acknowledged bootstrap: %+v, %v", got.Config, err)
	}
	// A different owner cannot inherit authority from the previous receipt.
	stale.OwnerToken = "fixture-next-owner"
	stale.Config.UpdateBootstrap = nil
	if err := writeRuntimeState(path, stale); err != nil {
		t.Fatal(err)
	}
	got, err = readRuntimeState(path)
	if err != nil || got.Config.NativeCommittedTx != "" {
		t.Fatalf("receipt crossed runtime ownership: %+v, %v", got.Config, err)
	}
}
