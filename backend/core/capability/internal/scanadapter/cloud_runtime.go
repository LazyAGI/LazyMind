package scanadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"lazymind/core/capability"
)

func supportedProvider(provider string) bool {
	return provider == "feishu" || provider == "notion" || provider == "googledrive"
}

func ownedAccount(account cloudAccount, call capability.InvocationContext) bool {
	// Auth currently stores globally owner-scoped connections with an empty tenant.
	// A populated tenant must still match; never infer ownership from the internal token.
	return account.OwnerUserID != "" && account.OwnerUserID == call.Principal.UserID &&
		(account.TenantID == "" || account.TenantID == call.Principal.TenantID)
}

func connectionError(code capability.ErrorCode, op, reason, message, provider, action, connectionID string) *capability.Error {
	err := capability.NewError(code, op, message, false, nil)
	err.Reason = reason
	path := "/cloud-documents"
	switch provider {
	case "feishu":
		path += "/feishu"
	case "notion":
		path += "/docs/notion-setup"
	case "googledrive":
		path += "/google-drive"
	}
	if connectionID != "" {
		// Only attach a connection after account ownership has been verified.
		path = "/cloud-documents?" + url.Values{
			"provider": {provider}, "connection_id": {connectionID}, "action": {action},
		}.Encode()
	}
	// Use deployment configuration, never an untrusted Host/Forwarded header.
	if base, parseErr := url.Parse(strings.TrimSpace(os.Getenv("LAZYMIND_PUBLIC_BASE_URL"))); parseErr == nil &&
		(base.Scheme == "https" || base.Scheme == "http") && base.Host != "" && base.User == nil && base.RawQuery == "" && base.Fragment == "" {
		basePath := strings.TrimSuffix(strings.TrimRight(base.Path, "/"), "/api/core")
		path = base.Scheme + "://" + base.Host + basePath + path
	}
	err.Action = &capability.ErrorAction{Type: action, URL: path}
	return err
}

// SetRuntimeEndpoint is called once during bootstrap, before serving requests.
func (a *CloudDocumentReader) SetRuntimeEndpoint(baseURL string) error {
	base, err := parseBaseURL(baseURL)
	if err != nil || a.internalToken == "" {
		return capability.NewError(capability.InvalidArgument, "cloud_document.adapter.new", "runtime URL and internal token are required", false, nil)
	}
	a.runtimeBase = base
	return nil
}

func (a *CloudDocumentReader) connectionToolConfig(ctx context.Context, call capability.InvocationContext, op string, account cloudAccount) (map[string]string, error) {
	query := url.Values{"user_id": {call.Principal.UserID}, "tenant_id": {call.Principal.TenantID}}
	path := "/v1/cloud/connections/" + url.PathEscape(account.ConnectionID) + "/token?" + query.Encode()
	var response struct {
		Data *struct {
			ConnectionID string `json:"connection_id"`
			Provider     string `json:"provider"`
			Status       string `json:"status"`
			AccessToken  string `json:"access_token"`
		} `json:"data"`
	}
	// The existing token endpoint owns refresh/recovery; do not reject an expired
	// connection before giving that endpoint a chance to recover it.
	if err := a.request(ctx, call, op, a.authBase, path, nil, true, &response); err != nil {
		if code, _ := capability.CodeOf(err); code == capability.PermissionDenied || code == capability.NotFound || code == capability.InvalidArgument {
			return nil, connectionError(capability.PermissionDenied, op, "AUTH_REQUIRED", "reauthorize this cloud account in LazyMind", account.Provider, "reauthorize", account.ConnectionID)
		}
		var detail *capability.Error
		if errors.As(err, &detail) && detail.Code == capability.Unavailable {
			detail.Reason = "TOKEN_UNAVAILABLE"
			detail.Action = connectionError(detail.Code, op, "", "", account.Provider, "check_connection", account.ConnectionID).Action
		}
		return nil, err
	}
	if response.Data == nil || response.Data.ConnectionID != account.ConnectionID || response.Data.Provider != account.Provider || response.Data.Status != "ACTIVE" || strings.TrimSpace(response.Data.AccessToken) == "" {
		return nil, capability.NewError(capability.PermissionDenied, op, "invalid connection credential response", false, nil)
	}
	return map[string]string{account.Provider: strings.TrimSpace(response.Data.AccessToken)}, nil
}

func (a *CloudDocumentReader) ReadCloudDocument(ctx context.Context, call capability.InvocationContext, input capability.ReadCloudDocumentInput) (capability.ReadCloudDocumentResult, error) {
	const op = "cloud_document.read"
	account, err := a.account(ctx, call, op, input.SourceID)
	if err != nil {
		return capability.ReadCloudDocumentResult{}, err
	}
	var result capability.ReadCloudDocumentResult
	err = a.runtimeRequest(ctx, call, op, "read", account, map[string]any{
		"locator": input.Locator, "offset": input.Offset, "limit": input.Limit, "expected_version": input.ExpectedVersion,
	}, &result)
	if err != nil {
		return capability.ReadCloudDocumentResult{}, err
	}
	if result.SourceID != account.ConnectionID || result.Provider != account.Provider || result.DocumentID == "" || result.Version == "" || result.ContentFormat != "markdown" {
		return capability.ReadCloudDocumentResult{}, capability.NewError(capability.Unavailable, op, "invalid document identity in runtime response", false, nil)
	}
	return result, nil
}

func (a *CloudDocumentReader) runtimeRequest(ctx context.Context, call capability.InvocationContext, op, operation string, account cloudAccount, body map[string]any, out any) error {
	if a.runtimeBase == nil {
		return capability.NewError(capability.Unsupported, op, "cloud document runtime is not configured", false, nil)
	}
	config, err := a.connectionToolConfig(ctx, call, op, account)
	if err != nil {
		return err
	}
	body["user_id"], body["tenant_id"] = call.Principal.UserID, call.Principal.TenantID
	body["source_id"], body["provider"], body["tool_config"] = account.ConnectionID, account.Provider, config
	err = a.request(ctx, call, op, a.runtimeBase, "/internal/documents:"+operation, body, true, out)
	return withAccessGuidance(err, account)
}

func withAccessGuidance(err error, account cloudAccount) error {
	var detail *capability.Error
	if errors.As(err, &detail) && (detail.Code == capability.PermissionDenied || detail.Code == capability.NotFound) {
		detail.Action = connectionError(detail.Code, detail.Operation, "", "", account.Provider, "check_access", account.ConnectionID).Action
		if detail.Reason == "" {
			detail.Reason = "DOCUMENT_UNAVAILABLE"
		}
	}
	return err
}

func (a *CloudDocumentReader) request(ctx context.Context, call capability.InvocationContext, op string, base *url.URL, path string, body any, internal bool, out any) error {
	if base == nil || internal && a.internalToken == "" {
		return capability.NewError(capability.Unsupported, op, "cloud service is not configured", false, nil)
	}
	method := http.MethodGet
	var payload []byte
	var err error
	if body != nil {
		method = http.MethodPost
		payload, err = json.Marshal(body)
		if err != nil {
			return capability.NewError(capability.Internal, op, "cannot encode cloud request", false, nil)
		}
	}
	timeout := a.timeout
	if strings.HasPrefix(path, "/internal/documents:") {
		timeout = 28 * time.Second // Runtime kills its worker at 25 seconds.
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, endpoint(base, path).String(), bytes.NewReader(payload))
	if err != nil {
		return capability.NewError(capability.Internal, op, "cannot build cloud request", false, nil)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-User-ID", call.Principal.UserID)
	request.Header.Set("X-Tenant-ID", call.Principal.TenantID)
	if internal {
		request.Header.Set("X-LazyMind-Internal-Token", a.internalToken)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		code := capability.Unavailable
		if ctx.Err() != nil {
			code = capability.DeadlineExceeded
		}
		return capability.NewError(code, op, "cloud document request did not complete", true, nil)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil {
		return capability.NewError(capability.Unavailable, op, "cannot read cloud response", true, nil)
	}
	if len(data) > 4<<20 {
		return capability.NewError(capability.ResultTooLarge, op, "cloud response exceeds 4 MiB", false, nil)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return cloudHTTPError(op, response.StatusCode, data)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return capability.NewError(capability.Unavailable, op, "invalid cloud response", true, nil)
	}
	return nil
}

func cloudHTTPError(op string, status int, data []byte) *capability.Error {
	var body struct {
		Detail struct {
			Code string `json:"code"`
		} `json:"detail"`
	}
	_ = json.Unmarshal(data, &body)
	code, retryable := capability.Unavailable, status >= 500 || status == 429
	switch status {
	case 400, 422:
		code = capability.InvalidArgument
	case 401, 403:
		code = capability.PermissionDenied
	case 404:
		code = capability.NotFound
	case 409:
		code = capability.Conflict
	case 413:
		code = capability.ResultTooLarge
	case 504:
		code = capability.DeadlineExceeded
	}
	err := capability.NewError(code, op, "cloud document request failed", retryable, nil)
	// Only known codes cross the boundary; provider messages may contain secrets.
	switch body.Detail.Code {
	case "INVALID_ARGUMENT", "AUTH_REQUIRED", "ACCESS_DENIED", "NOT_FOUND", "CONTENT_TOO_LARGE", "RESOURCE_LIMIT_EXCEEDED", "BUSY", "RATE_LIMITED", "TIMEOUT", "PROVIDER_UNAVAILABLE":
		err.Reason = body.Detail.Code
	case "UNSUPPORTED":
		err.Code, err.Reason, err.Retryable = capability.Unsupported, "UNSUPPORTED", false
	case "VERSION_CHANGED":
		err.Code, err.Reason, err.Message, err.Retryable = capability.Conflict, "VERSION_CHANGED", "document changed; restart reading from the first page", false
		err.Action = &capability.ErrorAction{Type: "restart_read"}
	}
	return err
}
