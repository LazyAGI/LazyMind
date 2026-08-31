package mcpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lazymind/core/capability"
)

func TestRealLazyMindMCP(t *testing.T) {
	if os.Getenv("LAZYMIND_REAL_MCP_E2E") != "1" {
		t.Skip("set LAZYMIND_REAL_MCP_E2E=1 to run against a real LazyMind stack")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("LAZYMIND_REAL_MCP_BASE_URL")), "/")
	username := strings.TrimSpace(os.Getenv("LAZYMIND_REAL_MCP_USERNAME"))
	password := os.Getenv("LAZYMIND_REAL_MCP_PASSWORD")
	if baseURL == "" || username == "" || password == "" {
		t.Fatal("LAZYMIND_REAL_MCP_BASE_URL, LAZYMIND_REAL_MCP_USERNAME and LAZYMIND_REAL_MCP_PASSWORD are required")
	}

	httpClient := &http.Client{Timeout: 90 * time.Second}
	token := realLogin(t, httpClient, baseURL, username, password)
	skillID, marker := createRealSkillFixture(t, httpClient, baseURL, token)
	t.Cleanup(func() { deleteRealSkillFixture(t, httpClient, baseURL, token, skillID) })

	client := mcp.NewClient(&mcp.Implementation{Name: "lazymind-real-e2e", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: baseURL + "/api/core/mcp/capabilities/v1",
		HTTPClient: &http.Client{
			Timeout:   90 * time.Second,
			Transport: bearerTransport{base: http.DefaultTransport, token: token},
		},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect to real MCP endpoint: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list real MCP tools: %v", err)
	}
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
			t.Fatalf("tool %q does not expose the required non-destructive annotations", tool.Name)
		}
		if !tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint {
			t.Fatalf("tool %q annotations = %#v, want read-only and idempotent", tool.Name, tool.Annotations)
		}
	}
	sort.Strings(names)
	if got, want := strings.Join(names, ","), "cloud_document.get,cloud_document.list,cloud_document.search,knowledge.document.get,knowledge.document.list,knowledge.list,knowledge.search,skill.get,skill.list"; got != want {
		t.Fatalf("real tool names = %q, want %q", got, want)
	}

	skills := callRealTool[capability.ListSkillsResult](t, session, "skill.list", map[string]any{
		"page": map[string]any{"page_size": 100},
	})
	if !containsSkill(skills.Items, skillID) {
		t.Fatalf("skill.list did not return real fixture %q", skillID)
	}
	skill := callRealTool[capability.GetSkillResult](t, session, "skill.get", map[string]any{
		"skill_id": skillID, "include_content": true,
	})
	if skill.Skill.ID != skillID || skill.Content == nil || !strings.Contains(skill.Content.Text, marker) {
		t.Fatalf("skill.get did not return committed fixture content: %#v", skill)
	}

	knowledge := callRealTool[capability.ListKnowledgeResult](t, session, "knowledge.list", map[string]any{
		"page": map[string]any{"page_size": 100},
	})
	if len(knowledge.Items) == 0 {
		t.Fatal("knowledge.list returned no real accessible knowledge bases")
	}
	query := strings.TrimSpace(os.Getenv("LAZYMIND_REAL_MCP_QUERY"))
	if query == "" {
		query = "利時"
	}
	var selectedKnowledge capability.KnowledgeSummary
	var selectedHit capability.KnowledgeSearchHit
	for _, item := range knowledge.Items {
		documents := callRealTool[capability.ListKnowledgeDocumentsResult](t, session, "knowledge.document.list", map[string]any{
			"knowledge_id": item.ID, "page": map[string]any{"page_size": 100},
		})
		if len(documents.Items) == 0 {
			continue
		}
		search := callRealTool[capability.SearchKnowledgeResult](t, session, "knowledge.search", map[string]any{
			"query": query, "knowledge_ids": []string{item.ID}, "top_k": 10,
		})
		if len(search.Hits) == 0 {
			continue
		}
		for _, hit := range search.Hits {
			if hit.KnowledgeID != item.ID || strings.TrimSpace(hit.DocumentID) == "" || strings.TrimSpace(hit.Text) == "" {
				t.Fatalf("knowledge.search returned an invalid mapped hit: %#v", hit)
			}
		}
		selectedKnowledge, selectedHit = item, search.Hits[0]
		break
	}
	if selectedHit.DocumentID == "" {
		t.Fatalf("knowledge.search returned no real retrieval hits for %q in any accessible knowledge base", query)
	}
	document := callRealTool[capability.GetKnowledgeDocumentResult](t, session, "knowledge.document.get", map[string]any{
		"knowledge_id": selectedKnowledge.ID, "document_id": selectedHit.DocumentID,
	})
	if document.Document.ID != selectedHit.DocumentID || document.Document.KnowledgeID != selectedKnowledge.ID {
		t.Fatalf("knowledge.document.get returned the wrong real document: %#v", document)
	}

	cloudSources := callRealTool[capability.ListCloudDocumentsResult](t, session, "cloud_document.list", map[string]any{
		"page": map[string]any{"page_size": 100},
	})
	if len(cloudSources.Items) == 0 {
		t.Fatal("cloud_document.list returned no real chat-enabled Feishu accounts")
	}
	cloudVerified := false
	for _, source := range cloudSources.Items {
		page := callRealTool[capability.GetCloudDocumentResult](t, session, "cloud_document.get", map[string]any{
			"source_id": source.ID, "include_documents": true,
			"documents_page": map[string]any{"page_size": 20},
		})
		query := ""
		seenDocuments := map[string]struct{}{}
		for depth := 0; depth < 3; depth++ {
			var nextContainer *capability.CloudDocumentMetadata
			for index := range page.Documents {
				item := page.Documents[index]
				seenDocuments[item.ID] = struct{}{}
				if item.IsDocument && item.DisplayName != "" {
					query = item.DisplayName
					break
				}
				if nextContainer == nil && item.HasChildren {
					nextContainer = &item
				}
			}
			if query != "" || nextContainer == nil {
				break
			}
			page = callRealTool[capability.GetCloudDocumentResult](t, session, "cloud_document.get", map[string]any{
				"source_id": source.ID, "include_documents": true,
				"node_ref": nextContainer.NodeRef, "target_type": nextContainer.TargetType,
				"target_ref":     nextContainer.TargetRef,
				"documents_page": map[string]any{"page_size": 20},
			})
		}
		if page.DocumentsPage != nil && page.DocumentsPage.NextPageToken != "" {
			next := callRealTool[capability.GetCloudDocumentResult](t, session, "cloud_document.get", map[string]any{
				"source_id": source.ID, "include_documents": true,
				"documents_page": map[string]any{
					"page_size": 20, "page_token": page.DocumentsPage.NextPageToken,
				},
			})
			for _, item := range next.Documents {
				if _, duplicate := seenDocuments[item.ID]; duplicate {
					t.Fatalf("cloud document pagination repeated %q", item.ID)
				}
			}
		}
		if query == "" {
			continue
		}
		search := callRealTool[capability.SearchCloudDocumentsResult](t, session, "cloud_document.search", map[string]any{
			"source_id": source.ID, "query": query,
			"include_documents": true, "include_containers": true,
			"page": map[string]any{"page_size": 20},
		})
		if len(search.Hits) > 0 {
			cloudVerified = true
			break
		}
	}
	if !cloudVerified {
		t.Fatal("cloud_document.search returned no real metadata hits")
	}

	if agent := strings.TrimSpace(os.Getenv("LAZYMIND_REAL_EXTERNAL_AGENT")); agent != "" {
		runRealExternalAgent(t, agent, baseURL, token, marker, query)
	}
}

func runRealExternalAgent(t *testing.T, agent, baseURL, token, marker, query string) {
	t.Helper()
	if agent != "codex" {
		t.Fatalf("unsupported LAZYMIND_REAL_EXTERNAL_AGENT %q", agent)
	}
	path, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal("codex CLI is required for the external-agent E2E test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	prompt := fmt.Sprintf(`Validate the LazyMind MCP integration. You MUST actually call all six tools on the "lazymind" server: skill.list, skill.get, knowledge.list, knowledge.document.list, knowledge.document.get, and knowledge.search. Use skill.list to find the skill named %q, then skill.get with include_content=true and confirm its content contains %q. Use knowledge.list to find an accessible knowledge base, use knowledge.document.list on it, then call knowledge.search with the exact query %q. If that knowledge base has no hit, try the next accessible knowledge base. Require at least one retrieval hit, then call knowledge.document.get for a returned document_id. knowledge.search is retrieval only: do not ask it to generate an answer. Only after every call succeeds, reply exactly: LAZYMIND_AGENT_E2E_OK %s`, marker, marker, query, marker)
	command := exec.CommandContext(ctx, path,
		"exec",
		"--ignore-user-config",
		"--ignore-rules",
		"--ephemeral",
		"--skip-git-repo-check",
		"--sandbox", "read-only",
		"--color", "never",
		"--json",
		"-C", t.TempDir(),
		"-c", fmt.Sprintf("mcp_servers.lazymind.url=%q", baseURL+"/api/core/mcp/capabilities/v1"),
		"-c", `mcp_servers.lazymind.bearer_token_env_var="LAZYMIND_AGENT_E2E_TOKEN"`,
		prompt,
	)
	command.Env = append(os.Environ(), "LAZYMIND_AGENT_E2E_TOKEN="+token)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("codex external-agent E2E failed: %v\n%s", err, output)
	}
	transcript := string(output)
	for _, required := range []string{"skill.list", "skill.get", "knowledge.list", "knowledge.document.list", "knowledge.document.get", "knowledge.search", "LAZYMIND_AGENT_E2E_OK " + marker} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("codex external-agent E2E did not prove %q was used or observed:\n%s", required, transcript)
		}
	}
}

func realLogin(t *testing.T, client *http.Client, baseURL, username, password string) string {
	t.Helper()
	var response struct {
		AccessToken string `json:"access_token"`
	}
	realAPI(t, client, http.MethodPost, baseURL+"/api/authservice/auth/login", "", map[string]any{
		"username": username, "password": password,
	}, &response)
	if strings.TrimSpace(response.AccessToken) == "" {
		t.Fatal("real auth login returned an empty access token")
	}
	return response.AccessToken
}

func createRealSkillFixture(t *testing.T, client *http.Client, baseURL, token string) (string, string) {
	t.Helper()
	marker := fmt.Sprintf("mcp-real-e2e-%d", time.Now().UnixNano())
	var response struct {
		SkillID string `json:"skill_id"`
	}
	realAPI(t, client, http.MethodPost, baseURL+"/api/core/skills", token, map[string]any{
		"name":        marker,
		"category":    "testing",
		"description": "Temporary real-service MCP validation fixture",
		"tags":        []string{"mcp-real-e2e"},
		"content":     "Use this temporary skill only to verify " + marker + ".",
		"is_enabled":  true,
	}, &response)
	if strings.TrimSpace(response.SkillID) == "" {
		t.Fatal("real skill creation returned an empty skill ID")
	}
	return response.SkillID, marker
}

func deleteRealSkillFixture(t *testing.T, client *http.Client, baseURL, token, skillID string) {
	t.Helper()
	if skillID == "" {
		return
	}
	if err := realAPICleanup(client, http.MethodDelete, baseURL+"/api/core/skills/"+skillID, token); err != nil {
		t.Errorf("trash real skill fixture: %v", err)
		return
	}
	if err := realAPICleanup(client, http.MethodDelete, baseURL+"/api/core/skills/"+skillID+":purge", token); err != nil {
		t.Errorf("purge real skill fixture: %v", err)
	}
}

func callRealTool[T any](t *testing.T, session *mcp.ClientSession, name string, arguments map[string]any) T {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("call real tool %s: %v", name, err)
	}
	if result.IsError {
		t.Fatalf("real tool %s returned an error: %s", name, toolText(result))
	}
	payload, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("encode real tool %s structured result: %v", name, err)
	}
	var out T
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("decode real tool %s structured result: %v; payload=%s", name, err, payload)
	}
	return out
}

func containsSkill(items []capability.SkillSummary, skillID string) bool {
	for _, item := range items {
		if item.ID == skillID {
			return true
		}
	}
	return false
}

func toolText(result *mcp.CallToolResult) string {
	parts := make([]string, 0, len(result.Content))
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func realAPI(t *testing.T, client *http.Client, method, endpoint, token string, input any, output any) {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(context.Background(), method, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("real API %s %s: %v", method, endpoint, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		t.Fatalf("real API %s %s status=%d body=%s", method, endpoint, response.StatusCode, responseBody)
	}
	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		t.Fatalf("decode real API envelope: %v; body=%s", err, responseBody)
	}
	if envelope.Code != 0 && envelope.Code != http.StatusOK {
		t.Fatalf("real API envelope code=%d message=%q", envelope.Code, envelope.Message)
	}
	if output != nil {
		if err := json.Unmarshal(envelope.Data, output); err != nil {
			t.Fatalf("decode real API data: %v; data=%s", err, envelope.Data)
		}
	}
}

func realAPICleanup(client *http.Client, method, endpoint, token string) error {
	request, err := http.NewRequestWithContext(context.Background(), method, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("status=%d body=%s", response.StatusCode, body)
	}
	return nil
}
