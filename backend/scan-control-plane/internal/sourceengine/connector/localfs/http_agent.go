package localfs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

type HTTPAgentClient struct {
	baseURL    *url.URL
	token      string
	httpClient *http.Client
}

func NewHTTPAgentClient(baseURL string, client *http.Client) (*HTTPAgentClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("agent base url must include scheme and host")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &HTTPAgentClient{baseURL: parsed, token: agentTokenFromEnv(), httpClient: client}, nil
}

func (c *HTTPAgentClient) ValidatePath(ctx context.Context, req ValidatePathRequest) (PathInfo, error) {
	var out PathInfo
	if err := c.doJSON(ctx, "/api/v1/agents/fs/validate", req, &out); err != nil {
		return PathInfo{}, err
	}
	return out, nil
}

func (c *HTTPAgentClient) ListDir(ctx context.Context, req ListDirRequest) (ListDirPage, error) {
	var out ListDirPage
	if err := c.doJSON(ctx, "/api/v1/agents/fs/list", req, &out); err != nil {
		return ListDirPage{}, err
	}
	return out, nil
}

func (c *HTTPAgentClient) ListRoots(ctx context.Context, req ListRootsRequest) ([]PathInfo, error) {
	var out struct {
		Items []PathInfo `json:"items"`
	}
	if err := c.doJSON(ctx, "/api/v1/agents/fs/roots", req, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *HTTPAgentClient) StatPath(ctx context.Context, req StatPathRequest) (PathInfo, error) {
	var out PathInfo
	if err := c.doJSON(ctx, "/api/v1/agents/fs/stat", req, &out); err != nil {
		return PathInfo{}, err
	}
	return out, nil
}

func (c *HTTPAgentClient) ExportFile(ctx context.Context, req ExportFileRequest) (ExportedFile, error) {
	var out ExportedFile
	if err := c.doJSON(ctx, "/api/v1/agents/fs/export", req, &out); err != nil {
		return ExportedFile{}, err
	}
	return out, nil
}

func (c *HTTPAgentClient) doJSON(ctx context.Context, endpoint string, in any, out any) error {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(in); err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(endpoint), &body)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeAgentError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func agentTokenFromEnv() string {
	if token := strings.TrimSpace(os.Getenv("LAZYMIND_SCAN_CONTROL_PLANE_AGENT_TOKEN")); token != "" {
		return token
	}
	return strings.TrimSpace(os.Getenv("LAZYMIND_FILE_WATCHER_AGENT_TOKEN"))
}

func (c *HTTPAgentClient) endpoint(endpoint string) string {
	u := *c.baseURL
	u.Path = path.Join(c.baseURL.Path, endpoint)
	return u.String()
}

func decodeAgentError(resp *http.Response) error {
	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&payload)
	switch strings.ToUpper(payload.Code) {
	case "AGENT_OFFLINE", "AGENT_NOT_AVAILABLE":
		return connector.NewError(ErrorCodeAgentNotAvailable, payload.Message)
	case "PATH_DENIED", "PATH_NOT_ALLOWED", "PERMISSION_DENIED":
		return connector.NewError(connector.ErrorCodePermissionDenied, payload.Message)
	case "PATH_NOT_FOUND", "TARGET_NOT_FOUND":
		return connector.NewError(ErrorCodeTargetNotFound, payload.Message)
	case "OBJECT_NOT_FOUND":
		return connector.NewError(ErrorCodeObjectNotFound, payload.Message)
	case "VERSION_MISMATCH":
		return connector.NewError(connector.ErrorCodeVersionMismatch, payload.Message)
	default:
		if payload.Message == "" {
			payload.Message = resp.Status
		}
		return connector.NewError(connector.ErrorCodeTransient, payload.Message)
	}
}
