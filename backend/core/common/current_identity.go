package common

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"time"
)

// CurrentIdentity is resolved from the current, enabled Auth account rather than
// execution metadata or caller-supplied tenant claims.
type CurrentIdentity struct {
	Subject     string   `json:"sub"`
	TenantID    string   `json:"tenant_id"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
}

func GetCurrentIdentity(ctx context.Context, userID string) (CurrentIdentity, error) {
	userID = strings.TrimSpace(userID)
	token := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"))
	if userID == "" || token == "" {
		return CurrentIdentity{}, errors.New("account identity unavailable or disabled")
	}
	var envelope struct {
		CurrentIdentity
		Data *CurrentIdentity `json:"data"`
	}
	endpoint := AuthServiceBaseURL() + "/user/" + url.PathEscape(userID) + "/identity/internal"
	if err := ApiGet(ctx, endpoint, map[string]string{"X-LazyMind-Internal-Token": token}, &envelope, 5*time.Second); err != nil {
		return CurrentIdentity{}, errors.New("account identity unavailable or disabled")
	}
	identity := envelope.CurrentIdentity
	if envelope.Data != nil {
		identity = *envelope.Data
	}
	if identity.Subject != userID {
		return CurrentIdentity{}, errors.New("account identity mismatch")
	}
	return identity, nil
}
