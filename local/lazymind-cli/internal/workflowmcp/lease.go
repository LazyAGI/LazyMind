package workflowmcp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"lazymind/agentconnector/internal/coreapi"
)

type stepLeaseKey struct {
	sessionID   string
	executionID string
}

type stepLease struct {
	handle  string
	expires time.Time
	cancel  context.CancelFunc
}

type stepLeaseRenewal struct {
	ctx   context.Context
	scope string
	mu    sync.Mutex
	steps map[stepLeaseKey]*stepLease
}

// EnableStepLeaseRenewal binds renewal to the connector's lifetime, which
// spans many individual MCP calls. Call before serving requests.
func (c *Client) EnableStepLeaseRenewal(ctx context.Context, accountScope string) {
	c.leaseRenewal = &stepLeaseRenewal{ctx: ctx, scope: accountScope, steps: make(map[stepLeaseKey]*stepLease)}
}

func (c *Client) trackStepLease(sessionID string, execution Execution) {
	renewal := c.leaseRenewal
	if renewal == nil || execution.ExecutionHandle == "" || execution.ExecutionID == "" {
		return
	}
	expires, err := time.Parse(time.RFC3339Nano, execution.LeaseExpires)
	if err != nil || !time.Now().Before(expires) {
		return
	}
	key := stepLeaseKey{sessionID: sessionID, executionID: execution.ExecutionID}
	renewal.mu.Lock()
	defer renewal.mu.Unlock()
	if previous := renewal.steps[key]; previous != nil {
		if previous.handle == execution.ExecutionHandle || previous.expires.After(expires) {
			return
		}
		previous.cancel()
	}
	ctx, cancel := context.WithCancel(renewal.ctx)
	lease := &stepLease{handle: execution.ExecutionHandle, expires: expires, cancel: cancel}
	renewal.steps[key] = lease
	go func() {
		defer cancel()
		defer func() {
			renewal.mu.Lock()
			defer renewal.mu.Unlock()
			if renewal.steps[key] == lease {
				delete(renewal.steps, key)
			}
		}()
		c.renewStepLease(ctx, renewal.scope, key, lease.handle, expires)
	}()
}

func (c *Client) stopStepLease(sessionID, executionID, handle string) {
	renewal := c.leaseRenewal
	if renewal == nil {
		return
	}
	key := stepLeaseKey{sessionID: sessionID, executionID: executionID}
	renewal.mu.Lock()
	defer renewal.mu.Unlock()
	if lease := renewal.steps[key]; lease != nil && lease.handle == handle {
		lease.cancel()
		delete(renewal.steps, key)
	}
}

func (c *Client) renewStepLease(ctx context.Context, accountScope string, key stepLeaseKey, handle string, expires time.Time) {
	path := "/workflow-sessions/" + url.PathEscape(key.sessionID) + "/hosted-attempts/" + url.PathEscape(key.executionID) + ":heartbeat"
	payload := map[string]string{"execution_handle": handle}
	delay := min(time.Until(expires)/3, 5*time.Minute)
	for delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if !time.Now().Before(expires) {
			return
		}
		scope, err := c.api.AccountScope()
		if err != nil || scope != accountScope {
			return
		}
		var result struct {
			Expires time.Time `json:"lease_expires_at"`
		}
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = c.api.DoJSON(callCtx, http.MethodPost, path, payload, &result)
		cancel()
		if err == nil {
			expires = result.Expires
			delay = min(time.Until(expires)/3, 5*time.Minute)
			continue
		}
		var apiErr *coreapi.Error
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 && apiErr.StatusCode != http.StatusTooManyRequests {
			return
		}
		delay = min(15*time.Second, time.Until(expires))
	}
}
