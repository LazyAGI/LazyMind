package providerconnection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HTTPSourceBindingAuthorizer struct {
	BaseURL       string
	InternalToken string
	HTTPClient    *http.Client
}

func (authorizer HTTPSourceBindingAuthorizer) Authorize(ctx context.Context, userID, tenantID, sourceID, bindingID, authConnectionID string) error {
	return authorizer.postAuthorization(ctx, map[string]string{
		"user_id": userID, "tenant_id": tenantID, "source_id": sourceID,
		"binding_id": bindingID, "auth_connection_id": authConnectionID, "context_mode": ContextModeSourceBinding,
	})
}

func (authorizer HTTPSourceBindingAuthorizer) AuthorizePreBindingBrowse(ctx context.Context, userID, tenantID, authConnectionID string) error {
	return authorizer.postAuthorization(ctx, map[string]string{
		"user_id": userID, "tenant_id": tenantID, "auth_connection_id": authConnectionID,
		"context_mode": ContextModePreBindingBrowse, "consumer": "datasource", "required_capability": "datasource.browse",
	})
}

func (authorizer HTTPSourceBindingAuthorizer) postAuthorization(ctx context.Context, payload map[string]string) error {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(authorizer.BaseURL), "/"))
	if err != nil || base.Scheme == "" || base.Host == "" || strings.TrimSpace(authorizer.InternalToken) == "" {
		return errors.New("source binding authorizer is unavailable")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return authorizer.doAuthorization(ctx, base, body)
}

func (authorizer HTTPSourceBindingAuthorizer) doAuthorization(ctx context.Context, base *url.URL, body []byte) error {
	base.Path = strings.TrimRight(base.Path, "/") + "/api/scan/internal/provider-token-context:authorize"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-LazyMind-Internal-Token", strings.TrimSpace(authorizer.InternalToken))
	client := authorizer.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("source binding authorization returned status %d", response.StatusCode)
	}
	return nil
}
