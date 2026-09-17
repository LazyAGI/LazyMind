package credentialvault

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type d2MemoryKeyStore struct {
	mu      sync.Mutex
	values  map[string][]byte
	loadErr error
	saveErr error
	saves   int
}

func newD2MemoryKeyStore() *d2MemoryKeyStore {
	return &d2MemoryKeyStore{values: make(map[string][]byte)}
}

func d2KeyStoreKey(scope AccountScope, kind LocalKeyKind) string {
	return scope.CloudIssuer + "\x00" + scope.CloudAccountID + "\x00" + string(kind)
}

func (store *d2MemoryKeyStore) Load(ctx context.Context, scope AccountScope, kind LocalKeyKind) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.loadErr != nil {
		return nil, store.loadErr
	}
	value, found := store.values[d2KeyStoreKey(scope, kind)]
	if !found {
		return nil, ErrLocalKeyNotFound
	}
	return append([]byte(nil), value...), nil
}

func (store *d2MemoryKeyStore) Save(ctx context.Context, scope AccountScope, kind LocalKeyKind, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.saveErr != nil {
		return store.saveErr
	}
	store.saves++
	store.values[d2KeyStoreKey(scope, kind)] = append([]byte(nil), value...)
	return nil
}

func (store *d2MemoryKeyStore) Delete(_ context.Context, scope AccountScope, kind LocalKeyKind) error {
	store.mu.Lock()
	delete(store.values, d2KeyStoreKey(scope, kind))
	store.mu.Unlock()
	return nil
}

func TestLocalRootKeyIsGeneratedOnceAndScopedToCloudAccount(t *testing.T) {
	store := newD2MemoryKeyStore()
	random := bytes.NewReader(append(bytes.Repeat([]byte{0x41}, 32), bytes.Repeat([]byte{0x42}, 224)...))
	manager, err := NewLocalKeyManager(store, random)
	if err != nil {
		t.Fatal(err)
	}
	scopeA := AccountScope{CloudIssuer: "https://cloud.example.test", CloudAccountID: "account-a"}
	first, err := manager.RootKey(context.Background(), scopeA)
	if err != nil {
		t.Fatalf("create local root key: %v", err)
	}
	second, err := manager.RootKey(context.Background(), scopeA)
	if err != nil {
		t.Fatalf("reload local root key: %v", err)
	}
	if len(first) != 32 || !bytes.Equal(first, second) || store.saves != 1 {
		t.Fatalf("root key length/reuse/saves = %d/%t/%d, want 32/true/1", len(first), bytes.Equal(first, second), store.saves)
	}
	scopeB := AccountScope{CloudIssuer: scopeA.CloudIssuer, CloudAccountID: "account-b"}
	third, err := manager.RootKey(context.Background(), scopeB)
	if err != nil {
		t.Fatalf("create second account root key: %v", err)
	}
	if bytes.Equal(first, third) {
		t.Fatal("two Cloud accounts shared one local root key")
	}
}

func TestLocalSigningKeyIsEd25519AndNeverLeavesSecureStoreAsMetadata(t *testing.T) {
	store := newD2MemoryKeyStore()
	manager, err := NewLocalKeyManager(store, bytes.NewReader(bytes.Repeat([]byte{0x52}, 512)))
	if err != nil {
		t.Fatal(err)
	}
	scope := AccountScope{CloudIssuer: "https://cloud.example.test", CloudAccountID: "account-signing"}
	privateKey, err := manager.SigningKey(context.Background(), scope)
	if err != nil {
		t.Fatalf("create signing key: %v", err)
	}
	if len(privateKey) != ed25519.PrivateKeySize || len(privateKey.Public().(ed25519.PublicKey)) != ed25519.PublicKeySize {
		t.Fatalf("signing key sizes = %d/%d", len(privateKey), len(privateKey.Public().(ed25519.PublicKey)))
	}
	stored := store.values[d2KeyStoreKey(scope, RecordSigningKey)]
	if len(stored) != ed25519.PrivateKeySize || bytes.Contains(stored, []byte("BEGIN PRIVATE KEY")) {
		t.Fatalf("secure signing key encoding is invalid: length=%d", len(stored))
	}
}

func TestLocalKeyManagerFailsClosedWhenSystemSecureStoreIsUnavailable(t *testing.T) {
	store := newD2MemoryKeyStore()
	store.loadErr = errors.New("fixture OS secure store unavailable")
	manager, err := NewLocalKeyManager(store, bytes.NewReader(bytes.Repeat([]byte{0x63}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.RootKey(context.Background(), AccountScope{CloudIssuer: "https://cloud.example.test", CloudAccountID: "account-a"})
	if !errors.Is(err, ErrLocalSecureStoreUnavailable) {
		t.Fatalf("secure-store failure = %v, want ErrLocalSecureStoreUnavailable", err)
	}
	if store.saves != 0 {
		t.Fatalf("secure-store failure attempted %d writes", store.saves)
	}
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "fixture os secure store") {
		t.Fatal("raw operating-system secure-store error escaped the local error boundary")
	}
}

var _ LocalKeyStore = (*d2MemoryKeyStore)(nil)

type firstReadBlockedKeyStore struct {
	*d2MemoryKeyStore
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (store *firstReadBlockedKeyStore) Load(ctx context.Context, scope AccountScope, kind LocalKeyKind) ([]byte, error) {
	value, err := store.d2MemoryKeyStore.Load(ctx, scope, kind)
	store.once.Do(func() { close(store.entered); <-store.release })
	return value, err
}

func TestFirstLocalRootKeyIsSharedByConcurrentRequests(t *testing.T) {
	store := &firstReadBlockedKeyStore{d2MemoryKeyStore: newD2MemoryKeyStore(), entered: make(chan struct{}), release: make(chan struct{})}
	manager, err := NewLocalKeyManager(store, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	scope := AccountScope{CloudIssuer: "lazymind-local", CloudAccountID: "local-user"}
	type result struct {
		key []byte
		err error
	}
	first, second := make(chan result, 1), make(chan result, 1)
	go func() { key, err := manager.RootKey(context.Background(), scope); first <- result{key, err} }()
	<-store.entered
	go func() { key, err := manager.RootKey(context.Background(), scope); second <- result{key, err} }()
	var b result
	secondDone := false
	select {
	case b = <-second:
		secondDone = true
	case <-time.After(100 * time.Millisecond):
	}
	close(store.release)
	a := <-first
	if !secondDone {
		b = <-second
	}
	if a.err != nil || b.err != nil {
		t.Fatalf("concurrent creation failed: %v / %v", a.err, b.err)
	}
	if !bytes.Equal(a.key, b.key) || store.saves != 1 {
		t.Fatalf("first requests generated different keys or overwrote the root key: saves=%d", store.saves)
	}
}
