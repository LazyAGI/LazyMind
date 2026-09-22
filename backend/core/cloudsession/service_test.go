package cloudsession

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"lazymind/core/cloudclient"
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

type failingAuthClient struct{ err error }

type blockedStatusAuthClient struct {
	entered chan struct{}
	release chan struct{}
}

func (client blockedStatusAuthClient) Refresh(ctx context.Context, _ RefreshToken) (TokenPair, error) {
	close(client.entered)
	select {
	case <-client.release:
		return TokenPair{}, errors.New("Cloud unavailable")
	case <-ctx.Done():
		return TokenPair{}, ctx.Err()
	}
}

func (client blockedStatusAuthClient) Logout(ctx context.Context, _ string, _ RefreshToken) error {
	_, err := client.Refresh(ctx, "")
	return err
}

func TestPublicSessionAvailabilityStopsBeforeRemoteLogoutCompletes(t *testing.T) {
	auth := blockedStatusAuthClient{entered: make(chan struct{}), release: make(chan struct{})}
	store := &fakeSecureStore{}
	service := NewService(ServiceDeps{Store: store, Auth: auth})
	if err := service.Establish(context.Background(), TokenPair{AccessToken: "access", RefreshToken: "refresh", AccessExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	loggedOut := make(chan struct{})
	go func() { _ = service.Logout(context.Background()); close(loggedOut) }()
	defer func() { close(auth.release); <-loggedOut }()
	<-auth.entered
	read := make(chan Status, 1)
	go func() {
		status := service.Status(context.Background())
		if service.CloudBusinessAvailable() {
			status.State = "unexpected-available"
		}
		read <- status
	}()
	select {
	case status := <-read:
		if status.State != StateSignedOut || status.AccessToken != "" {
			t.Fatalf("logout still exposes an available Cloud session: %+v", status)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("remote logout blocked local availability checks")
	}
}

func TestPublicSessionStatusDoesNotWaitForCloudRestore(t *testing.T) {
	auth := blockedStatusAuthClient{entered: make(chan struct{}), release: make(chan struct{})}
	service := NewService(ServiceDeps{Store: &fakeSecureStore{token: "saved-refresh"}, Auth: auth})
	restored := make(chan struct{})
	go func() { _ = service.Restore(context.Background()); close(restored) }()
	defer func() { close(auth.release); <-restored }()
	<-auth.entered

	read := make(chan Status, 1)
	go func() {
		status := service.Status(context.Background())
		if service.CloudBusinessAvailable() {
			status.State = "unexpected-available"
		}
		read <- status
	}()
	select {
	case status := <-read:
		if status.State != StateRefreshing || !status.Configured || status.AccessToken != "" {
			t.Fatalf("unexpected public restore status: %+v", status)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Cloud restore blocked public status and local availability checks")
	}
}

func (client failingAuthClient) Refresh(context.Context, RefreshToken) (TokenPair, error) {
	return TokenPair{}, client.err
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

func TestRefreshSeparatesOfflineFromReauthenticationAndPreservesStoredToken(t *testing.T) {
	tests := []struct {
		name             string
		err              error
		wantState        State
		wantReachability Reachability
	}{
		{
			name:      "transport failure",
			err:       errors.New("fixture network unavailable"),
			wantState: StateOffline, wantReachability: ReachabilityUnreachable,
		},
		{
			name:      "invalid credentials",
			err:       &cloudclient.CloudError{HTTPStatus: http.StatusUnauthorized},
			wantState: StateReauthRequired, wantReachability: ReachabilityReachable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeSecureStore{token: "stored-refresh"}
			service := NewService(ServiceDeps{Store: store, Auth: failingAuthClient{err: test.err}})
			if err := service.Restore(context.Background()); err == nil {
				t.Fatal("failed refresh unexpectedly succeeded")
			}
			status := service.Status(context.Background())
			if status.State != test.wantState || status.Reachability != test.wantReachability {
				t.Fatalf("status=%+v want state=%q reachability=%q", status, test.wantState, test.wantReachability)
			}
			if store.token != "stored-refresh" {
				t.Fatalf("temporary Cloud failure deleted the stored refresh token: %q", store.token)
			}
		})
	}
}
