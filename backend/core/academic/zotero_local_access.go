package academic

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"lazymind/core/common"
	"lazymind/core/store"
	"lazymind/core/systemdeps"
)

// The native runtime manager explicitly configures both local mode and the
// bootstrap account used by its local proxy's auto-login. Shared deployments
// must never turn destination-dataset permissions into host-library access.
// Verify the owner using only the Bearer token, not forwarded identity headers.
func authorizeLocalZoteroSource(r *http.Request, source string) error {
	if source != "local" {
		return nil
	}
	forbidden := func() error {
		return zoteroFailure(http.StatusForbidden, "ZOTERO_LOCAL_ACCESS_FORBIDDEN", "Local Zotero access requires the authenticated owner of a local desktop runtime")
	}
	unavailable := func() error {
		return zoteroFailure(http.StatusServiceUnavailable, "ZOTERO_LOCAL_AUTH_UNAVAILABLE", "Local Zotero owner authentication is unavailable")
	}
	owner := strings.TrimSpace(os.Getenv("LAZYMIND_BOOTSTRAP_ADMIN_USERNAME"))
	if !systemdeps.IsLocalRuntime() || owner == "" {
		return forbidden()
	}
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	parts := strings.Fields(authorization)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(store.UserID(r)) == "" {
		return forbidden()
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, common.AuthServiceBaseURL()+"/auth/me", nil)
	if err != nil {
		return unavailable()
	}
	req.Header.Set("Authorization", "Bearer "+parts[1])
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	response, err := client.Do(req)
	if err != nil {
		return unavailable()
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return forbidden()
	}
	if response.StatusCode != http.StatusOK {
		return unavailable()
	}
	type identity struct {
		UserID   string `json:"user_id"`
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	var payload struct {
		identity
		Code int       `json:"code"`
		Data *identity `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&payload); err != nil || (payload.Code != 0 && payload.Code != 200) {
		return unavailable()
	}
	verified := payload.identity
	if payload.Data != nil {
		verified = *payload.Data
	}
	if strings.TrimSpace(verified.UserID) == "" || strings.TrimSpace(verified.Username) == "" || strings.TrimSpace(verified.Role) == "" {
		return unavailable()
	}
	if strings.TrimSpace(verified.UserID) != strings.TrimSpace(store.UserID(r)) || verified.Username != owner || verified.Role != "system-admin" {
		return forbidden()
	}
	return nil
}
