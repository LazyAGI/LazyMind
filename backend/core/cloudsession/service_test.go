package cloudsession

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fakeSecureStore struct {
	mu    sync.Mutex
	token string
}

func (s *fakeSecureStore) Load(context.Context) (RefreshToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return RefreshToken(s.token), nil
}

func (s *fakeSecureStore) Save(_ context.Context, token RefreshToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = string(token)
	return nil
}

func (s *fakeSecureStore) Delete(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = ""
	return nil
}

type fakeAuthClient struct {
	mu           sync.Mutex
	refreshCalls int
}

func (c *fakeAuthClient) Refresh(_ context.Context, _ RefreshToken) (TokenPair, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshCalls++
	return TokenPair{AccessToken: "new-access", RefreshToken: "new-refresh", AccessExpiresAt: time.Now().Add(10 * time.Minute)}, nil
}

func TestRestorePublishesSessionOnlyAfterRefreshTokenRotatesSafely(t *testing.T) {
	store := &fakeSecureStore{token: "old-refresh"}
	client := &fakeAuthClient{}
	service := NewService(ServiceDeps{Store: store, Auth: client})

	if err := service.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	status := service.Status(context.Background())
	if status.State != StateSignedIn || status.AccessToken != "" {
		t.Fatalf("unsafe public status: %+v", status)
	}
	if store.token != "new-refresh" {
		t.Fatalf("refresh token was not rotated in secure store: %q", store.token)
	}
}

func TestConcurrentAccessTokenRefreshUsesSingleflight(t *testing.T) {
	store := &fakeSecureStore{token: "old-refresh"}
	client := &fakeAuthClient{}
	service := NewService(ServiceDeps{Store: store, Auth: client})

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = service.AccessToken(context.Background(), 9*time.Minute)
		}()
	}
	wg.Wait()
	if client.refreshCalls != 1 {
		t.Fatalf("refresh calls=%d want=1", client.refreshCalls)
	}
}

func TestLogoutClearsLocalStateWhenCloudResultIsUnknown(t *testing.T) {
	store := &fakeSecureStore{token: "refresh"}
	service := NewService(ServiceDeps{Store: store, Auth: &fakeAuthClient{}})
	_ = service.Logout(context.Background())
	if store.token != "" {
		t.Fatalf("refresh token remains after logout: %q", store.token)
	}
	if got := service.Status(context.Background()).State; got != StateSignedOut {
		t.Fatalf("state=%q want=%q", got, StateSignedOut)
	}
}

func TestLogoutIsSafeBeforeCloudSessionIsConfigured(t *testing.T) {
	service := NewService(ServiceDeps{})
	if err := service.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := service.Status(context.Background()).State; got != StateSignedOut {
		t.Fatalf("state=%q want=%q", got, StateSignedOut)
	}
}

func TestEstablishPersistsRefreshBeforePublishingSignedIn(t *testing.T) {
	store := &fakeSecureStore{}
	service := NewService(ServiceDeps{Store: store})
	err := service.Establish(context.Background(), TokenPair{
		AccessToken: "access", RefreshToken: "refresh", AccessExpiresAt: time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.token != "refresh" || service.Status(context.Background()).State != StateSignedIn {
		t.Fatalf("store=%q status=%+v", store.token, service.Status(context.Background()))
	}
}
