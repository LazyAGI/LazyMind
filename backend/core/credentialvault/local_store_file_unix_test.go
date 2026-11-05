//go:build linux || darwin

package credentialvault

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func fileStoreTestDir(t *testing.T) string {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	return filepath.Join(root, "keys")
}
func TestFileKeyStorePersistsPrivateScopedKeys(t *testing.T) {
	dir := fileStoreTestDir(t)
	s := newFileLocalKeyStore(dir)
	ctx := context.Background()
	scope := AccountScope{CloudIssuer: "lazymind-local", CloudAccountID: "u"}
	value := bytes.Repeat([]byte{42}, 32)
	if e := s.Save(ctx, scope, LocalRootKey, value); e != nil {
		t.Fatal(e)
	}
	got, e := newFileLocalKeyStore(dir).Load(ctx, scope, LocalRootKey)
	if e != nil || !bytes.Equal(got, value) {
		t.Fatal(got, e)
	}
	info, _ := os.Stat(dir)
	if info.Mode().Perm() != 0700 {
		t.Fatal("directory permissions")
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		fi, _ := entry.Info()
		if fi.Mode().Perm() != 0600 {
			t.Fatal("file permissions")
		}
	}
	other := scope
	other.CloudAccountID = "v"
	if _, e := s.Load(ctx, other, LocalRootKey); !errors.Is(e, ErrLocalKeyNotFound) {
		t.Fatal(e)
	}
	if e := s.Delete(ctx, scope, LocalRootKey); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Load(ctx, scope, LocalRootKey); !errors.Is(e, ErrLocalKeyNotFound) {
		t.Fatal(e)
	}
}
func TestFileKeyStoreRejectsSymlinksAndUnsafePermissions(t *testing.T) {
	for _, mode := range []string{"directory_link", "file_link", "directory_mode", "file_mode"} {
		t.Run(mode, func(t *testing.T) {
			dir := fileStoreTestDir(t)
			scope := AccountScope{CloudIssuer: "lazymind-local", CloudAccountID: "u"}
			s := newFileLocalKeyStore(dir)
			ctx := context.Background()
			if e := s.Save(ctx, scope, LocalRootKey, []byte("secret")); e != nil {
				t.Fatal(e)
			}
			entries, _ := os.ReadDir(dir)
			path := filepath.Join(dir, entries[0].Name())
			switch mode {
			case "directory_link":
				os.Rename(dir, dir+"-real")
				os.Symlink(dir+"-real", dir)
			case "file_link":
				os.Rename(path, path+"-real")
				os.Symlink(path+"-real", path)
			case "directory_mode":
				os.Chmod(dir, 0755)
			case "file_mode":
				os.Chmod(path, 0644)
			}
			if _, e := s.Load(ctx, scope, LocalRootKey); !errors.Is(e, ErrLocalSecureStoreUnavailable) {
				t.Fatalf("unsafe load accepted %v", e)
			}
		})
	}
}
func TestFileKeyStoreConcurrentCreationCannotReplaceRoot(t *testing.T) {
	dir := fileStoreTestDir(t)
	scope := AccountScope{CloudIssuer: "lazymind-local", CloudAccountID: "u"}
	var wg sync.WaitGroup
	winner := make(chan []byte, 16)
	for i := byte(0); i < 16; i++ {
		wg.Add(1)
		go func(v byte) {
			defer wg.Done()
			key := bytes.Repeat([]byte{v}, 32)
			if newFileLocalKeyStore(dir).Save(context.Background(), scope, LocalRootKey, key) == nil {
				winner <- key
			}
		}(i)
	}
	wg.Wait()
	close(winner)
	count := 0
	for key := range winner {
		count++
		got, e := newFileLocalKeyStore(dir).Load(context.Background(), scope, LocalRootKey)
		if e != nil || !bytes.Equal(got, key) {
			t.Fatal("published key overwritten", e)
		}
	}
	if count != 1 {
		t.Fatal("expected one atomic winner", count)
	}
}

func TestFileKeyStoreSaveIsIdempotentButNeverReplacesExistingKey(t *testing.T) {
	s := newFileLocalKeyStore(fileStoreTestDir(t))
	scope := AccountScope{CloudIssuer: "lazymind-local", CloudAccountID: "u"}
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if e := s.Save(ctx, scope, LocalRootKey, []byte("original")); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.Save(ctx, scope, LocalRootKey, []byte("replacement")); !errors.Is(e, ErrLocalSecureStoreUnavailable) {
		t.Fatal("overwrote root", e)
	}
	got, e := s.Load(ctx, scope, LocalRootKey)
	if e != nil || string(got) != "original" {
		t.Fatal("root changed", e)
	}
}
