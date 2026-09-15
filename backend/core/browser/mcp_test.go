package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (t bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
}

func TestMCPPublishesBrowserToolsForSignedUser(t *testing.T) {
	hub, err := NewHub()
	if err != nil {
		t.Fatal(err)
	}
	token, err := hub.ToolToken("user-1")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewMCPHandler(hub))
	defer server.Close()
	httpClient := &http.Client{Transport: bearerTransport{base: http.DefaultTransport, token: token}}
	client := mcp.NewClient(&mcp.Implementation{Name: "browser-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: server.URL, HTTPClient: httpClient, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	want := append([]string(nil), ToolNames...)
	sort.Strings(want)
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("tools = %v, want %v", names, want)
	}
}

func TestMCPRejectsMissingBearerToken(t *testing.T) {
	hub, _ := NewHub()
	server := httptest.NewServer(NewMCPHandler(hub))
	defer server.Close()
	request, _ := http.NewRequest(http.MethodPost, server.URL, strings.NewReader("{}"))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.StatusCode)
	}
}

func TestAnnotateBrowserPageStateMarksVisibleFeishuEditMode(t *testing.T) {
	result := map[string]any{
		"url": "https://sensetime.feishu.cn/wiki/Ua4Fw8ShmiJcRQk1GyVcRTnVngg",
		"elements": []any{
			map[string]any{"role": "textbox", "state": []any{"readonly"}},
			map[string]any{"role": "button", "name": "编辑"},
		},
	}

	annotateBrowserPageState(result)

	state, ok := result["page_state"].(map[string]any)
	if !ok || state["editor_mode"] != "editable" || state["editable"] != true {
		t.Fatalf("page_state = %#v", result["page_state"])
	}
	instruction, _ := state["instruction"].(string)
	if !strings.Contains(instruction, "不要点击") || !strings.Contains(instruction, "readonly") {
		t.Fatalf("instruction = %q", instruction)
	}
}

func TestAnnotateBrowserPageStateDoesNotGuessOtherPages(t *testing.T) {
	tests := []map[string]any{
		{
			"url":      "https://example.com/wiki/document",
			"elements": []any{map[string]any{"role": "button", "name": "编辑"}},
		},
		{
			"url":      "https://example.feishu.cn/wiki/document",
			"elements": []any{map[string]any{"role": "button", "name": "编辑历史"}},
		},
	}
	for _, result := range tests {
		annotateBrowserPageState(result)
		if _, exists := result["page_state"]; exists {
			t.Fatalf("unexpected page_state for %#v", result)
		}
	}
}
