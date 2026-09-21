package cloudsession

import (
	"context"
	"errors"
	"sync"
)

type memorySecureTokenStore struct {
	mu    sync.Mutex
	token RefreshToken
}

func NewMemorySecureTokenStore() SecureTokenStore {
	return &memorySecureTokenStore{}
}

func (store *memorySecureTokenStore) Load(ctx context.Context) (RefreshToken, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.token == "" {
		return "", ErrNoRefreshToken
	}
	return store.token, nil
}

func (store *memorySecureTokenStore) Save(ctx context.Context, token RefreshToken) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if token == "" {
		return errors.New("Cloud refresh token is empty")
	}
	store.mu.Lock()
	store.token = token
	store.mu.Unlock()
	return nil
}

func (store *memorySecureTokenStore) Delete(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	store.token = ""
	store.mu.Unlock()
	return nil
}
