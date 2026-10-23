package workflowmcp

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"testing"
)

func TestWorkBuddyExecutionToolsHaveNoMCPAppsPresentation(t *testing.T) {
	ctx := context.Background()
	a, b := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	// Presentation is independent; MCP still exposes execution tools.
	Register(server, &Client{HostProvider: "workbuddy"})
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
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range tools.Tools {
		if tool.Name == "workflow.panel.open" || tool.Meta["ui"] != nil {
			t.Fatalf("legacy WorkBuddy presentation retained: %s", tool.Name)
		}
		if tool.Name == "workflow.start" {
			found = true
		}
	}
	if !found {
		t.Fatal("execution tools missing")
	}
}
