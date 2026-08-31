package historyinjection

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"lazymind/core/common"

	"gorm.io/gorm"
)

// ResolveImportedOwner returns the persisted owner when every discovered
// sample conversation has already been imported. Startup can then perform the
// idempotent repair pass without logging in with bootstrap credentials, which
// may no longer match after the administrator changes their password.
func ResolveImportedOwner(ctx context.Context, db *gorm.DB, sources []BundleSource) (TargetOwner, bool, error) {
	if db == nil || len(sources) == 0 {
		return TargetOwner{}, false, nil
	}
	var owner TargetOwner
	for _, source := range sources {
		var persisted struct {
			ID       string `gorm:"column:create_user_id"`
			Username string `gorm:"column:create_user_name"`
		}
		result := db.WithContext(ctx).Raw(
			"SELECT create_user_id, create_user_name FROM conversations WHERE id = ?",
			source.Manifest.ConversationID,
		).Scan(&persisted)
		if result.Error != nil {
			return TargetOwner{}, false, result.Error
		}
		if result.RowsAffected != 1 || strings.TrimSpace(persisted.ID) == "" {
			return TargetOwner{}, false, nil
		}
		if owner.ID == "" {
			owner = TargetOwner{ID: persisted.ID, Username: persisted.Username}
			continue
		}
		if owner.ID != persisted.ID {
			return TargetOwner{}, false, nil
		}
		if owner.Username == "" {
			owner.Username = persisted.Username
		}
	}
	return owner, true, nil
}

func ResolveBootstrapOwner(ctx context.Context, timeout time.Duration) (TargetOwner, error) {
	username := strings.TrimSpace(os.Getenv("LAZYMIND_BOOTSTRAP_ADMIN_USERNAME"))
	if username == "" {
		username = "admin"
	}
	password := os.Getenv("LAZYMIND_BOOTSTRAP_ADMIN_PASSWORD")
	if password == "" {
		password = "admin"
	}
	baseURL := strings.TrimRight(common.AuthServiceBaseURL(), "/")
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := &http.Client{Timeout: 10 * time.Second}
	var lastErr error
	for {
		owner, err := resolveOwnerOnce(deadlineCtx, client, baseURL, username, password)
		if err == nil {
			return owner, nil
		}
		lastErr = err
		select {
		case <-deadlineCtx.Done():
			return TargetOwner{}, fmt.Errorf("resolve bootstrap admin for history injection: %w (last error: %v)", deadlineCtx.Err(), lastErr)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func resolveOwnerOnce(ctx context.Context, client *http.Client, baseURL, username, password string) (TargetOwner, error) {
	loginBody, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/auth/login", bytes.NewReader(loginBody))
	if err != nil {
		return TargetOwner{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return TargetOwner{}, err
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	if readErr != nil {
		return TargetOwner{}, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return TargetOwner{}, fmt.Errorf("auth login returned HTTP %d", resp.StatusCode)
	}
	var loginPayload struct {
		AccessToken string `json:"access_token"`
	}
	var login struct {
		AccessToken string `json:"access_token"`
		Data        struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &login) != nil {
		return TargetOwner{}, fmt.Errorf("auth login response has no access token")
	}
	loginPayload.AccessToken = login.AccessToken
	if strings.TrimSpace(loginPayload.AccessToken) == "" {
		loginPayload.AccessToken = login.Data.AccessToken
	}
	if strings.TrimSpace(loginPayload.AccessToken) == "" {
		return TargetOwner{}, fmt.Errorf("auth login response has no access token")
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/auth/me", nil)
	if err != nil {
		return TargetOwner{}, err
	}
	req.Header.Set("Authorization", "Bearer "+loginPayload.AccessToken)
	resp, err = client.Do(req)
	if err != nil {
		return TargetOwner{}, err
	}
	body, readErr = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	if readErr != nil {
		return TargetOwner{}, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return TargetOwner{}, fmt.Errorf("auth me returned HTTP %d", resp.StatusCode)
	}
	type ownerPayload struct {
		UserID   string `json:"user_id"`
		Username string `json:"username"`
	}
	var me struct {
		ownerPayload
		Data ownerPayload `json:"data"`
	}
	if err := json.Unmarshal(body, &me); err != nil {
		return TargetOwner{}, fmt.Errorf("auth me response has no user_id")
	}
	payload := me.ownerPayload
	if strings.TrimSpace(payload.UserID) == "" {
		payload = me.Data
	}
	if strings.TrimSpace(payload.UserID) == "" {
		return TargetOwner{}, fmt.Errorf("auth me response has no user_id")
	}
	if strings.TrimSpace(payload.Username) == "" {
		payload.Username = username
	}
	return TargetOwner{ID: payload.UserID, Username: payload.Username}, nil
}
