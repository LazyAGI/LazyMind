package mcpbridge

import (
	"context"
	"errors"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A session belongs to one account. Endpoint changes replace it at the next call;
// a failed tool call is never replayed because its side effects may have committed.
type liveConnection struct {
	mu       sync.RWMutex
	bridge   *Bridge
	scope    string
	endpoint string
	session  *mcp.ClientSession
}

func (c *liveConnection) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != nil {
		_ = c.session.Close()
		c.session = nil
	}
}
func (c *liveConnection) call(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	for {
		scope, err := c.bridge.api.AccountScope()
		if err != nil {
			return nil, err
		}
		if scope != c.scope {
			return nil, errors.New("LazyMind account changed; reconnect the Agent")
		}
		endpoint, err := c.bridge.Endpoint(ctx)
		if err != nil {
			return nil, err
		}
		c.mu.RLock()
		if c.session != nil && c.endpoint == endpoint {
			session := c.session
			result, err := session.CallTool(ctx, params)
			c.mu.RUnlock()
			if err != nil {
				c.mu.Lock()
				if c.session == session {
					_ = session.Close()
					c.session = nil
				}
				c.mu.Unlock()
			}
			return result, err
		}
		c.mu.RUnlock()
		// Existing calls drain before a connection is replaced. Ordinary calls share
		// the read lock and retain MCP's concurrent execution behavior.
		c.mu.Lock()
		if c.session == nil || c.endpoint != endpoint {
			if c.session != nil {
				_ = c.session.Close()
				c.session = nil
			}
			c.session, _, c.endpoint, err = c.bridge.Connect(ctx)
		}
		c.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
}

func accountGuard(current func() (string, error), expected string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			if method == "tools/call" {
				scope, err := current()
				if err != nil {
					return nil, err
				}
				if scope != expected {
					return nil, errors.New("LazyMind account changed; reconnect the Agent")
				}
			}
			return next(ctx, method, request)
		}
	}
}
