package workflowmcp

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"strings"
	"testing"
)

func TestWorkBuddyStartRequiresNativeEvidenceEvenWithSuppliedDriver(t *testing.T) {
	ctx := context.Background()
	a, b := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	called := false
	Register(server, &Client{HostProvider: "workbuddy", RequireHostBinding: true,
		ResolveDriver: func(_ context.Context, raw json.RawMessage) (string, error) {
			called = true
			var args map[string]any
			if err := json.Unmarshal(raw, &args); err != nil {
				t.Fatal(err)
			}
			if args["idempotency_key"] != "stable-start-key-1" || args["driver_session_id"] != threadUUID {
				t.Fatal(args)
			}
			return "", errors.New("native call is ambiguous")
		},
		BindController: func(context.Context, string, string) error { t.Fatal("binding before identity proof"); return nil },
	})
	ss, err := server.Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "workflow.start", Arguments: map[string]any{
		"workflow_id": "wf", "idempotency_key": "stable-start-key-1", "driver_session_id": threadUUID,
	}})
	if err != nil || !called || !result.IsError {
		t.Fatalf("result=%+v called=%v error=%v", result, called, err)
	}
	if !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "native call is ambiguous") {
		t.Fatal("identity failure hidden")
	}
}
