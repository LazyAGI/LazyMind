package cloudsession

import (
	"context"
	"errors"
	"sync"
	"time"

	"lazymind/core/cloudclient"
)

type RefreshToken = string
type TokenPair = cloudclient.DesktopTokenPair

type State string

const (
	StateSignedOut      State = "signed_out"
	StateAuthorizing    State = "authorizing"
	StateExchanging     State = "exchanging"
	StateRestoring      State = "restoring"
	StateSignedIn       State = "signed_in"
	StateRefreshing     State = "refreshing"
	StateReauthRequired State = "reauth_required"
	StateOffline        State = "offline"
)

var ErrNoRefreshToken = errors.New("cloud refresh token is unavailable")

type SecureTokenStore interface {
	Load(context.Context) (RefreshToken, error)
	Save(context.Context, RefreshToken) error
	Delete(context.Context) error
}

type AuthClient interface {
	Refresh(context.Context, RefreshToken) (TokenPair, error)
}

type LogoutClient interface {
	Logout(context.Context, string, RefreshToken) error
}

type ServiceDeps struct {
	Store SecureTokenStore
	Auth  AuthClient
	Now   func() time.Time
}

type Status struct {
	State           State     `json:"state"`
	AccessToken     string    `json:"-"`
	AccessExpires   time.Time `json:"access_expires_at,omitempty"`
	AccountID       string    `json:"account_id,omitempty"`
	Username        string    `json:"username,omitempty"`
	EmailMasked     string    `json:"email_masked,omitempty"`
	RegistrationURL string    `json:"registration_url,omitempty"`
}

type Service struct {
	mu            sync.Mutex
	store         SecureTokenStore
	auth          AuthClient
	now           func() time.Time
	state         State
	accessToken   string
	accessExpires time.Time
}

func NewService(deps ServiceDeps) *Service {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &Service{store: deps.Store, auth: deps.Auth, now: now, state: StateSignedOut}
}

func (s *Service) Restore(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = StateRestoring
	return s.refreshLocked(ctx)
}

func (s *Service) Establish(ctx context.Context, pair TokenPair) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil || pair.AccessToken == "" || pair.RefreshToken == "" || !pair.AccessExpiresAt.After(s.now()) {
		s.clearLocked(StateReauthRequired)
		return errors.New("cloud login returned an incomplete token pair")
	}
	if err := s.store.Save(ctx, pair.RefreshToken); err != nil {
		s.clearLocked(StateReauthRequired)
		return err
	}
	s.accessToken = pair.AccessToken
	s.accessExpires = pair.AccessExpiresAt
	s.state = StateSignedIn
	return nil
}

func (s *Service) setState(state State) {
	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
}

func (s *Service) AccessToken(ctx context.Context, minimumTTL time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accessToken != "" && s.accessExpires.Sub(s.now()) >= minimumTTL {
		return s.accessToken, nil
	}
	if err := s.refreshLocked(ctx); err != nil {
		return "", err
	}
	return s.accessToken, nil
}

func (s *Service) Status(context.Context) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{State: s.state, AccessExpires: s.accessExpires}
}

func (s *Service) Logout(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		s.clearLocked(StateSignedOut)
		return nil
	}
	var remoteErr error
	refreshToken, loadErr := s.store.Load(ctx)
	if loadErr == nil && refreshToken != "" {
		if client, ok := s.auth.(LogoutClient); ok {
			remoteErr = client.Logout(ctx, s.accessToken, refreshToken)
		}
	}
	deleteErr := s.store.Delete(ctx)
	s.clearLocked(StateSignedOut)
	if deleteErr != nil {
		return deleteErr
	}
	return remoteErr
}

func (s *Service) refreshLocked(ctx context.Context) error {
	if s.store == nil || s.auth == nil {
		s.clearLocked(StateSignedOut)
		return ErrNoRefreshToken
	}
	refreshToken, err := s.store.Load(ctx)
	if err != nil || refreshToken == "" {
		s.clearLocked(StateSignedOut)
		if err != nil {
			return err
		}
		return ErrNoRefreshToken
	}
	s.state = StateRefreshing
	pair, err := s.auth.Refresh(ctx, refreshToken)
	if err != nil {
		s.clearLocked(StateReauthRequired)
		return err
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" || !pair.AccessExpiresAt.After(s.now()) {
		s.clearLocked(StateReauthRequired)
		return errors.New("cloud refresh returned an incomplete token pair")
	}
	if err := s.store.Save(ctx, pair.RefreshToken); err != nil {
		s.clearLocked(StateReauthRequired)
		return err
	}
	s.accessToken = pair.AccessToken
	s.accessExpires = pair.AccessExpiresAt
	s.state = StateSignedIn
	return nil
}

func (s *Service) clearLocked(state State) {
	s.state = state
	s.accessToken = ""
	s.accessExpires = time.Time{}
}
