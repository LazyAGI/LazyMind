package coreapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"lazymind/agentconnector/internal/credentials"
)

const maxRequestBytes = 32 << 20
const maxResponseBytes = 64 << 20

type Client struct {
	credentials *credentials.Store
	http        *http.Client
}

type Error struct {
	StatusCode int
	Code       string
	Message    string
	Retryable  bool
}

func (e *Error) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

func New(store *credentials.Store) (*Client, error) {
	if store == nil {
		return nil, errors.New("credential store is required")
	}
	return &Client{credentials: store, http: &http.Client{Transport: &authenticatedTransport{
		base: http.DefaultTransport, credentials: store,
	}}}, nil
}

func (c *Client) HTTPClient() *http.Client { return c.http }

func (c *Client) ServerURL(ctx context.Context) (string, error) {
	server, err := c.credentials.ServerURL(ctx)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(server, "/"), nil
}

func (c *Client) MCPURL(ctx context.Context) (string, error) {
	if configured := strings.TrimSpace(os.Getenv("LAZYMIND_MCP_URL")); configured != "" {
		return strings.TrimRight(configured, "/"), nil
	}
	server, err := c.ServerURL(ctx)
	if err != nil {
		return "", err
	}
	return server + "/api/core/mcp/capabilities/v1", nil
}

func (c *Client) DoJSON(ctx context.Context, method, path string, input, output any) error {
	server, err := c.ServerURL(ctx)
	if err != nil {
		return err
	}
	var body []byte
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode LazyMind request: %w", err)
		}
		if len(body) > maxRequestBytes {
			return errors.New("LazyMind request exceeds 32 MiB")
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, server+"/api/core"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Workflow-Contract-Version", "workflow.v1")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(responseBody) > maxResponseBytes {
		return errors.New("LazyMind response exceeds 64 MiB")
	}
	payload, apiErr := unwrap(response.StatusCode, responseBody)
	if apiErr != nil {
		return apiErr
	}
	if output == nil || len(payload) == 0 || string(payload) == "null" {
		return nil
	}
	if err := json.Unmarshal(payload, output); err != nil {
		return fmt.Errorf("decode LazyMind response: %w", err)
	}
	return nil
}

func unwrap(status int, body []byte) (json.RawMessage, error) {
	var value struct {
		OK     *bool           `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
		Code    *int            `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, &Error{StatusCode: status, Message: strings.TrimSpace(string(body))}
	}
	if value.OK != nil {
		if !*value.OK || status < 200 || status >= 300 {
			if value.Error != nil {
				return nil, &Error{StatusCode: status, Code: value.Error.Code, Message: value.Error.Message, Retryable: value.Error.Retryable}
			}
			return nil, &Error{StatusCode: status, Message: http.StatusText(status)}
		}
		return value.Result, nil
	}
	if value.Code != nil {
		if *value.Code != 0 || status < 200 || status >= 300 {
			return nil, &Error{StatusCode: status, Code: fmt.Sprintf("CORE_%d", *value.Code), Message: value.Message}
		}
		return value.Data, nil
	}
	if status < 200 || status >= 300 {
		return nil, &Error{StatusCode: status, Message: http.StatusText(status)}
	}
	return append(json.RawMessage(nil), body...), nil
}

type authenticatedTransport struct {
	base        http.RoundTripper
	credentials *credentials.Store
}

func (t *authenticatedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	body, err := readRequestBody(request)
	if err != nil {
		return nil, err
	}
	token, err := t.credentials.AccessToken(request.Context())
	if err != nil {
		return nil, err
	}
	response, err := t.base.RoundTrip(cloneRequest(request, body, token))
	if err != nil || response.StatusCode != http.StatusUnauthorized {
		return response, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	_ = response.Body.Close()
	refreshed, err := t.credentials.ForceRefresh(request.Context(), token)
	if err != nil {
		return nil, err
	}
	return t.base.RoundTrip(cloneRequest(request, body, refreshed))
}

func readRequestBody(request *http.Request) ([]byte, error) {
	if request.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maxRequestBytes+1))
	_ = request.Body.Close()
	if err != nil {
		return nil, err
	}
	if len(body) > maxRequestBytes {
		return nil, errors.New("LazyMind request exceeds 32 MiB")
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func cloneRequest(request *http.Request, body []byte, token string) *http.Request {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+token)
	if externalRef := strings.TrimSpace(os.Getenv("LAZYMIND_EXTERNAL_REF")); externalRef != "" {
		clone.Header.Set("X-LazyMind-External-Ref", externalRef)
	}
	if lease := strings.TrimSpace(os.Getenv("LAZYMIND_EXTERNAL_LEASE")); lease != "" {
		clone.Header.Set("X-LazyMind-External-Lease", lease)
	}
	if hostID := strings.TrimSpace(os.Getenv("LAZYMIND_EXTERNAL_HOST")); hostID != "" {
		clone.Header.Set("X-LazyMind-External-Host", hostID)
	}
	if conversationID := strings.TrimSpace(os.Getenv("LAZYMIND_CONVERSATION_ID")); conversationID != "" {
		clone.Header.Set("X-LazyMind-Conversation-Id", conversationID)
	}
	if invocation, ok := InvocationFromContext(request.Context()); ok {
		clone.Header.Set("X-LazyMind-Invocation-Id", invocation.ID)
		clone.Header.Set("X-LazyMind-Invocation-Client", invocation.ClientName)
		clone.Header.Set("X-LazyMind-Connector-Instance-Id", invocation.ConnectorInstanceID)
	}
	if body != nil {
		clone.Body = io.NopCloser(bytes.NewReader(body))
		clone.ContentLength = int64(len(body))
		clone.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	}
	return clone
}
