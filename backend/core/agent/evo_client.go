package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lazymind/core/common"
)

const evoClientTimeout = 30 * time.Second

type evoClient struct {
	baseURL string
	headers map[string]string
	client  *http.Client
}

type evoThread struct {
	ThreadID    string `json:"thread_id"`
	ID          string `json:"id,omitempty"`
	Status      string `json:"status"`
	CurrentStep string `json:"current_step,omitempty"`
	LastError   any    `json:"last_error,omitempty"`
}

type evoGate struct {
	Step             string `json:"step"`
	ArtifactID       string `json:"artifact_id"`
	Versions         []int  `json:"versions"`
	EffectiveVersion *int   `json:"effective_version"`
	LatestVersion    *int   `json:"latest_version"`
}

type evoGateList struct {
	ThreadID string    `json:"thread_id"`
	Gates    []evoGate `json:"gates"`
}

type evoGateContent struct {
	ThreadID string `json:"thread_id"`
	Step     string `json:"step"`
	Version  int    `json:"version"`
	Content  any    `json:"content"`
}

func newEvoClient(headers map[string]string) evoClient {
	copied := make(map[string]string, len(headers))
	for key, value := range headers {
		if strings.TrimSpace(value) != "" {
			copied[key] = value
		}
	}
	return evoClient{
		baseURL: common.EvoServiceEndpoint(),
		headers: copied,
		client:  &http.Client{Timeout: evoClientTimeout},
	}
}

func (c evoClient) CreateThread(ctx context.Context, payload map[string]any, out any) error {
	return c.doJSON(ctx, http.MethodPost, "/threads", nil, payload, out)
}

func (c evoClient) GetThread(ctx context.Context, threadID string) (*evoThread, error) {
	var thread evoThread
	if err := c.doJSON(ctx, http.MethodGet, "/threads/"+url.PathEscape(threadID), nil, nil, &thread); err != nil {
		return nil, err
	}
	if thread.ThreadID == "" {
		thread.ThreadID = thread.ID
	}
	return &thread, nil
}

func (c evoClient) PostCommand(ctx context.Context, threadID, action string, payload map[string]any) (*upstreamProxyResponse, int, error) {
	targetPath := "/threads/" + url.PathEscape(threadID) + "/" + strings.Trim(strings.TrimSpace(action), "/")
	body, contentType, err := c.doRaw(ctx, http.MethodPost, targetPath, nil, payload)
	if err != nil {
		return nil, evoProxyStatusCode(err), err
	}
	return rawProxyResponse(body, contentType), http.StatusOK, nil
}

func (c evoClient) DeleteThread(ctx context.Context, threadID string, out any) error {
	return c.doJSON(ctx, http.MethodDelete, "/threads/"+url.PathEscape(threadID), nil, nil, out)
}

func (c evoClient) EventsStreamURL(threadID, stepID string) string {
	query := url.Values{}
	if strings.TrimSpace(stepID) != "" {
		query.Set("step_id", strings.TrimSpace(stepID))
	}
	return c.url("/threads/"+url.PathEscape(threadID)+"/events:stream", query)
}

func (c evoClient) MessagesURL(threadID string) string {
	return c.url("/threads/"+url.PathEscape(threadID)+"/messages", nil)
}

func (c evoClient) ListGates(ctx context.Context, threadID string) (*evoGateList, error) {
	var result evoGateList
	if err := c.doJSON(ctx, http.MethodGet, "/threads/"+url.PathEscape(threadID)+"/gates", nil, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c evoClient) GetGateContent(ctx context.Context, threadID, step string, version int) (*evoGateContent, error) {
	targetPath := fmt.Sprintf(
		"/threads/%s/gates/%s/versions/%d",
		url.PathEscape(threadID),
		url.PathEscape(step),
		version,
	)
	var result evoGateContent
	if err := c.doJSON(ctx, http.MethodGet, targetPath, nil, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c evoClient) doJSON(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	raw, _, err := c.doRaw(ctx, method, path, query, body)
	if err != nil {
		return err
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("unmarshal evo response: %w", err)
	}
	return nil
}

func (c evoClient) doRaw(ctx context.Context, method, path string, query url.Values, body any) ([]byte, string, error) {
	var reqBody io.Reader = http.NoBody
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, "", fmt.Errorf("marshal evo request: %w", err)
		}
		reqBody = bytes.NewReader(bodyBytes)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path, query), reqBody)
	if err != nil {
		return nil, "", err
	}
	for key, value := range c.headers {
		req.Header.Set(key, value)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", &common.HTTPError{
			StatusCode: resp.StatusCode,
			Message:    strings.TrimSpace(string(respBytes)),
		}
	}
	return respBytes, resp.Header.Get("Content-Type"), nil
}

func (c evoClient) url(path string, query url.Values) string {
	result := common.JoinURL(c.baseURL, path)
	if len(query) > 0 {
		result += "?" + query.Encode()
	}
	return result
}

func rawProxyResponse(bodyBytes []byte, contentType string) *upstreamProxyResponse {
	if strings.Contains(contentType, "application/json") {
		var payload any
		if err := json.Unmarshal(bodyBytes, &payload); err == nil {
			return &upstreamProxyResponse{Body: payload, ContentType: "application/json"}
		}
	}
	return &upstreamProxyResponse{Body: string(bodyBytes), ContentType: contentType}
}
