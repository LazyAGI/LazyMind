package credentials

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"lazymind/agentconnector/internal/localfile"
)

const (
	authPath         = "/api/authservice/auth"
	localSessionPath = "/_local/admin-session"
	credentialFile   = "credentials.json"
	maxAuthBody      = 1 << 20
)

var ErrAuthenticationRequired = errors.New("not logged in to LazyMind; sign in to the local LazyMind page and try again")

func IsAuthenticationRequired(err error) bool {
	if errors.Is(err, ErrAuthenticationRequired) {
		return true
	}
	var responseErr *apiError
	return errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusUnauthorized
}

type Credentials struct {
	ServerURL    string  `json:"server_url"`
	Username     string  `json:"username,omitempty"`
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	ExpiresIn    int64   `json:"expires_in"`
	SavedAt      float64 `json:"saved_at"`
	Role         string  `json:"role,omitempty"`
	TenantID     string  `json:"tenant_id,omitempty"`
}

type Store struct {
	home           string
	serverOverride string
	httpClient     *http.Client
}

type apiError struct {
	StatusCode int
	Message    string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("auth API returned HTTP %d: %s", e.StatusCode, e.Message)
}

func defaultHome() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("LAZYMIND_HOME")); configured != "" {
		return filepath.Abs(configured)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".lazymind"), nil
}

func NewStore(home, server string) (*Store, error) {
	if strings.TrimSpace(home) == "" {
		var err error
		home, err = defaultHome()
		if err != nil {
			return nil, err
		}
	}
	absHome, err := filepath.Abs(home)
	if err != nil {
		return nil, fmt.Errorf("resolve LazyMind home: %w", err)
	}
	return &Store{
		home:           filepath.Clean(absHome),
		serverOverride: normalizeServerURL(server),
		httpClient:     &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (s *Store) path() string { return filepath.Join(s.home, credentialFile) }

func (s *Store) Save(value Credentials) error {
	server := normalizeServerURL(value.ServerURL)
	parsed, err := url.Parse(server)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return errors.New("LazyMind server URL must be an HTTP(S) origin without embedded credentials")
	}
	if strings.TrimSpace(value.AccessToken) == "" || strings.TrimSpace(value.RefreshToken) == "" {
		return errors.New("LazyMind access and refresh tokens are required")
	}
	value.ServerURL = server
	return s.withLock(func() error { return s.saveUnlocked(value) })
}

func (s *Store) Clear() error {
	return s.withLock(func() error {
		if err := os.Remove(s.path()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	})
}

func (s *Store) AccessToken(ctx context.Context) (string, error) {
	var token string
	err := s.withLock(func() error {
		value, err := s.loadUnlocked()
		if errors.Is(err, ErrAuthenticationRequired) {
			value, err = s.bootstrapLocalSessionUnlocked(ctx)
		}
		if err != nil {
			return err
		}
		if tokenExpiredSoon(value, time.Now(), 90*time.Second) {
			value, err = s.refreshOrBootstrapUnlocked(ctx, value)
			if err != nil {
				return err
			}
		}
		token = value.AccessToken
		return nil
	})
	return token, err
}

func (s *Store) bootstrapLocalSessionUnlocked(ctx context.Context) (Credentials, error) {
	return s.bootstrapLocalSessionForServerUnlocked(ctx, "", false)
}

func (s *Store) bootstrapLocalSessionForServerUnlocked(ctx context.Context, preferredServer string, force bool) (Credentials, error) {
	servers := runtimeServerCandidates()
	if preferredServer = normalizeServerURL(preferredServer); preferredServer != "" {
		servers = append([]string{preferredServer}, servers...)
	}
	seen := make(map[string]struct{}, len(servers))
	for _, server := range servers {
		server = normalizeServerURL(server)
		if _, ok := seen[server]; server == "" || ok {
			continue
		}
		seen[server] = struct{}{}
		requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		var session struct {
			Token        string `json:"token"`
			RefreshToken string `json:"refreshToken"`
			Username     string `json:"username"`
			Role         string `json:"role"`
			TenantID     string `json:"tenantId"`
		}
		endpoint := server + localSessionPath
		if force {
			endpoint += "?force=true"
		}
		err := s.requestJSON(requestCtx, http.MethodPost, endpoint, "", map[string]any{}, &session)
		cancel()
		if err != nil || strings.TrimSpace(session.Token) == "" || strings.TrimSpace(session.RefreshToken) == "" {
			continue
		}
		value := Credentials{
			ServerURL:    server,
			Username:     session.Username,
			AccessToken:  session.Token,
			RefreshToken: session.RefreshToken,
			Role:         session.Role,
			TenantID:     session.TenantID,
		}
		if err := s.saveUnlocked(value); err != nil {
			return Credentials{}, fmt.Errorf("save local LazyMind session: %w", err)
		}
		return value, nil
	}
	return Credentials{}, ErrAuthenticationRequired
}

func (s *Store) ForceRefresh(ctx context.Context, rejectedAccessToken string) (string, error) {
	var token string
	err := s.withLock(func() error {
		value, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if rejectedAccessToken != "" && value.AccessToken != rejectedAccessToken {
			token = value.AccessToken
			return nil
		}
		value, err = s.refreshOrBootstrapUnlocked(ctx, value)
		if err != nil {
			return err
		}
		token = value.AccessToken
		return nil
	})
	return token, err
}

func (s *Store) ServerURL(ctx context.Context) (string, error) {
	if s.serverOverride != "" {
		return s.serverOverride, nil
	}
	if value, err := s.loadUnlocked(); err == nil && normalizeServerURL(value.ServerURL) != "" {
		return normalizeServerURL(value.ServerURL), nil
	}
	if configured := normalizeServerURL(os.Getenv("LAZYMIND_SERVER_URL")); configured != "" {
		return configured, nil
	}
	candidates := runtimeServerCandidates()
	for _, candidate := range candidates {
		probeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, candidate+authPath+"/health", nil)
		if err == nil {
			response, requestErr := s.httpClient.Do(request)
			if requestErr == nil {
				_ = response.Body.Close()
				if response.StatusCode >= 200 && response.StatusCode < 300 {
					cancel()
					return candidate, nil
				}
			}
		}
		cancel()
	}
	return "http://localhost:8000", nil
}

func (s *Store) refreshUnlocked(ctx context.Context, current Credentials) (Credentials, error) {
	if strings.TrimSpace(current.RefreshToken) == "" {
		return Credentials{}, ErrAuthenticationRequired
	}
	var response struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Role         string `json:"role"`
		TenantID     string `json:"tenant_id"`
	}
	err := s.requestJSON(ctx, http.MethodPost, normalizeServerURL(current.ServerURL)+authPath+"/refresh", "",
		map[string]string{"refresh_token": current.RefreshToken}, &response)
	if err != nil {
		latest, loadErr := s.loadUnlocked()
		if loadErr == nil && latest.AccessToken != "" && latest.RefreshToken != "" &&
			(latest.AccessToken != current.AccessToken || latest.RefreshToken != current.RefreshToken) {
			return latest, nil
		}
		return Credentials{}, fmt.Errorf("refresh LazyMind session: %w", err)
	}
	if response.AccessToken == "" || response.RefreshToken == "" {
		return Credentials{}, errors.New("refresh response is missing token fields")
	}
	current.AccessToken = response.AccessToken
	current.RefreshToken = response.RefreshToken
	current.ExpiresIn = response.ExpiresIn
	if response.Role != "" {
		current.Role = response.Role
	}
	if response.TenantID != "" {
		current.TenantID = response.TenantID
	}
	if err := s.saveUnlocked(current); err != nil {
		return Credentials{}, fmt.Errorf("save refreshed credentials: %w", err)
	}
	return current, nil
}

func (s *Store) refreshOrBootstrapUnlocked(ctx context.Context, current Credentials) (Credentials, error) {
	refreshed, err := s.refreshUnlocked(ctx, current)
	if err == nil || !localSessionCanRecover(current.ServerURL, err) {
		return refreshed, err
	}
	bootstrapped, bootstrapErr := s.bootstrapLocalSessionForServerUnlocked(ctx, current.ServerURL, true)
	if bootstrapErr != nil {
		return Credentials{}, fmt.Errorf("%w; restore local LazyMind session: %w", err, bootstrapErr)
	}
	return bootstrapped, nil
}

func localSessionCanRecover(serverURL string, err error) bool {
	var responseErr *apiError
	if !errors.As(err, &responseErr) || responseErr.StatusCode != http.StatusUnauthorized {
		return false
	}
	parsed, parseErr := url.Parse(normalizeServerURL(serverURL))
	if parseErr != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Store) loadUnlocked() (Credentials, error) {
	body, err := os.ReadFile(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return Credentials{}, ErrAuthenticationRequired
	}
	if err != nil {
		return Credentials{}, fmt.Errorf("read credentials: %w", err)
	}
	var value Credentials
	if err := json.Unmarshal(body, &value); err != nil {
		return Credentials{}, fmt.Errorf("decode credentials: %w", err)
	}
	value.ServerURL = normalizeServerURL(value.ServerURL)
	if value.ServerURL == "" || value.AccessToken == "" || value.RefreshToken == "" {
		return Credentials{}, ErrAuthenticationRequired
	}
	return value, nil
}

func (s *Store) saveUnlocked(value Credentials) error {
	if err := os.MkdirAll(s.home, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(s.home, 0o700)
	value.ServerURL = normalizeServerURL(value.ServerURL)
	value.SavedAt = float64(time.Now().UnixNano()) / float64(time.Second)
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(s.home, credentialFile+".*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(body, '\n')); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := localfile.Replace(temporaryPath, s.path()); err != nil {
		return err
	}
	return os.Chmod(s.path(), 0o600)
}

func (s *Store) withLock(fn func() error) error {
	if err := os.MkdirAll(s.home, 0o700); err != nil {
		return err
	}
	unlock, err := localfile.Lock(filepath.Join(s.home, credentialFile+".lock"))
	if err != nil {
		return fmt.Errorf("lock credentials: %w", err)
	}
	defer unlock()
	return fn()
}

func (s *Store) requestJSON(ctx context.Context, method, endpoint, accessToken string, requestValue, responseValue any) error {
	body, err := json.Marshal(requestValue)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	if accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}
	response, err := s.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("cannot reach LazyMind at %s: %w", endpoint, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxAuthBody+1))
	if err != nil {
		return err
	}
	if len(responseBody) > maxAuthBody {
		return errors.New("auth response is too large")
	}
	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(responseBody, &envelope)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(envelope.Message)
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		return &apiError{StatusCode: response.StatusCode, Message: message}
	}
	payload := responseBody
	if len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		payload = envelope.Data
	}
	if responseValue == nil || len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, responseValue); err != nil {
		return fmt.Errorf("decode auth response: %w", err)
	}
	return nil
}

func tokenExpiredSoon(value Credentials, now time.Time, skew time.Duration) bool {
	parts := strings.Split(value.AccessToken, ".")
	if len(parts) == 3 {
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err == nil {
			var claims struct {
				ExpiresAt int64 `json:"exp"`
			}
			if json.Unmarshal(payload, &claims) == nil && claims.ExpiresAt > 0 {
				return !now.Add(skew).Before(time.Unix(claims.ExpiresAt, 0))
			}
		}
	}
	if value.ExpiresIn <= 0 || value.SavedAt <= 0 {
		return false
	}
	expiresAt := time.Unix(0, int64(value.SavedAt*float64(time.Second))).Add(time.Duration(value.ExpiresIn) * time.Second)
	return !now.Add(skew).Before(expiresAt)
}

func normalizeServerURL(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

func runtimeServerCandidates() []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, 4)
	appendURL := func(value string) {
		value = normalizeServerURL(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	appendURL(os.Getenv("LAZYMIND_SERVER_URL"))
	for _, path := range runtimeStatePaths() {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var state struct {
			Config struct {
				FrontendPort int `json:"frontendPort"`
				LocalProxy   struct {
					Port int `json:"port"`
				} `json:"localProxy"`
			} `json:"config"`
		}
		if json.Unmarshal(body, &state) != nil {
			continue
		}
		if state.Config.FrontendPort > 0 {
			appendURL("http://127.0.0.1:" + strconv.Itoa(state.Config.FrontendPort))
		}
		if state.Config.LocalProxy.Port > 0 {
			appendURL("http://127.0.0.1:" + strconv.Itoa(state.Config.LocalProxy.Port))
		}
	}
	appendURL("http://127.0.0.1:8090")
	appendURL("http://127.0.0.1:5024")
	appendURL("http://localhost:8000")
	return result
}

func runtimeStatePaths() []string {
	if configured := strings.TrimSpace(os.Getenv("LAZYMIND_RUNTIME_STATE_FILE")); configured != "" {
		return []string{configured}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var root string
	switch runtime.GOOS {
	case "darwin":
		root = filepath.Join(home, "Library", "Application Support", "LazyMind")
	case "windows":
		root = strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if root == "" {
			root = filepath.Join(home, "AppData", "Local")
		}
		root = filepath.Join(root, "LazyMind")
	default:
		root = strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
		if root == "" {
			root = filepath.Join(home, ".local", "share")
		}
		root = filepath.Join(root, "LazyMind")
	}
	return []string{filepath.Join(root, "state", "runtime-state.json")}
}
