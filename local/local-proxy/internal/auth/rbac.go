package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	headerUserID   = "X-User-Id"
	headerUserName = "X-User-Name"
	headerTenantID = "X-Tenant-Id"
	headerUserRole = "X-User-Role"
)

var identityHeaders = []string{
	headerUserID,
	headerUserName,
	headerTenantID,
	headerUserRole,
}

type IdentityClaims struct {
	UserID   string
	UserName string
	TenantID string
	Role     string
}

type AuthorizationError struct {
	Status  int
	Detail  string
	Message string
}

func (e *AuthorizationError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Detail != "" {
		return e.Detail
	}
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("authorization failed with status %d", e.Status)
}

func (e *AuthorizationError) Body() map[string]string {
	if e == nil {
		return nil
	}
	if e.Detail != "" {
		return map[string]string{"detail": e.Detail}
	}
	if e.Message != "" {
		return map[string]string{"message": e.Message}
	}
	return nil
}

type RBACAdapter struct {
	AuthServiceURL string
	Client         *http.Client
}

func NewRBACAdapter(authServiceURL string, client *http.Client) *RBACAdapter {
	if client == nil {
		client = &http.Client{
			Timeout: 5 * time.Second,
		}
	}
	return &RBACAdapter{
		AuthServiceURL: strings.TrimRight(strings.TrimSpace(authServiceURL), "/"),
		Client:         client,
	}
}

type authorizePayload struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

type authorizeResponseClaims struct {
	UserID   any `json:"user_id"`
	UserName any `json:"username"`
	TenantID any `json:"tenant_id"`
	Role     any `json:"role"`
}

type authorizeResponse struct {
	Data     *authorizeResponseClaims `json:"data"`
	UserID   any                      `json:"user_id"`
	UserName any                      `json:"username"`
	TenantID any                      `json:"tenant_id"`
	Role     any                      `json:"role"`
}

// AuthorizeAndInjectIdentity validates an incoming request with auth-service and injects trusted
// identity headers into the same request on success.
func (a *RBACAdapter) AuthorizeAndInjectIdentity(ctx context.Context, req *http.Request) (IdentityClaims, *AuthorizationError) {
	var claims IdentityClaims

	if req == nil || req.URL == nil {
		return claims, &AuthorizationError{
			Status:  http.StatusBadGateway,
			Message: "Authorization check failed",
		}
	}

	payload := authorizePayload{
		Method: req.Method,
		Path:   req.URL.Path,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return claims, &AuthorizationError{
			Status:  http.StatusBadGateway,
			Message: "Authorization check failed",
		}
	}

	authReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		a.AuthServiceURL+"/api/authservice/auth/authorize",
		bytes.NewReader(body),
	)
	if err != nil {
		return claims, &AuthorizationError{
			Status:  http.StatusBadGateway,
			Message: "Authorization check failed",
		}
	}

	authReq.Header.Set("Content-Type", "application/json")
	if authorization := req.Header.Get("Authorization"); authorization != "" {
		authReq.Header.Set("Authorization", authorization)
	}

	resp, err := a.Client.Do(authReq)
	if err != nil {
		return claims, &AuthorizationError{
			Status:  http.StatusServiceUnavailable,
			Message: "Authorization service unavailable",
		}
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var parsed authorizeResponse
		if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
			return claims, &AuthorizationError{
				Status:  http.StatusBadGateway,
				Message: "Authorization check failed",
			}
		}
		claims = claimsFromResponse(parsed)
		ApplyTrustedIdentityHeaders(req.Header, claims)
		return claims, nil
	case http.StatusUnauthorized:
		return claims, &AuthorizationError{
			Status: http.StatusUnauthorized,
			Detail: "Unauthorized",
		}
	case http.StatusForbidden:
		return claims, &AuthorizationError{
			Status: http.StatusForbidden,
			Detail: "Forbidden",
		}
	default:
		return claims, &AuthorizationError{
			Status:  http.StatusBadGateway,
			Message: "Authorization check failed",
		}
	}
}

func claimsFromResponse(resp authorizeResponse) IdentityClaims {
	source := authorizeResponseClaims{
		UserID:   resp.UserID,
		UserName: resp.UserName,
		TenantID: resp.TenantID,
		Role:     resp.Role,
	}
	if resp.Data != nil {
		source = *resp.Data
	}

	return IdentityClaims{
		UserID:   parseStringClaim(source.UserID),
		UserName: parseStringClaim(source.UserName),
		TenantID: parseStringClaim(source.TenantID),
		Role:     parseStringClaim(source.Role),
	}
}

func parseStringClaim(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return s
}

func StripIdentityHeaders(header http.Header) {
	for _, h := range identityHeaders {
		header.Del(h)
	}
}

func ApplyTrustedIdentityHeaders(header http.Header, claims IdentityClaims) {
	StripIdentityHeaders(header)
	if claims.UserID != "" {
		header.Set(headerUserID, claims.UserID)
	}
	if claims.UserName != "" {
		header.Set(headerUserName, claims.UserName)
	}
	if claims.TenantID != "" {
		header.Set(headerTenantID, claims.TenantID)
	}
	if claims.Role != "" {
		header.Set(headerUserRole, claims.Role)
	}
}
