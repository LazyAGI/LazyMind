package common

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// UserID textRequesttext X-User-Id textUser ID。
func UserID(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("X-User-Id"))
}

// UserName textRequesttext X-User-Name textUsertext。
func UserName(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("X-User-Name"))
}

// UserRole returns the trusted role header injected by the auth gateway/local proxy.
func UserRole(r *http.Request) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.Header.Get("X-User-Role"))
}

func RoleIsAdmin(role string) bool {
	normalized := strings.ToLower(strings.TrimSpace(role))
	switch normalized {
	case "admin", "system-admin", "system_admin":
		return true
	default:
		return strings.HasSuffix(normalized, ".admin")
	}
}

func RequestUserIsAdmin(r *http.Request) bool {
	if RoleIsAdmin(UserRole(r)) {
		return true
	}
	if r == nil {
		return false
	}
	role, disabled, ok := lookupRequestUserRole(r)
	return ok && !disabled && RoleIsAdmin(role)
}

func lookupRequestUserRole(r *http.Request) (string, bool, bool) {
	type roleResponse struct {
		UserID   string `json:"user_id"`
		Role     string `json:"role"`
		Disabled bool   `json:"disabled"`
		Data     struct {
			UserID   string `json:"user_id"`
			Role     string `json:"role"`
			Disabled bool   `json:"disabled"`
		} `json:"data"`
	}
	extract := func(resp roleResponse) (string, bool, bool) {
		role := strings.TrimSpace(resp.Role)
		disabled := resp.Disabled
		if role == "" {
			role = strings.TrimSpace(resp.Data.Role)
			disabled = resp.Data.Disabled
		}
		return role, disabled, role != ""
	}
	if authorization := strings.TrimSpace(r.Header.Get("Authorization")); authorization != "" {
		var resp roleResponse
		headers := map[string]string{"Accept": "application/json", "Authorization": authorization}
		if err := ApiPost(requestContext(r), AuthServiceBaseURL()+"/auth/validate", nil, headers, &resp, 3*time.Second); err == nil {
			if role, disabled, ok := extract(resp); ok {
				return role, disabled, true
			}
		}
	}
	userID := UserID(r)
	internalToken := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"))
	if userID == "" || internalToken == "" {
		return "", false, false
	}
	var resp roleResponse
	headers := map[string]string{"Accept": "application/json", "X-LazyMind-Internal-Token": internalToken}
	endpoint := AuthServiceBaseURL() + "/user/" + url.PathEscape(userID) + "/role/internal"
	if err := ApiGet(requestContext(r), endpoint, headers, &resp, 3*time.Second); err != nil {
		return "", false, false
	}
	role, disabled, ok := extract(resp)
	return role, disabled, ok
}
