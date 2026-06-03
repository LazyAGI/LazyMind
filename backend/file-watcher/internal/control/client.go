package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	internal "github.com/lazymind/file_watcher/internal"
)

// ControlPlaneClient defines the control-plane communication interface.
type ControlPlaneClient interface {
	RegisterAgent(ctx context.Context, req internal.RegisterAgentRequest) error
	ReportHeartbeat(ctx context.Context, req internal.HeartbeatPayload) error
	ReportEvents(ctx context.Context, req internal.ReportEventsRequest) error
	PullCommands(ctx context.Context, req internal.PullCommandsRequest) (internal.PullCommandsResponse, error)
	AckCommand(ctx context.Context, req internal.AckCommandRequest) error
}

type httpClient struct {
	baseURL    string
	agentToken string
	http       *http.Client
	log        *zap.Logger
}

func NewHTTPClient(baseURL, agentToken string, log *zap.Logger) ControlPlaneClient {
	return &httpClient{
		baseURL:    baseURL,
		agentToken: agentToken,
		http:       &http.Client{Timeout: 15 * time.Second},
		log:        log,
	}
}

func (c *httpClient) RegisterAgent(ctx context.Context, req internal.RegisterAgentRequest) error {
	return c.post(ctx, "/api/v1/agents/register", req, nil)
}

func (c *httpClient) ReportHeartbeat(ctx context.Context, req internal.HeartbeatPayload) error {
	return c.post(ctx, "/api/v1/agents/heartbeat", req, nil)
}

func (c *httpClient) ReportEvents(ctx context.Context, req internal.ReportEventsRequest) error {
	return c.post(ctx, "/api/v1/agents/events", req, nil)
}

func (c *httpClient) PullCommands(ctx context.Context, req internal.PullCommandsRequest) (internal.PullCommandsResponse, error) {
	var resp internal.PullCommandsResponse
	if err := c.post(ctx, "/api/v1/agents/pull", req, &resp); err != nil {
		return resp, err
	}
	return resp, nil
}

func (c *httpClient) AckCommand(ctx context.Context, req internal.AckCommandRequest) error {
	return c.post(ctx, "/api/v1/agents/commands/ack", req, nil)
}

func (c *httpClient) post(ctx context.Context, path string, body, out any) error {
	start := time.Now()
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.agentToken)

	resp, err := c.http.Do(req)
	if err != nil {
		c.log.Warn("control plane request failed",
			zap.String("path", path),
			zap.Int("bytes", len(data)),
			zap.Duration("cost", time.Since(start)),
			zap.Error(err),
		)
		return fmt.Errorf("%s: %w", internal.ErrControlPlaneDown, err)
	}
	defer resp.Body.Close()
	c.log.Debug("control plane request finished",
		zap.String("path", path),
		zap.Int("status", resp.StatusCode),
		zap.Int("bytes", len(data)),
		zap.Duration("cost", time.Since(start)),
	)

	if resp.StatusCode >= 400 {
		c.log.Warn("control plane returned error status",
			zap.String("path", path),
			zap.Int("status", resp.StatusCode),
		)
		return fmt.Errorf("control plane returned %d for %s", resp.StatusCode, path)
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
