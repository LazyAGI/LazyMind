package cloudsession

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func encryptedStoreFixture(t *testing.T, encoding *base64.Encoding) (SecureTokenStore, string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("encrypted-file mode uses POSIX permission checks; Windows defaults to its system credential store")
	}
	root := t.TempDir()
	keyPath := filepath.Join(root, "key")
	if err := os.WriteFile(keyPath, []byte(encoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))), 0o600); err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(root, "session", "token.enc")
	return NewEncryptedFileSecureTokenStore(tokenPath, keyPath), tokenPath, keyPath
}

func TestEncryptedFileStoreRoundTripRotationAndDeletion(t *testing.T) {
	for name, encoding := range map[string]*base64.Encoding{"raw base64": base64.RawStdEncoding, "padded base64": base64.StdEncoding} {
		t.Run(name, func(t *testing.T) {
			store, tokenPath, keyPath := encryptedStoreFixture(t, encoding)
			ctx := context.Background()
			if _, err := store.Load(ctx); !errors.Is(err, ErrNoRefreshToken) {
				t.Fatalf("missing token: %v", err)
			}
			if err := store.Save(ctx, "first-refresh-canary"); err != nil {
				t.Fatal(err)
			}
			first, err := os.ReadFile(tokenPath)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(first, []byte("first-refresh-canary")) {
				t.Fatal("token stored in plaintext")
			}
			for path, mode := range map[string]os.FileMode{tokenPath: 0o600, filepath.Dir(tokenPath): 0o700} {
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != mode {
					t.Fatalf("incorrect storage permissions for %s", filepath.Base(path))
				}
			}
			if token, err := NewEncryptedFileSecureTokenStore(tokenPath, keyPath).Load(ctx); err != nil || token != "first-refresh-canary" {
				t.Fatal("new store instance could not restore token")
			}
			if err := store.Save(ctx, "rotated-refresh-canary"); err != nil {
				t.Fatal(err)
			}
			if token, err := NewEncryptedFileSecureTokenStore(tokenPath, keyPath).Load(ctx); err != nil || token != "rotated-refresh-canary" {
				t.Fatal("rotated token was not persisted")
			}
			if matches, err := filepath.Glob(filepath.Join(filepath.Dir(tokenPath), ".cloud-session-*")); err != nil || len(matches) != 0 {
				t.Fatal("temporary token file remains after successful save")
			}
			if err := store.Delete(ctx); err != nil {
				t.Fatal(err)
			}
			if err := store.Delete(ctx); err != nil {
				t.Fatal("repeated deletion should be safe")
			}
			if _, err := store.Load(ctx); !errors.Is(err, ErrNoRefreshToken) {
				t.Fatal("deleted token remains restorable")
			}
		})
	}
}

func TestEncryptedFileStoreRejectsTamperingWithoutChangingStoredData(t *testing.T) {
	for _, mutation := range []string{"invalid JSON", "wrong version", "invalid nonce", "modified ciphertext", "wrong key"} {
		t.Run(mutation, func(t *testing.T) {
			store, tokenPath, keyPath := encryptedStoreFixture(t, base64.RawStdEncoding)
			ctx := context.Background()
			if err := store.Save(ctx, "refresh-canary"); err != nil {
				t.Fatal(err)
			}
			payload, err := os.ReadFile(tokenPath)
			if err != nil {
				t.Fatal(err)
			}
			var envelope encryptedFileEnvelope
			if err := json.Unmarshal(payload, &envelope); err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "wrong version":
				envelope.Version = "unsupported"
			case "invalid nonce":
				envelope.Nonce = "!invalid!"
			case "modified ciphertext":
				value, err := base64.RawStdEncoding.DecodeString(envelope.Ciphertext)
				if err != nil {
					t.Fatal(err)
				}
				value[len(value)-1] ^= 1
				envelope.Ciphertext = base64.RawStdEncoding.EncodeToString(value)
			case "wrong key":
				if err := os.WriteFile(keyPath, []byte(base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{0x43}, 32))), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			payload, err = json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			if mutation == "invalid JSON" {
				payload = []byte("{")
			}
			if err := os.WriteFile(tokenPath, payload, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := NewEncryptedFileSecureTokenStore(tokenPath, keyPath).Load(ctx); !errors.Is(err, ErrEncryptedFileStoreUnavailable) {
				t.Fatalf("unsafe token was accepted: %v", err)
			}
			after, err := os.ReadFile(tokenPath)
			if err != nil || !bytes.Equal(payload, after) {
				t.Fatal("failed read modified stored data")
			}
		})
	}
}

func TestEncryptedFileStoreRejectsUnavailableOrUnsafeKeys(t *testing.T) {
	for _, scenario := range []string{"missing", "public permissions", "invalid base64", "wrong length"} {
		t.Run(scenario, func(t *testing.T) {
			_, tokenPath, keyPath := encryptedStoreFixture(t, base64.RawStdEncoding)
			var err error
			switch scenario {
			case "missing":
				err = os.Remove(keyPath)
			case "public permissions":
				err = os.Chmod(keyPath, 0o644)
			case "invalid base64":
				err = os.WriteFile(keyPath, []byte("!invalid!"), 0o600)
			case "wrong length":
				err = os.WriteFile(keyPath, []byte(base64.RawStdEncoding.EncodeToString([]byte("short"))), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			store := NewEncryptedFileSecureTokenStore(tokenPath, keyPath)
			if _, err := store.Load(context.Background()); !errors.Is(err, ErrEncryptedFileStoreUnavailable) {
				t.Fatalf("unsafe key load: %v", err)
			}
			if err := store.Save(context.Background(), "refresh-canary"); !errors.Is(err, ErrEncryptedFileStoreUnavailable) {
				t.Fatalf("unsafe key save: %v", err)
			}
			if _, err := os.Stat(tokenPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed save created a token file")
			}
		})
	}
}

func TestEncryptedFileStoreDoesNotHideDeletionFailure(t *testing.T) {
	_, tokenPath, keyPath := encryptedStoreFixture(t, base64.RawStdEncoding)
	if err := os.MkdirAll(tokenPath, 0o700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(tokenPath, "unrelated-file")
	if err := os.WriteFile(child, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewEncryptedFileSecureTokenStore(tokenPath, keyPath)
	if err := store.Delete(context.Background()); err == nil {
		t.Fatal("deletion failure was hidden")
	}
	if _, err := os.Stat(child); err != nil {
		t.Fatal("failed deletion removed unrelated data")
	}
}
