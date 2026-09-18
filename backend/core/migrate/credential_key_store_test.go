package migrate

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"lazymind/core/credentialvault"
	"lazymind/core/modelprovider"
)

type migrationCredentialKeyStore struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (store *migrationCredentialKeyStore) key(scope credentialvault.AccountScope, kind credentialvault.LocalKeyKind) string {
	return scope.CloudIssuer + "\x00" + scope.CloudAccountID + "\x00" + string(kind)
}

func (store *migrationCredentialKeyStore) Load(_ context.Context, scope credentialvault.AccountScope, kind credentialvault.LocalKeyKind) ([]byte, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, found := store.values[store.key(scope, kind)]
	if !found {
		return nil, credentialvault.ErrLocalKeyNotFound
	}
	return append([]byte(nil), value...), nil
}

func (store *migrationCredentialKeyStore) Save(_ context.Context, scope credentialvault.AccountScope, kind credentialvault.LocalKeyKind, value []byte) error {
	store.mu.Lock()
	store.values[store.key(scope, kind)] = append([]byte(nil), value...)
	store.mu.Unlock()
	return nil
}

func (store *migrationCredentialKeyStore) Delete(_ context.Context, scope credentialvault.AccountScope, kind credentialvault.LocalKeyKind) error {
	store.mu.Lock()
	delete(store.values, store.key(scope, kind))
	store.mu.Unlock()
	return nil
}

func installMigrationCredentialKeyManager(t *testing.T) {
	t.Helper()
	manager, err := credentialvault.NewLocalKeyManager(
		&migrationCredentialKeyStore{values: make(map[string][]byte)},
		bytes.NewReader(bytes.Repeat([]byte{0x6b}, 64*1024)),
	)
	if err != nil {
		t.Fatal(err)
	}
	restore := modelprovider.SetCredentialKeyManager(manager)
	t.Cleanup(restore)
}
