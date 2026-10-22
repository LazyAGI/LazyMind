package mcpbridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lazymind/agentconnector/internal/credentials"
)

func TestLiveConnectionMovesToCurrentPortAndRejectsAccountSwitch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	serve := func(label string, calls *atomic.Int32) *httptest.Server {
		s := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
		for _, name := range requiredTools {
			s.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				calls.Add(1)
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: label}}}, nil
			})
		}
		return httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil))
	}
	var firstCalls, secondCalls atomic.Int32
	first := serve("first", &firstCalls)
	defer first.Close()
	second := serve("second", &secondCalls)
	defer second.Close()
	store, _ := credentials.NewStore(t.TempDir(), "")
	root := t.TempDir()
	save := func(server, user string) {
		t.Helper()
		if err := store.SaveDesktop(credentials.Credentials{ServerURL: server, Username: user, AccessToken: "access", RefreshToken: "refresh"}, root); err != nil {
			t.Fatal(err)
		}
	}
	save(first.URL, "alice")
	bridge, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	session, _, endpoint, err := bridge.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope, _ := store.AccountScope()
	connection := &liveConnection{bridge: bridge, scope: scope, session: session, endpoint: endpoint}
	defer connection.close()
	call := func(want string) {
		t.Helper()
		result, err := connection.call(ctx, &mcp.CallToolParams{Name: requiredTools[0], Arguments: map[string]any{}})
		if err != nil {
			t.Fatal(err)
		}
		if result.Content[0].(*mcp.TextContent).Text != want {
			t.Fatal("used stale MCP session")
		}
	}
	call("first")
	save(second.URL, "alice")
	call("second")
	if firstCalls.Load() != 1 || secondCalls.Load() != 1 {
		t.Fatal("unexpected duplicate call")
	}
	save(second.URL, "bob")
	if _, err := connection.call(ctx, &mcp.CallToolParams{Name: requiredTools[0]}); err == nil || !strings.Contains(err.Error(), "account changed") {
		t.Fatalf("account switch allowed: %v", err)
	}
	if secondCalls.Load() != 1 {
		t.Fatal("called tool under another account")
	}
}

func TestLiveConnectionRetainsConcurrentToolCalls(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	server := mcp.NewServer(&mcp.Implementation{Name: "concurrency", Version: "1"}, nil)
	for _, name := range requiredTools {
		server.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(callCtx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if calls.Add(1) == 1 {
				close(started)
				select {
				case <-release:
				case <-callCtx.Done():
					return nil, callCtx.Err()
				}
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, nil
		})
	}
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()
	store, _ := credentials.NewStore(t.TempDir(), "")
	if err := store.Save(credentials.Credentials{ServerURL: httpServer.URL, AccessToken: "access", RefreshToken: "refresh"}); err != nil {
		t.Fatal(err)
	}
	bridge, _ := New(store)
	session, _, endpoint, err := bridge.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope, _ := store.AccountScope()
	connection := &liveConnection{bridge: bridge, scope: scope, session: session, endpoint: endpoint}
	defer connection.close()
	defer close(release)
	first := make(chan error, 1)
	go func() { _, err := connection.call(ctx, &mcp.CallToolParams{Name: requiredTools[0]}); first <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	second, cancelSecond := context.WithTimeout(ctx, time.Second)
	defer cancelSecond()
	if _, err := connection.call(second, &mcp.CallToolParams{Name: requiredTools[1]}); err != nil {
		t.Fatalf("second tool was blocked by first tool: %v", err)
	}
}
