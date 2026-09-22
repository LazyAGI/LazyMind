package cloudsession

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"lazymind/core/cloudclient"
)

type RefreshToken = string
type TokenPair = cloudclient.DesktopTokenPair

type State string
type Reachability string

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

const (
	ReachabilityUnknown     Reachability = "unknown"
	ReachabilityChecking    Reachability = "checking"
	ReachabilityReachable   Reachability = "reachable"
	ReachabilityUnreachable Reachability = "unreachable"
)

var ErrNoRefreshToken = errors.New("cloud refresh token is unavailable")

var ErrLocalLogoutFailed = errors.New("cloud session could not be cleared from local storage")

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
	State           State        `json:"state"`
	Configured      bool         `json:"configured"`
	Reachability    Reachability `json:"reachability"`
	AccessToken     string       `json:"-"`
	AccessExpires   time.Time    `json:"access_expires_at,omitempty"`
	AccountID       string       `json:"account_id,omitempty"`
	Username        string       `json:"username,omitempty"`
	EmailMasked     string       `json:"email_masked,omitempty"`
	RegistrationURL string       `json:"registration_url,omitempty"`
}

type Service struct {
	mu            sync.Mutex
	publicStatus  atomic.Pointer[Status]
	store         SecureTokenStore
	auth          AuthClient
	now           func() time.Time
	state         State
	accessToken   string
	accessExpires time.Time
	configured    bool
	reachability  Reachability
}

func NewService(deps ServiceDeps) *Service {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	configured := deps.Store != nil && deps.Auth != nil
	service := &Service{
		store: deps.Store, auth: deps.Auth, now: now, state: StateSignedOut,
		configured: configured, reachability: ReachabilityUnknown,
	}
	service.publishStatusLocked()
	return service
}

func (s *Service) Restore(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = StateRestoring
	s.publishStatusLocked()
	return s.refreshLocked(ctx)
}

func (s *Service) Establish(ctx context.Context, pair TokenPair) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.publishStatusLocked()
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
	s.reachability = ReachabilityReachable
	return nil
}

func (s *Service) setState(state State) {
	s.mu.Lock()
	s.state = state
	s.publishStatusLocked()
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
	if status := s.publicStatus.Load(); status != nil {
		return *status
	}
	return Status{}
}

// Login transitions still need the authoritative state after any in-flight
// credential operation completes; only public status reads use the snapshot.
func (s *Service) currentState() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Publish only credential-free state. Status readers must never wait for
// refresh-token storage or Cloud network I/O protected by mu.
func (s *Service) publishStatusLocked() {
	s.publicStatus.Store(&Status{
		State: s.state, Configured: s.configured, Reachability: s.reachability,
		AccessExpires: s.accessExpires,
	})
}

func (s *Service) SetReachability(reachability Reachability) {
	if s == nil {
		return
	}
	if reachability != ReachabilityUnknown && reachability != ReachabilityChecking &&
		reachability != ReachabilityReachable && reachability != ReachabilityUnreachable {
		return
	}
	s.mu.Lock()
	s.reachability = reachability
	if reachability == ReachabilityUnreachable && s.state == StateSignedIn {
		s.state = StateOffline
	}
	s.publishStatusLocked()
	s.mu.Unlock()
}

func (s *Service) CloudBusinessAvailable() bool {
	if s == nil {
		return false
	}
	status := s.Status(context.Background())
	return status.Configured && status.Reachability == ReachabilityReachable && status.State == StateSignedIn
}

func (s *Service) Logout(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.publishStatusLocked()
	s.state = StateSignedOut
	s.publishStatusLocked()
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
		return errors.Join(ErrLocalLogoutFailed, deleteErr)
	}
	return remoteErr
}

func (s *Service) refreshLocked(ctx context.Context) error {
	defer s.publishStatusLocked()
	if s.store == nil || s.auth == nil {
		s.clearLocked(StateSignedOut)
		return ErrNoRefreshToken
	}
	s.state = StateRefreshing
	s.publishStatusLocked()
	refreshToken, err := s.store.Load(ctx)
	if err != nil || refreshToken == "" {
		s.clearLocked(StateSignedOut)
		if err != nil {
			return err
		}
		return ErrNoRefreshToken
	}
	pair, err := s.auth.Refresh(ctx, refreshToken)
	if err != nil {
		var cloudErr *cloudclient.CloudError
		if errors.As(err, &cloudErr) && (cloudErr.HTTPStatus == http.StatusUnauthorized || cloudErr.HTTPStatus == http.StatusForbidden) {
			s.clearLocked(StateReauthRequired)
			s.reachability = ReachabilityReachable
		} else {
			s.clearLocked(StateOffline)
			s.reachability = ReachabilityUnreachable
		}
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
	s.reachability = ReachabilityReachable
	return nil
}

func (s *Service) clearLocked(state State) {
	s.state = state
	s.accessToken = ""
	s.accessExpires = time.Time{}
}
