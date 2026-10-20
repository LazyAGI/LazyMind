package mcpclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func exitedDSHTestProcess(t *testing.T) string {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(binary, "-test.run=^$")
	if err := child.Run(); err != nil {
		t.Fatal(err)
	}
	return strconv.Itoa(child.ProcessState.Pid()) + "\n"
}

func TestDSHPatchLockPreservesLiveAndUnknownHolders(t *testing.T) {
	for _, record := range []string{strconv.Itoa(os.Getpid()) + "\n", "", "occupied", "0\n", "-1\n", "2147483648\n"} {
		t.Run(strconv.Quote(record), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cordis.patch.yml")
			writeTestFile(t, path+".lock", record)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			if unlock, err := lockDSHPatch(ctx, path); err == nil {
				unlock()
				t.Fatal("stole a live or unproven lock")
			}
			if body, err := os.ReadFile(path + ".lock"); err != nil || string(body) != record {
				t.Fatalf("changed holder record: %q %v", body, err)
			}
		})
	}
}

func TestDSHPatchLockWaitsForActiveWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cordis.patch.yml")
	unlock, err := lockDSHPatch(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	result := make(chan error, 1)
	go func() {
		nextUnlock, err := lockDSHPatch(context.Background(), path)
		if err == nil {
			nextUnlock()
		}
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("contender did not wait: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	if err := <-result; err != nil {
		t.Fatalf("could not acquire released lock: %v", err)
	}
}

func TestDSHPatchLockSharesDSHTakeoverClaim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cordis.patch.yml.lock")
	record := exitedDSHTestProcess(t)
	writeTestFile(t, path, record)
	sum := sha256.Sum256([]byte(record))
	claim := path + ".takeover-" + hex.EncodeToString(sum[:8])
	writeTestFile(t, claim, strconv.Itoa(os.Getpid())+"\n")
	if takeOverExitedDSHLock(path) {
		t.Fatal("ignored another contender's takeover claim")
	}
	if err := os.Remove(claim); err != nil {
		t.Fatal(err)
	}
	if !takeOverExitedDSHLock(path) || pathExists(path) || pathExists(claim) {
		t.Fatal("did not reclaim exited writer and release takeover claim")
	}
}
