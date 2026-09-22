package modelprovider

import (
	"bytes"
	"context"
	"os"
	"sync"
	"testing"

	"lazymind/core/credentialvault"
)

type modelProviderTestKeyStore struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (store *modelProviderTestKeyStore) key(scope credentialvault.AccountScope, kind credentialvault.LocalKeyKind) string {
	return scope.CloudIssuer + "\x00" + scope.CloudAccountID + "\x00" + string(kind)
}

func (store *modelProviderTestKeyStore) Load(_ context.Context, scope credentialvault.AccountScope, kind credentialvault.LocalKeyKind) ([]byte, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, found := store.values[store.key(scope, kind)]
	if !found {
		return nil, credentialvault.ErrLocalKeyNotFound
	}
	return append([]byte(nil), value...), nil
}

func (store *modelProviderTestKeyStore) Save(_ context.Context, scope credentialvault.AccountScope, kind credentialvault.LocalKeyKind, value []byte) error {
	store.mu.Lock()
	store.values[store.key(scope, kind)] = append([]byte(nil), value...)
	store.mu.Unlock()
	return nil
}

func (store *modelProviderTestKeyStore) Delete(_ context.Context, scope credentialvault.AccountScope, kind credentialvault.LocalKeyKind) error {
	store.mu.Lock()
	delete(store.values, store.key(scope, kind))
	store.mu.Unlock()
	return nil
}

func TestMain(m *testing.M) {
	store := &modelProviderTestKeyStore{values: make(map[string][]byte)}
	manager, err := credentialvault.NewLocalKeyManager(store, bytes.NewReader(bytes.Repeat([]byte{0x5a}, 64*1024)))
	if err != nil {
		panic(err)
	}
	restore := SetCredentialKeyManager(manager)
	code := m.Run()
	restore()
	os.Exit(code)
}

var _ credentialvault.LocalKeyStore = (*modelProviderTestKeyStore)(nil)
