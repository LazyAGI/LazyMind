package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

func TestRecoveryStatusExistsWithoutUpdateConfigurationAndRedactsPaths(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "recovery")
	data := filepath.Join(base, "PRIVATE-DATA")
	app := filepath.Join(base, "app")
	for _, dir := range []string{data, app} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	plan := desktopupdate.TransactionPlan{TrustDomain: "fixture", Audience: "fixture-personal", Channel: "personal-test", Mode: "unsigned-test", Platform: "darwin", Arch: "arm64", SigningIdentity: "adhoc", UpdaterProtocol: 1, Scope: desktopupdate.UpdateScope{Installation: app, DataRoots: []string{data}}, SourceID: "source", TargetID: "target", SourceVersion: "1.0.0", TargetVersion: "1.1.0", SourceManifestSHA256: strings.Repeat("a", 64), SourcePackageSHA256: strings.Repeat("b", 64), TargetPackageSHA256: strings.Repeat("c", 64), PolicySequence: 1, PolicyDigest: strings.Repeat("d", 64), DataProfile: "fixture", TargetDataProfile: "fixture", Units: []desktopupdate.SnapshotUnit{{Path: data}}, Reservations: []desktopupdate.InstallSpaceReservation{{Directory: base, InstallBytes: 1024, RecoveryBytes: 1024}}, OwnerToken: "PRIVATE-OWNER"}
	tx, err := desktopupdate.BeginUpdateTransaction(context.Background(), root, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	var out bytes.Buffer
	if err = run(context.Background(), []string{"recovery-status"}, strings.NewReader(""), &out, "", root, time.Now()); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Status       string `json:"status"`
		Transactions []struct {
			ID            string `json:"id"`
			Stage         string `json:"stage"`
			SourceVersion string `json:"sourceVersion"`
			TargetVersion string `json:"targetVersion"`
		} `json:"transactions"`
	}
	if err = json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "recovery_status" || len(result.Transactions) != 1 || result.Transactions[0].ID != tx.Journal().ID || result.Transactions[0].Stage != "PREPARED" {
		t.Fatalf("missing pending recovery status: %s", out.String())
	}
	for _, private := range []string{data, app, root, plan.OwnerToken} {
		if strings.Contains(out.String(), private) {
			t.Fatal("recovery summary leaked local data")
		}
	}
	if tx.Journal().Stage != "PREPARED" {
		t.Fatal("status read advanced transaction")
	}
}
