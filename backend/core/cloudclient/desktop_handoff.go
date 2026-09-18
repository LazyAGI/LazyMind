package cloudclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const desktopClientID = "lazymind-desktop"

type DesktopAuthorizationRequest struct {
	RedirectURI   string
	CallbackURL   string
	State         string
	CodeChallenge string
	Locale        string
}

type DesktopAuthorization struct {
	AuthorizationURL string
	TransactionID    string
}

type DesktopCodeExchange struct {
	RedirectURI   string
	CallbackURL   string
	Code          string
	CodeVerifier  string
	TransactionID string
}

type DesktopTokenPair struct {
	AccessToken     string
	RefreshToken    string
	AccessExpiresAt time.Time
}

func (c *Client) BeginDesktopAuthorization(_ context.Context, request DesktopAuthorizationRequest) (DesktopAuthorization, error) {
	redirectURI := firstNonEmpty(request.RedirectURI, request.CallbackURL)
	if !validDesktopLoopbackCallback(redirectURI) || strings.TrimSpace(request.State) == "" ||
		strings.TrimSpace(request.CodeChallenge) == "" {
		return DesktopAuthorization{}, errors.New("invalid LazyMind Cloud Desktop authorization request")
	}
	locale, ok := normalizeCloudLocale(request.Locale)
	if !ok {
		return DesktopAuthorization{}, errors.New("invalid LazyMind Cloud locale")
	}
	endpoint, err := url.Parse(c.resolve("/" + locale + "/desktop/authorize"))
	if err != nil {
		return DesktopAuthorization{}, err
	}
	query := endpoint.Query()
	query.Set("client_id", desktopClientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("state", request.State)
	query.Set("code_challenge", request.CodeChallenge)
	query.Set("code_challenge_method", "S256")
	endpoint.RawQuery = query.Encode()
	return DesktopAuthorization{AuthorizationURL: endpoint.String()}, nil
}

func (c *Client) ExchangeDesktopCode(ctx context.Context, exchange DesktopCodeExchange) (DesktopTokenPair, error) {
	redirectURI := firstNonEmpty(exchange.RedirectURI, exchange.CallbackURL)
	if !validDesktopLoopbackCallback(redirectURI) || strings.TrimSpace(exchange.Code) == "" || strings.TrimSpace(exchange.CodeVerifier) == "" {
		return DesktopTokenPair{}, errors.New("invalid LazyMind Cloud Desktop code exchange")
	}
	body, err := json.Marshal(map[string]string{
		"grant_type": "authorization_code", "client_id": desktopClientID,
		"code": exchange.Code, "code_verifier": exchange.CodeVerifier, "redirect_uri": redirectURI,
	})
	if err != nil {
		return DesktopTokenPair{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.resolve("/v1/desktop-auth/token"), bytes.NewReader(body))
	if err != nil {
		return DesktopTokenPair{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return DesktopTokenPair{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return DesktopTokenPair{}, decodeCloudError(response)
	}
	var tokens AuthTokens
	if err := decodeStrictJSON(response, &tokens); err != nil {
		return DesktopTokenPair{}, fmt.Errorf("decode LazyMind Cloud Desktop token response: %w", err)
	}
	if strings.TrimSpace(tokens.AccessToken) == "" || strings.TrimSpace(tokens.RefreshToken) == "" || !isSafeCloudID(tokens.SessionID) {
		return DesktopTokenPair{}, errors.New("LazyMind Cloud returned an incomplete Desktop token pair")
	}
	expiresAt, err := AccessTokenExpiresAt(tokens.AccessToken)
	if err != nil {
		return DesktopTokenPair{}, err
	}
	return DesktopTokenPair{AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken, AccessExpiresAt: expiresAt}, nil
}

func (c *Client) RegistrationURL(locale string) (string, error) {
	normalized, ok := normalizeCloudLocale(locale)
	if !ok {
		return "", errors.New("invalid LazyMind Cloud locale")
	}
	return c.resolve("/" + normalized + "/register"), nil
}

func normalizeCloudLocale(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "zh", "zh-cn":
		return "zh", true
	case "en", "en-us":
		return "en", true
	default:
		return "", false
	}
}

func validDesktopLoopbackCallback(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Hostname() != "127.0.0.1" ||
		parsed.Path != "/cloud-auth/callback" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery {
		return false
	}
	host, portValue, err := net.SplitHostPort(parsed.Host)
	if err != nil || host != "127.0.0.1" {
		return false
	}
	port, err := strconv.Atoi(portValue)
	return err == nil && port >= 1024 && port <= 65535
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
