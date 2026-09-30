package realtime

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// Authorize checks the original HTTP operation, not merely access to the socket.
// Only verified identity headers may be passed to the existing business handlers.
func Authorize(baseURL string) func(*http.Request) int {
	client := &http.Client{Timeout: 5 * time.Second}
	return func(r *http.Request) int {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			return http.StatusUnauthorized
		}
		body, _ := json.Marshal(map[string]string{"method": r.Method, "path": "/api/core" + r.URL.Path})
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
			strings.TrimRight(baseURL, "/")+"/auth/authorize", bytes.NewReader(body))
		if err != nil {
			return http.StatusServiceUnavailable
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", r.Header.Get("Authorization"))
		resp, err := client.Do(req)
		if err != nil {
			return http.StatusServiceUnavailable
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return resp.StatusCode
		}
		if resp.StatusCode != http.StatusOK {
			return http.StatusBadGateway
		}
		type claims struct {
			UserID   string `json:"user_id"`
			Username string `json:"username"`
			TenantID string `json:"tenant_id"`
			Role     string `json:"role"`
		}
		var result struct {
			claims
			Data *claims `json:"data"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result) != nil {
			return http.StatusBadGateway
		}
		identity := result.claims
		if result.Data != nil {
			identity = *result.Data
		}
		if strings.TrimSpace(identity.UserID) == "" {
			return http.StatusUnauthorized
		}
		r.Header.Set("X-User-Id", identity.UserID)
		r.Header.Set("X-User-Name", identity.Username)
		r.Header.Set("X-Tenant-Id", identity.TenantID)
		r.Header.Set("X-User-Role", identity.Role)
		return http.StatusOK
	}
}
