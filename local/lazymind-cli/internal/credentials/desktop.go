package credentials

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"time"
)

func desktopFingerprint(value Credentials) string {
	body, _ := json.Marshal([]string{value.ServerURL, value.AccessToken, value.RefreshToken})
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

// DesktopSnapshot is used only by the main process before creating a renderer.
// It does not renew, bootstrap or start notifications without the renderer's
// matching stored session. Missing legacy handoff metadata means no restoration.
func (s *Store) DesktopSnapshot() (Credentials, error) {
	var value Credentials
	err := s.withLock(func() error {
		var err error
		value, err = s.loadUnlocked()
		return err
	})
	if err != nil || len(value.DesktopHandoff) != 64 {
		return Credentials{}, ErrAuthenticationRequired
	}
	return value, nil
}

// RenewDesktop never bootstraps a local administrator or changes the bound user.
// The existing cross-process credential lock serializes refresh-token rotation.
func (s *Store) RenewDesktop(ctx context.Context, expected Credentials, userID string) (Credentials, error) {
	return s.RenewDesktopCandidate(ctx, expected, userID, nil)
}

// A refresh response may arrive while the subsequent identity endpoint is
// temporarily unavailable. The main process retains that candidate in memory;
// retries validate it instead of consuming the already-rotated refresh token.
func (s *Store) RenewDesktopCandidate(ctx context.Context, expected Credentials, userID string, pending *Credentials) (Credentials, error) {
	var result Credentials
	origin, err := url.Parse(expected.ServerURL)
	if err != nil || origin.Scheme != "http" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" ||
		(origin.Hostname() != "127.0.0.1" && origin.Hostname() != "localhost" && origin.Hostname() != "::1") ||
		userID == "" || len(userID) > 256 || expected.AccessToken == "" || expected.RefreshToken == "" ||
		len(expected.AccessToken) > 16384 || len(expected.RefreshToken) > 16384 {
		return result, ErrAuthenticationRequired
	}
	client := *s.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	strict := *s
	strict.httpClient = &client
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err = s.withLock(func() error {
		current, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if current.ServerURL != expected.ServerURL {
			return ErrAuthenticationRequired
		}
		candidate := current
		if candidate.DesktopHandoff == "" {
			candidate.DesktopHandoff = desktopFingerprint(current)
		}
		if current.AccessToken == expected.AccessToken && current.RefreshToken == expected.RefreshToken {
			if pending != nil {
				if pending.ServerURL != current.ServerURL || pending.AccessToken == "" || pending.RefreshToken == "" ||
					len(pending.AccessToken) > 16384 || len(pending.RefreshToken) > 16384 {
					return ErrAuthenticationRequired
				}
				candidate.AccessToken, candidate.RefreshToken = pending.AccessToken, pending.RefreshToken
				candidate.ExpiresIn, candidate.Role, candidate.TenantID = pending.ExpiresIn, pending.Role, pending.TenantID
			} else {
				var response Credentials
				if err := strict.requestJSON(ctx, http.MethodPost, current.ServerURL+authPath+"/refresh", "",
					map[string]string{"refresh_token": current.RefreshToken}, &response); err != nil {
					return err
				}
				if response.AccessToken == "" || response.RefreshToken == "" || len(response.AccessToken) > 16384 || len(response.RefreshToken) > 16384 {
					return ErrAuthenticationRequired
				}
				candidate.AccessToken, candidate.RefreshToken = response.AccessToken, response.RefreshToken
				candidate.ExpiresIn = response.ExpiresIn
				candidate.Role, candidate.TenantID = response.Role, response.TenantID
			}
		}
		result = candidate
		var identity struct {
			UserID string `json:"user_id"`
			Status string `json:"status"`
		}
		if err := strict.requestJSON(ctx, http.MethodGet, candidate.ServerURL+authPath+"/me", candidate.AccessToken, nil, &identity); err != nil {
			return err
		}
		if identity.UserID != userID || identity.Status != "active" {
			return ErrAuthenticationRequired
		}
		if err := s.saveUnlocked(candidate); err != nil {
			return err
		}
		result = candidate
		return nil
	})
	if err != nil {
		var responseErr *apiError
		if IsAuthenticationRequired(err) || (errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusForbidden) {
			return Credentials{}, ErrAuthenticationRequired
		}
		// Only stable codes may cross the desktop IPC boundary.
		return result, errors.New("DESKTOP_SESSION_RENEWAL_UNAVAILABLE")
	}
	return result, nil
}

// SaveDesktop binds a local session to the desktop data directory, whose identity
// survives port allocation changes. Remote accounts retain exact server scope.
// Called only by the desktop's session handoff, never inferred from a URL alone.
func (s *Store) SaveDesktop(value Credentials, runtimeRoot string) error {
	value.LocalRuntimeID = ""
	if isLoopbackOrigin(value.ServerURL) {
		if runtimeRoot == "" {
			paths := runtimeStatePaths()
			if len(paths) > 0 {
				runtimeRoot = filepath.Dir(filepath.Dir(paths[0]))
			}
		}
		if !filepath.IsAbs(runtimeRoot) {
			return errors.New("desktop runtime root must be absolute")
		}
		sum := sha256.Sum256([]byte(filepath.Clean(runtimeRoot)))
		value.LocalRuntimeID = hex.EncodeToString(sum[:])
	}
	return s.Save(value)
}
func isLoopbackOrigin(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "http" && u.User == nil && (u.Path == "" || u.Path == "/") && u.RawQuery == "" && u.Fragment == "" && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback())
}
