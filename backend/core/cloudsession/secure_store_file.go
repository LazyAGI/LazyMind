package cloudsession

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var ErrEncryptedFileStoreUnavailable = errors.New("encrypted Cloud token store is unavailable")

type encryptedFileTokenStore struct {
	mu   sync.Mutex
	path string
	key  []byte
	err  error
}

type encryptedFileEnvelope struct {
	Version    string `json:"version"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func NewEncryptedFileSecureTokenStore(path, keyFile string) SecureTokenStore {
	store := &encryptedFileTokenStore{path: filepath.Clean(strings.TrimSpace(path))}
	info, err := os.Stat(strings.TrimSpace(keyFile))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		store.err = ErrEncryptedFileStoreUnavailable
		return store
	}
	payload, err := os.ReadFile(strings.TrimSpace(keyFile))
	if err != nil {
		store.err = ErrEncryptedFileStoreUnavailable
		return store
	}
	decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(payload)))
	if err != nil {
		decoded, err = base64.StdEncoding.DecodeString(strings.TrimSpace(string(payload)))
	}
	if err != nil || len(decoded) != 32 || store.path == "." || store.path == "" {
		store.err = ErrEncryptedFileStoreUnavailable
		return store
	}
	store.key = decoded
	return store
}

func (store *encryptedFileTokenStore) Load(context.Context) (RefreshToken, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.err != nil {
		return "", store.err
	}
	payload, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNoRefreshToken
	}
	if err != nil {
		return "", err
	}
	var envelope encryptedFileEnvelope
	if json.Unmarshal(payload, &envelope) != nil || envelope.Version != "cloud-session/v1" {
		return "", ErrEncryptedFileStoreUnavailable
	}
	nonce, nonceErr := base64.RawStdEncoding.DecodeString(envelope.Nonce)
	ciphertext, ciphertextErr := base64.RawStdEncoding.DecodeString(envelope.Ciphertext)
	if nonceErr != nil || ciphertextErr != nil {
		return "", ErrEncryptedFileStoreUnavailable
	}
	gcm, err := store.gcm()
	if err != nil || len(nonce) != gcm.NonceSize() {
		return "", ErrEncryptedFileStoreUnavailable
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte("lazymind-cloud-session/v1"))
	if err != nil || len(plaintext) == 0 {
		return "", ErrEncryptedFileStoreUnavailable
	}
	return string(plaintext), nil
}

func (store *encryptedFileTokenStore) Save(_ context.Context, token RefreshToken) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.err != nil || strings.TrimSpace(token) == "" {
		return ErrEncryptedFileStoreUnavailable
	}
	gcm, err := store.gcm()
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	envelope := encryptedFileEnvelope{
		Version: "cloud-session/v1", Nonce: base64.RawStdEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawStdEncoding.EncodeToString(gcm.Seal(nil, nonce, []byte(token), []byte("lazymind-cloud-session/v1"))),
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	directory := filepath.Dir(store.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".cloud-session-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(payload); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, store.path)
}

func (store *encryptedFileTokenStore) Delete(context.Context) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.err != nil {
		return store.err
	}
	err := os.Remove(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (store *encryptedFileTokenStore) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(store.key)
	if err != nil {
		return nil, ErrEncryptedFileStoreUnavailable
	}
	return cipher.NewGCM(block)
}
