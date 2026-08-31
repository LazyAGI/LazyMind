package cloudclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type Account struct {
	ID                      string   `json:"id"`
	Username                string   `json:"username"`
	EmailMasked             string   `json:"email_masked"`
	Roles                   []string `json:"roles"`
	EffectivePermissionKeys []string `json:"effective_permission_keys"`
	Status                  string   `json:"status"`
	RBACVersion             int64    `json:"rbac_version"`
	PolicyRevision          int64    `json:"policy_revision"`
}

func (c *Client) GetCurrentAccount(ctx context.Context, accessToken string) (Account, error) {
	if err := validateBearer(accessToken); err != nil {
		return Account{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.resolve("/v1/account/me"), nil)
	if err != nil {
		return Account{}, err
	}
	setCloudHeaders(request, accessToken)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return Account{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Account{}, decodeCloudError(response)
	}
	var account Account
	if err := decodeStrictJSON(response, &account); err != nil {
		return Account{}, fmt.Errorf("decode LazyMind Cloud account: %w", err)
	}
	if !isSafeCloudID(account.ID) || strings.TrimSpace(account.Username) == "" || len(account.Roles) != 1 ||
		(account.Status != "active" && account.Status != "disabled") || account.RBACVersion < 1 || account.PolicyRevision < 1 {
		return Account{}, errors.New("LazyMind Cloud returned an invalid account")
	}
	return account, nil
}
