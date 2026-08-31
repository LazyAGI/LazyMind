package agentconnector_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lazymind/agentconnector/internal/agentcatalog"
	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/chatagent"
)

func TestRealConnectorStdioCallsAllLazyMindTools(t *testing.T) {
	if os.Getenv("LAZYMIND_REAL_CONNECTOR_E2E") != "1" {
		t.Skip("set LAZYMIND_REAL_CONNECTOR_E2E=1 to run against a real logged-in LazyMind stack")
	}
	binary := strings.TrimSpace(os.Getenv("LAZYMIND_REAL_CONNECTOR_BIN"))
	if binary == "" {
		t.Fatal("LAZYMIND_REAL_CONNECTOR_BIN is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "mcp", "proxy")
	command.Env = os.Environ()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "lazymind-connector-real-e2e", Version: "1"}, nil).
		Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatalf("connect through real stdio bridge: %v", err)
	}
	defer session.Close()

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list real bridged tools: %v", err)
	}
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	want := "cloud_document.get,cloud_document.list,cloud_document.search,knowledge.document.get,knowledge.document.list,knowledge.list,knowledge.search,skill.get,skill.list,workflow.artifact.get,workflow.artifact.list,workflow.get,workflow.input.get,workflow.input.import,workflow.list,workflow.session.list,workflow.session.resume,workflow.session.stop,workflow.start,workflow.state,workflow.step.begin,workflow.step.resume,workflow.step.submit"
	if strings.Join(names, ",") != want {
		t.Fatalf("bridged tools = %v, want %s", names, want)
	}
	serverURL, accessToken := realCredentials(t)
	skillID, skillMarker := createSkillFixture(t, ctx, serverURL, accessToken)
	t.Cleanup(func() { deleteSkillFixture(t, serverURL, accessToken, skillID) })
	knowledge := createKnowledgeFixture(t, ctx, serverURL, accessToken)

	skills := callTool(t, ctx, session, "skill.list", map[string]any{"page": map[string]any{"page_size": 100}})
	skillItems := objectItems(t, skills)
	foundSkill := false
	for _, item := range skillItems {
		if stringField(t, item, "id") == skillID {
			foundSkill = true
			break
		}
	}
	if !foundSkill {
		t.Fatalf("real skill.list did not return temporary skill %s", skillID)
	}
	skill := callTool(t, ctx, session, "skill.get", map[string]any{"skill_id": skillID, "include_content": true})
	if stringField(t, objectField(t, skill, "skill"), "id") != skillID {
		t.Fatal("real skill.get returned a different skill")
	}

	knowledgeList := callTool(t, ctx, session, "knowledge.list", map[string]any{
		"keyword": knowledge.Name, "page": map[string]any{"page_size": 10},
	})
	knowledgeItems := objectItems(t, knowledgeList)
	if len(knowledgeItems) != 1 || stringField(t, knowledgeItems[0], "id") != knowledge.ID {
		t.Fatalf("real knowledge.list did not isolate temporary knowledge base %s", knowledge.ID)
	}
	documents := callTool(t, ctx, session, "knowledge.document.list", map[string]any{
		"knowledge_id": knowledge.ID, "page": map[string]any{"page_size": 10},
	})
	documentItems := objectItems(t, documents)
	if len(documentItems) != 1 {
		t.Fatalf("real knowledge.document.list returned %d synthetic documents, want 1", len(documentItems))
	}
	search := callTool(t, ctx, session, "knowledge.search", map[string]any{
		"query": knowledge.Query, "knowledge_ids": []string{knowledge.ID}, "top_k": 10,
	})
	hits, ok := search["hits"].([]any)
	if !ok || len(hits) == 0 {
		t.Fatalf("temporary synthetic knowledge base returned no hit for %q", knowledge.Query)
	}
	firstHit, ok := hits[0].(map[string]any)
	if !ok {
		t.Fatal("real knowledge.search returned a non-object hit")
	}
	documentID := stringField(t, firstHit, "document_id")
	document := callTool(t, ctx, session, "knowledge.document.get", map[string]any{
		"knowledge_id": knowledge.ID, "document_id": documentID, "include_content": true,
	})
	detail := objectField(t, document, "document")
	if stringField(t, detail, "id") != documentID || stringField(t, detail, "knowledge_id") != knowledge.ID {
		t.Fatal("real knowledge.document.get returned the wrong document")
	}
	if os.Getenv("LAZYMIND_REAL_CONNECTOR_CLOUD_E2E") == "1" {
		verifyRealCloudDocuments(t, ctx, session)
	} else {
		t.Log("cloud document calls skipped: no authorized cloud account was requested for this run")
	}
	workflowSessionID := verifyRealWorkflowRuntime(t, ctx, session)
	verifyInvocationLedger(t, ctx, serverURL, accessToken, workflowSessionID)
	verifyRejectedTokenRefresh(t, ctx, session)
	if mode := strings.TrimSpace(os.Getenv("LAZYMIND_REAL_CONNECTOR_CODEX")); mode != "" {
		codexHome := strings.TrimSpace(os.Getenv("LAZYMIND_REAL_CONNECTOR_CODEX_HOME"))
		if codexHome == "" {
			t.Fatal("LAZYMIND_REAL_CONNECTOR_CODEX_HOME is required for isolated real Codex validation")
		}
		configureCodex(t, ctx, mode, binary, codexHome)
		runCodexE2E(t, ctx, codexHome, skillMarker, knowledge)
		threadID := runCodexThreadBootstrapE2E(t, ctx, codexHome)
		conversationID := verifyCodexConversationBootstrap(t, ctx, serverURL, accessToken, threadID)
		resumeCodexWorkflowE2E(t, ctx, codexHome, threadID)
		resumeCodexThreadE2E(t, ctx, codexHome, threadID)
		verifyCodexConversationProjection(t, ctx, serverURL, accessToken, threadID, conversationID)
	}
}

func TestRealConnectorCloudDocuments(t *testing.T) {
	if os.Getenv("LAZYMIND_REAL_CONNECTOR_CLOUD_E2E") != "1" {
		t.Skip("set LAZYMIND_REAL_CONNECTOR_CLOUD_E2E=1 to validate real Feishu cloud documents")
	}
	binary := strings.TrimSpace(os.Getenv("LAZYMIND_REAL_CONNECTOR_BIN"))
	if binary == "" {
		t.Fatal("LAZYMIND_REAL_CONNECTOR_BIN is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "mcp", "proxy")
	command.Env = os.Environ()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "lazymind-cloud-document-real-e2e", Version: "1"}, nil).
		Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatalf("connect through real stdio bridge: %v", err)
	}
	defer session.Close()
	verifyRealCloudDocuments(t, ctx, session)
}

func TestRealAssistantIntegrationLifecycle(t *testing.T) {
	if os.Getenv("LAZYMIND_REAL_ASSISTANT_INTEGRATION_E2E") != "1" {
		t.Skip("set LAZYMIND_REAL_ASSISTANT_INTEGRATION_E2E=1 to validate installed Assistant integrations")
	}
	binary := strings.TrimSpace(os.Getenv("LAZYMIND_REAL_CONNECTOR_BIN"))
	if binary == "" {
		t.Fatal("LAZYMIND_REAL_CONNECTOR_BIN is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	agents := []string{"codex", "cursor", "workbuddy", "raccoon", "traework", "deepseek-harness"}
	for _, agent := range agents {
		t.Run(agent, func(t *testing.T) {
			run := func(action string) agentintegration.Status {
				t.Helper()
				command := exec.CommandContext(ctx, binary, "internal", "agent", agent, action)
				command.Env = os.Environ()
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("%s %s: %v\n%s", agent, action, err, output)
				}
				var status agentintegration.Status
				if err := json.Unmarshal(output, &status); err != nil {
					t.Fatalf("decode %s %s: %v\n%s", agent, action, err, output)
				}
				return status
			}

			disconnected := run("disconnect")
			if disconnected.State != agentintegration.Ready {
				t.Fatalf("%s did not reach ready before connect: %#v", agent, disconnected)
			}
			for _, requirement := range disconnected.Requirements {
				if !requirement.Satisfied {
					t.Fatalf("%s real requirement is not satisfied: %#v", agent, requirement)
				}
			}
			t.Cleanup(func() { _ = run("disconnect") })

			connected := run("connect")
			if agent == "cursor" {
				if connected.State != agentintegration.ActionRequired || connected.Action == nil ||
					connected.Action.Kind != "open_url" || !strings.HasPrefix(connected.Action.URL, "cursor://") {
					t.Fatalf("Cursor did not return its native MCP confirmation link: %#v", connected)
				}
			} else if connected.State != agentintegration.Enabled {
				t.Fatalf("%s real connect state=%q message=%q", agent, connected.State, connected.Message)
			}

			if final := run("disconnect"); final.State != agentintegration.Ready {
				t.Fatalf("%s did not cleanly disconnect: %#v", agent, final)
			}
		})
	}
}

func verifyRealCloudDocuments(t *testing.T, ctx context.Context, session *mcp.ClientSession) {
	t.Helper()
	cloudList := callTool(t, ctx, session, "cloud_document.list", map[string]any{
		"page": map[string]any{"page_size": 100},
	})
	cloudItems := objectItems(t, cloudList)
	if len(cloudItems) == 0 {
		t.Fatal("real cloud_document.list returned no chat-enabled Feishu accounts")
	}
	for _, account := range cloudItems {
		accountID := stringField(t, account, "id")
		cloud := callTool(t, ctx, session, "cloud_document.get", map[string]any{
			"source_id": accountID, "include_documents": true,
			"documents_page": map[string]any{"page_size": 20},
		})
		query := ""
		for depth := 0; depth < 3; depth++ {
			documents, _ := cloud["documents"].([]any)
			var next map[string]any
			for _, raw := range documents {
				item, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				display, _ := item["display_name"].(string)
				isDocument, _ := item["is_document"].(bool)
				hasChildren, _ := item["has_children"].(bool)
				if isDocument && strings.TrimSpace(display) != "" {
					query = display
					break
				}
				if next == nil && hasChildren {
					next = item
				}
			}
			if query != "" || next == nil {
				break
			}
			cloud = callTool(t, ctx, session, "cloud_document.get", map[string]any{
				"source_id": accountID, "include_documents": true,
				"node_ref": next["node_ref"], "target_type": next["target_type"],
				"target_ref":     next["target_ref"],
				"documents_page": map[string]any{"page_size": 20},
			})
		}
		if query == "" {
			continue
		}
		search := callTool(t, ctx, session, "cloud_document.search", map[string]any{
			"source_id": accountID, "query": query,
			"include_documents": true, "include_containers": true,
			"page": map[string]any{"page_size": 20},
		})
		if hits, ok := search["hits"].([]any); ok && len(hits) > 0 {
			return
		}
	}
	t.Fatal("real cloud_document.search returned no online Feishu document hits")
}

func TestRealCodexExternalThreadBinding(t *testing.T) {
	if os.Getenv("LAZYMIND_REAL_CODEX_BINDING_E2E") != "1" {
		t.Skip("set LAZYMIND_REAL_CODEX_BINDING_E2E=1 to validate direct Codex thread projection")
	}
	binary := strings.TrimSpace(os.Getenv("LAZYMIND_REAL_CONNECTOR_BIN"))
	if binary == "" {
		t.Fatal("LAZYMIND_REAL_CONNECTOR_BIN is required")
	}
	codexHome := strings.TrimSpace(os.Getenv("LAZYMIND_REAL_CONNECTOR_CODEX_HOME"))
	if codexHome == "" {
		t.Fatal("LAZYMIND_REAL_CONNECTOR_CODEX_HOME is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	serverURL, accessToken := realCredentials(t)
	configureCodex(t, ctx, "manual", binary, codexHome)
	threadID := runCodexThreadBootstrapE2E(t, ctx, codexHome)
	conversationID := verifyCodexConversationBootstrap(t, ctx, serverURL, accessToken, threadID)
	resumeCodexWorkflowE2E(t, ctx, codexHome, threadID)
	resumeCodexThreadE2E(t, ctx, codexHome, threadID)
	verifyCodexConversationProjection(t, ctx, serverURL, accessToken, threadID, conversationID)
}

func TestRealCodexWorkflowSessionScope(t *testing.T) {
	if os.Getenv("LAZYMIND_REAL_CODEX_WORKFLOW_SCOPE_E2E") != "1" {
		t.Skip("set LAZYMIND_REAL_CODEX_WORKFLOW_SCOPE_E2E=1 to validate Codex Workflow session scoping")
	}
	binary := strings.TrimSpace(os.Getenv("LAZYMIND_REAL_CONNECTOR_BIN"))
	threadID := strings.TrimSpace(os.Getenv("LAZYMIND_REAL_SCOPE_THREAD_ID"))
	if binary == "" || threadID == "" {
		t.Fatal("LAZYMIND_REAL_CONNECTOR_BIN and LAZYMIND_REAL_SCOPE_THREAD_ID are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "mcp", "proxy")
	command.Env = os.Environ()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "codex-workflow-scope-real-e2e", Version: "1"}, nil).
		Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatalf("connect through real Codex-scoped stdio bridge: %v", err)
	}
	defer session.Close()
	meta := mcp.Meta{
		"threadId": threadID, "turnId": fmt.Sprintf("scope-e2e-%d", time.Now().UnixNano()), "threadSource": "user",
	}
	listed := callToolWithMeta(t, ctx, session, "workflow.session.list", map[string]any{"page_size": 100}, meta)
	rawSessions, ok := listed["sessions"].([]any)
	if !ok || len(rawSessions) != 1 {
		t.Fatalf("current Codex conversation returned %d Workflow sessions: %#v", len(rawSessions), listed)
	}
	current, ok := rawSessions[0].(map[string]any)
	if !ok || stringField(t, current, "conversation_id") == "" || stringField(t, current, "session_id") == "" {
		t.Fatalf("scoped Workflow session is incomplete: %#v", rawSessions[0])
	}
}

func TestRealNativeAgentSessionCatalogs(t *testing.T) {
	if os.Getenv("LAZYMIND_REAL_NATIVE_SESSION_CATALOG_E2E") != "1" {
		t.Skip("set LAZYMIND_REAL_NATIVE_SESSION_CATALOG_E2E=1 to inspect native Agent catalogs")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	tests := []struct {
		name string
		load func(context.Context) ([]chatagent.NativeSession, error)
	}{
		{name: "codex", load: agentcatalog.CodexSessions},
		{name: "cursor", load: agentcatalog.CursorSessions},
		{name: "workbuddy", load: agentcatalog.WorkBuddySessions},
	}
	resumableThreads := make(map[string]map[string]struct{}, len(tests))
	nativeSessions := make(map[string]map[string]chatagent.NativeSession, len(tests))
	for _, test := range tests {
		sessions, err := test.load(ctx)
		if err != nil {
			t.Fatalf("%s catalog: %v", test.name, err)
		}
		used, importedTurns := 0, 0
		projects := map[string]struct{}{}
		resumableThreads[test.name] = map[string]struct{}{}
		nativeSessions[test.name] = map[string]chatagent.NativeSession{}
		for _, session := range sessions {
			resumableThreads[test.name][session.ThreadID] = struct{}{}
			nativeSessions[test.name][session.ThreadID] = session
			if len(session.Turns) > 0 {
				used++
				importedTurns += len(session.Turns)
				projects[session.ProjectKey] = struct{}{}
			}
		}
		if test.name == "codex" && (len(sessions) == 0 || used == 0 || importedTurns == 0 || len(projects) == 0) {
			t.Fatalf("%s catalog has sessions=%d used=%d turns=%d projects=%d", test.name, len(sessions), used, importedTurns, len(projects))
		}
		t.Logf("%s sessions=%d used=%d turns=%d projects=%d", test.name, len(sessions), used, importedTurns, len(projects))
	}
	if os.Getenv("LAZYMIND_REAL_NATIVE_SESSION_CORE_E2E") != "1" {
		return
	}
	serverURL, accessToken := realCredentials(t)
	for provider, expected := range resumableThreads {
		var projection struct {
			Sessions []struct {
				ThreadID string `json:"provider_thread_id"`
			} `json:"sessions"`
			Total int `json:"total_size"`
		}
		realAPI(t, ctx, http.MethodGet, serverURL+"/api/core/external-chat/providers/"+provider+"/sessions?page_size=100", accessToken, nil, &projection)
		actual := make(map[string]struct{}, len(projection.Sessions))
		for _, session := range projection.Sessions {
			actual[session.ThreadID] = struct{}{}
		}
		missing := make([]string, 0)
		for threadID := range expected {
			if _, ok := actual[threadID]; !ok {
				missing = append(missing, threadID)
			}
		}
		if projection.Total != len(expected) || len(actual) != len(expected) || len(missing) > 0 {
			details := make([]string, 0, len(missing))
			for _, threadID := range missing {
				session := nativeSessions[provider][threadID]
				encoded, _ := json.Marshal(session)
				details = append(details, fmt.Sprintf(
					"%s(project=%d/%d display=%d turns=%d bytes=%d)", threadID,
					len([]rune(session.ProjectKey)), len([]rune(session.ProjectName)),
					len([]rune(session.DisplayName)), len(session.Turns), len(encoded),
				))
			}
			t.Fatalf("%s Core projection total=%d actual=%d native=%d missing=%v", provider, projection.Total, len(actual), len(expected), details)
		}
	}
}

func verifyRealWorkflowRuntime(t *testing.T, ctx context.Context, session *mcp.ClientSession) string {
	t.Helper()
	catalog := callTool(t, ctx, session, "workflow.list", map[string]any{})
	workflows, ok := catalog["workflows"].([]any)
	if !ok {
		t.Fatalf("real workflow.list has no workflows array: %#v", catalog)
	}
	found := false
	for _, raw := range workflows {
		item, _ := raw.(map[string]any)
		if item["workflow_id"] == "test-workflow" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("real workflow.list did not return the built-in test-workflow")
	}
	workflow := callTool(t, ctx, session, "workflow.get", map[string]any{"workflow_id": "test-workflow"})
	if workflow["workflow_id"] != "test-workflow" || workflow["revision_id"] == "" {
		t.Fatalf("real workflow.get returned an invalid package: %#v", workflow)
	}

	imagePath := createWorkflowFixtureFile(t, ".lazymind-workflow-e2e-*.png", mustDecodeBase64(t,
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="))
	textPath := createWorkflowFixtureFile(t, ".lazymind-workflow-e2e-*.txt", []byte("real external Agent Workflow artifact\n"))
	imported := callTool(t, ctx, session, "workflow.input.import", map[string]any{"local_path": imagePath})
	resource := objectField(t, imported, "resource")
	resourceID := stringField(t, resource, "resource_id")
	readInput := callTool(t, ctx, session, "workflow.input.get", map[string]any{"resource_id": resourceID})
	if rendered, _ := readInput["rendered_as_mcp_content"].(bool); !rendered {
		t.Fatalf("real image input was not rendered as MCP image content: %#v", readInput)
	}

	started := callTool(t, ctx, session, "workflow.start", map[string]any{
		"workflow_id": "test-workflow", "request_context": "运行外部 Agent Workflow 真实服务验收",
		"idempotency_key": fmt.Sprintf("connector-workflow-%d", time.Now().UnixNano()),
	})
	sessionID := stringField(t, started, "session_id")
	listedSessions := callTool(t, ctx, session, "workflow.session.list", map[string]any{"status": "active", "page_size": 100})
	foundSession := false
	for _, raw := range listedSessions["sessions"].([]any) {
		item, _ := raw.(map[string]any)
		if item["session_id"] == sessionID {
			foundSession = true
			break
		}
	}
	if !foundSession {
		t.Fatalf("workflow.session.list did not return new external session %s: %#v", sessionID, listedSessions)
	}
	stopCommand := fmt.Sprintf("connector-session-stop-%d", time.Now().UnixNano())
	stopped := callTool(t, ctx, session, "workflow.session.stop", map[string]any{"session_id": sessionID, "command_id": stopCommand})
	stopReplay := callTool(t, ctx, session, "workflow.session.stop", map[string]any{"session_id": sessionID, "command_id": stopCommand})
	if stopped["status"] != "stopped" || stopped["state_version"] != stopReplay["state_version"] {
		t.Fatalf("session stop was not idempotent: first=%#v replay=%#v", stopped, stopReplay)
	}
	resumed := callTool(t, ctx, session, "workflow.session.resume", map[string]any{
		"session_id": sessionID, "command_id": fmt.Sprintf("connector-session-resume-%d", time.Now().UnixNano()),
	})
	if resumed["status"] != "active" {
		t.Fatalf("session resume did not reactivate Workflow: %#v", resumed)
	}
	submittedList := false
	submittedRevision := false
	for iteration := 0; iteration < 12; iteration++ {
		state := callTool(t, ctx, session, "workflow.state", map[string]any{"session_id": sessionID})
		projection := objectField(t, state, "projection")
		if completed, _ := projection["completed"].(bool); completed {
			break
		}
		ready, ok := projection["ready"].([]any)
		if !ok || len(ready) == 0 {
			t.Fatalf("real Workflow has no ready step before completion: %#v", projection)
		}
		stepID, _ := ready[0].(string)
		begun := callTool(t, ctx, session, "workflow.step.begin", map[string]any{
			"session_id": sessionID, "step_id": stepID,
			"command_id": fmt.Sprintf("connector-step-%d-%d", time.Now().UnixNano(), iteration),
		})
		execution := objectField(t, begun, "execution")
		executionID := stringField(t, execution, "execution_id")
		contract := objectField(t, execution, "step_contract")
		if _, leaked := contract["metadata"]; leaked {
			t.Fatalf("public step contract leaked Runtime metadata: %#v", contract["metadata"])
		}
		if iteration == 0 {
			resumed := callTool(t, ctx, session, "workflow.step.resume", map[string]any{
				"session_id": sessionID, "execution_id": executionID,
			})
			resumedExecution := objectField(t, resumed, "execution")
			if stringField(t, resumedExecution, "execution_id") != executionID {
				t.Fatal("resuming after a connector restart changed the execution identity")
			}
			resumedContract := objectField(t, resumedExecution, "step_contract")
			if resumedContract["attempt_id"] != contract["attempt_id"] || resumedContract["workflow_revision"] != contract["workflow_revision"] {
				t.Fatal("resuming did not return the pinned execution contract")
			}
		}
		required, _ := contract["required_outputs"].([]any)
		cardinality, _ := contract["output_cardinality"].(map[string]any)
		outputs := make([]map[string]any, 0, len(required))
		for _, raw := range required {
			slot, _ := raw.(string)
			output := map[string]any{"slot": slot, "value": map[string]any{"text": "real e2e " + slot}}
			if strings.Contains(slot, "image") {
				output = map[string]any{"slot": slot, "local_path": imagePath, "caption": "real image artifact"}
			} else if strings.Contains(slot, "attachment") || strings.Contains(slot, "report") {
				output = map[string]any{"slot": slot, "local_path": textPath, "caption": "real file artifact"}
			}
			if cardinality[slot] == "list" {
				submittedList = true
				output["seq"] = 1
				second := make(map[string]any, len(output))
				for key, value := range output {
					second[key] = value
				}
				second["seq"] = 2
				outputs = append(outputs, output, second)
			} else if slot == "rewritten_attachment" {
				submittedRevision = true
				output["seq"] = 1
				second := make(map[string]any, len(output))
				for key, value := range output {
					second[key] = value
				}
				second["seq"] = 2
				outputs = append(outputs, output, second)
			} else {
				outputs = append(outputs, output)
			}
		}
		submitted := callTool(t, ctx, session, "workflow.step.submit", map[string]any{
			"session_id": sessionID, "execution_id": executionID, "outcome": "succeeded",
			"summary": "real external Agent step completed", "executor_ref": "real-service-e2e", "outputs": outputs,
		})
		if submitted["attempt_status"] != "succeeded" {
			t.Fatalf("real Workflow submit did not succeed: %#v", submitted)
		}
		if iteration == 11 {
			t.Fatal("real Workflow did not complete within 12 steps")
		}
	}
	final := callTool(t, ctx, session, "workflow.state", map[string]any{"session_id": sessionID})
	if completed, _ := objectField(t, final, "projection")["completed"].(bool); !completed {
		t.Fatalf("real Workflow did not reach completed: %#v", final)
	}
	listed := callTool(t, ctx, session, "workflow.artifact.list", map[string]any{"session_id": sessionID})
	artifacts, ok := listed["artifacts"].([]any)
	if !ok || len(artifacts) < 6 {
		t.Fatalf("real Workflow did not persist its artifacts: %#v", listed)
	}
	if !submittedList {
		t.Fatal("real Workflow fixture did not exercise a list-cardinality output")
	}
	if !submittedRevision {
		t.Fatal("real Workflow fixture did not exercise a single-cardinality revision")
	}
	listCounts := map[string]int{}
	revisionPersisted := false
	for _, raw := range artifacts {
		artifact, _ := raw.(map[string]any)
		if artifact["list_index"] != nil {
			listCounts[stringField(t, artifact, "slot")]++
		}
		if artifact["slot"] == "rewritten_attachment" && artifact["revision"] == float64(2) {
			revisionPersisted = true
		}
	}
	listPersisted := false
	for _, count := range listCounts {
		if count >= 2 {
			listPersisted = true
		}
	}
	if !listPersisted {
		t.Fatalf("real Workflow did not retain both selected list items: %#v", listed)
	}
	if !revisionPersisted {
		t.Fatalf("real Workflow did not select the second single-slot revision: %#v", listed)
	}
	first, _ := artifacts[0].(map[string]any)
	artifactID := stringField(t, first, "artifact_id")
	readArtifact := callTool(t, ctx, session, "workflow.artifact.get", map[string]any{"artifact_id": artifactID})
	if objectField(t, readArtifact, "artifact")["artifact_id"] != artifactID {
		t.Fatal("real workflow.artifact.get returned a different revision")
	}
	return sessionID
}

func verifyInvocationLedger(t *testing.T, ctx context.Context, serverURL, token, sessionID string) {
	t.Helper()
	var page struct {
		Invocations []struct {
			InvocationID string `json:"invocation_id"`
			ClientName   string `json:"client_name"`
			Connector    string `json:"connector_name"`
			ToolName     string `json:"tool_name"`
			Status       string `json:"status"`
			SessionID    string `json:"session_id"`
		} `json:"invocations"`
	}
	endpoint := serverURL + "/api/core/agent-invocations?client_name=" +
		url.QueryEscape("lazymind-connector-real-e2e") + "&page_size=100"
	realAPI(t, ctx, http.MethodGet, endpoint, token, nil, &page)
	wantTools := map[string]bool{
		"knowledge.search": false, "workflow.start": false, "workflow.session.list": false,
		"workflow.session.stop": false, "workflow.session.resume": false, "workflow.step.submit": false,
	}
	linkedSession := false
	for _, invocation := range page.Invocations {
		if invocation.InvocationID == "" || invocation.ClientName != "lazymind-connector-real-e2e" || invocation.Connector != "lazymind-mcp" {
			t.Fatalf("invalid invocation provenance: %+v", invocation)
		}
		if invocation.Status == "running" {
			t.Fatalf("real MCP call has no terminal evidence: %+v", invocation)
		}
		if _, tracked := wantTools[invocation.ToolName]; tracked {
			wantTools[invocation.ToolName] = true
		}
		if invocation.SessionID == sessionID {
			linkedSession = true
		}
	}
	for tool, found := range wantTools {
		if !found {
			t.Fatalf("invocation ledger did not record %s: %+v", tool, page.Invocations)
		}
	}
	if !linkedSession {
		t.Fatalf("invocation ledger did not link Workflow session %s", sessionID)
	}
}

func createWorkflowFixtureFile(t *testing.T, pattern string, content []byte) string {
	t.Helper()
	file, err := os.CreateTemp(".", pattern)
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	t.Cleanup(func() { _ = os.Remove(path) })
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func mustDecodeBase64(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

type knowledgeFixture struct {
	ID    string
	Name  string
	Query string
}

func verifyRejectedTokenRefresh(t *testing.T, ctx context.Context, session *mcp.ClientSession) {
	t.Helper()
	path := filepath.Join(os.Getenv("LAZYMIND_HOME"), "credentials.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read credentials before refresh test: %v", err)
	}
	var before map[string]any
	if err := json.Unmarshal(body, &before); err != nil {
		t.Fatalf("decode credentials before refresh test: %v", err)
	}
	oldRefresh, _ := before["refresh_token"].(string)
	before["access_token"] = "connector-real-e2e-rejected-access-token"
	before["expires_in"] = float64(3600)
	before["saved_at"] = float64(time.Now().Unix())
	invalid, err := json.MarshalIndent(before, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(invalid, '\n'), 0o600); err != nil {
		t.Fatalf("install rejected access token fixture: %v", err)
	}
	callTool(t, ctx, session, "knowledge.list", map[string]any{"page": map[string]any{"page_size": 1}})
	afterBody, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read credentials after refresh test: %v", err)
	}
	var after map[string]any
	if err := json.Unmarshal(afterBody, &after); err != nil {
		t.Fatalf("decode credentials after refresh test: %v", err)
	}
	if after["access_token"] == before["access_token"] || after["refresh_token"] == oldRefresh {
		t.Fatal("running stdio bridge did not rotate the rejected access/refresh token pair")
	}
}

func realCredentials(t *testing.T) (string, string) {
	t.Helper()
	home := strings.TrimSpace(os.Getenv("LAZYMIND_HOME"))
	if home == "" {
		t.Fatal("LAZYMIND_HOME is required for the real connector test")
	}
	body, err := os.ReadFile(filepath.Join(home, "credentials.json"))
	if err != nil {
		t.Fatalf("read real connector credentials: %v", err)
	}
	var value struct {
		ServerURL   string `json:"server_url"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("decode real connector credentials: %v", err)
	}
	if value.ServerURL == "" || value.AccessToken == "" {
		t.Fatal("real connector credentials are incomplete")
	}
	return strings.TrimRight(value.ServerURL, "/"), value.AccessToken
}

func createSkillFixture(t *testing.T, ctx context.Context, serverURL, token string) (string, string) {
	t.Helper()
	marker := fmt.Sprintf("connector-real-e2e-%d", time.Now().UnixNano())
	var response struct {
		SkillID string `json:"skill_id"`
	}
	realAPI(t, ctx, http.MethodPost, serverURL+"/api/core/skills", token, map[string]any{
		"name": marker, "category": "testing", "description": "Temporary connector real-service fixture",
		"tags": []string{"connector-real-e2e"}, "content": "Temporary connector validation " + marker,
		"is_enabled": true,
	}, &response)
	if response.SkillID == "" {
		t.Fatal("create real skill fixture returned no skill_id")
	}
	return response.SkillID, marker
}

func createKnowledgeFixture(t *testing.T, ctx context.Context, serverURL, token string) knowledgeFixture {
	t.Helper()
	nonce := time.Now().UnixNano()
	fixture := knowledgeFixture{
		ID:    fmt.Sprintf("lm-e2e-%d", nonce),
		Name:  fmt.Sprintf("Connector-Synthetic-E2E-%d", nonce),
		Query: "星河蓝鲸合成验收口令",
	}
	algoID := strings.TrimSpace(os.Getenv("LAZYMIND_REAL_CONNECTOR_ALGO_ID"))
	if algoID == "" {
		algoID = "general_algo"
	}
	var created struct {
		DatasetID string `json:"dataset_id"`
	}
	realAPI(t, ctx, http.MethodPost,
		serverURL+"/api/core/datasets?dataset_id="+url.QueryEscape(fixture.ID), token,
		map[string]any{
			"display_name": fixture.Name,
			"desc":         "Synthetic connector E2E fixture with no user data",
			"algo":         map[string]any{"algo_id": algoID},
		}, &created)
	if created.DatasetID != fixture.ID {
		t.Fatalf("create synthetic knowledge base returned %q, want %q", created.DatasetID, fixture.ID)
	}
	t.Cleanup(func() { deleteKnowledgeFixture(t, serverURL, token, fixture.ID) })

	content := fmt.Sprintf("这是外部 Agent 连接器的合成验收文档。临时验收口令是：%s。随机测试标记是 %d。本文不包含任何用户私有内容。", fixture.Query, nonce)
	taskID := uploadSyntheticDocument(t, ctx, serverURL, token, fixture.ID, content)
	realAPI(t, ctx, http.MethodPost, serverURL+"/api/core/datasets/"+fixture.ID+"/tasks:start", token,
		map[string]any{"task_ids": []string{taskID}}, nil)
	waitForSyntheticDocument(t, ctx, serverURL, token, fixture.ID, taskID)
	return fixture
}

func uploadSyntheticDocument(t *testing.T, ctx context.Context, serverURL, token, knowledgeID, content string) string {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", "connector-synthetic-e2e.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(part, content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		serverURL+"/api/core/datasets/"+knowledgeID+"/tasks:batchUpload", bytes.NewReader(body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	var response struct {
		Tasks []struct {
			TaskID string `json:"task_id"`
		} `json:"tasks"`
	}
	realRequest(t, request, &response)
	if len(response.Tasks) != 1 || response.Tasks[0].TaskID == "" {
		t.Fatal("synthetic document upload did not create exactly one task")
	}
	return response.Tasks[0].TaskID
}

func waitForSyntheticDocument(t *testing.T, ctx context.Context, serverURL, token, knowledgeID, taskID string) {
	t.Helper()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		var task struct {
			State string `json:"task_state"`
			Error string `json:"err_msg"`
		}
		realAPI(t, ctx, http.MethodGet,
			serverURL+"/api/core/datasets/"+knowledgeID+"/tasks/"+taskID, token, nil, &task)
		switch strings.ToUpper(strings.TrimSpace(task.State)) {
		case "SUCCESS", "SUCCEEDED":
			return
		case "FAILED", "ERROR", "CANCELED", "CANCELLED":
			t.Fatalf("synthetic document task ended in %s: %s", task.State, task.Error)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for synthetic document: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func configureCodex(t *testing.T, ctx context.Context, mode, connectorBinary, codexHome string) {
	t.Helper()
	codexBinary, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal("real Codex CLI is required for Codex validation")
	}
	commandEnv := append(os.Environ(), "CODEX_HOME="+codexHome)
	run := func(program string, arguments ...string) ([]byte, error) {
		command := exec.CommandContext(ctx, program, arguments...)
		command.Env = commandEnv
		return command.CombinedOutput()
	}
	_, _ = run(codexBinary, "mcp", "remove", "lazymind")
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		command := exec.CommandContext(cleanupCtx, codexBinary, "mcp", "remove", "lazymind")
		command.Env = commandEnv
		_, _ = command.CombinedOutput()
	})

	switch mode {
	case "manual":
		arguments := []string{"mcp", "add", "lazymind"}
		if home := strings.TrimSpace(os.Getenv("LAZYMIND_HOME")); home != "" {
			arguments = append(arguments, "--env", "LAZYMIND_HOME="+home)
		}
		arguments = append(arguments, "--", connectorBinary, "mcp", "proxy")
		if output, err := run(codexBinary, arguments...); err != nil {
			t.Fatalf("real Codex manual mcp add failed: %v\n%s", err, output)
		}
	case "adapter":
		if output, err := run(codexBinary, "mcp", "add", "lazymind", "--", connectorBinary, "foreign"); err != nil {
			t.Fatalf("prepare foreign Codex MCP configuration: %v\n%s", err, output)
		}
		if output, err := run(connectorBinary, "internal", "agent", "codex", "connect", "--agent-bin", codexBinary); err == nil || !strings.Contains(string(output), "not managed by this LazyMind installation") {
			t.Fatalf("Adapter did not safely refuse a foreign Codex config: err=%v\n%s", err, output)
		}
		if output, err := run(codexBinary, "mcp", "get", "lazymind", "--json"); err != nil || !strings.Contains(string(output), "foreign") {
			t.Fatalf("Adapter changed the foreign Codex config: err=%v\n%s", err, output)
		}
		if output, err := run(codexBinary, "mcp", "remove", "lazymind"); err != nil {
			t.Fatalf("remove foreign Codex MCP configuration: %v\n%s", err, output)
		}
		if output, err := run(codexBinary,
			"mcp", "add", "lazymind",
			"--env", "LAZYMIND_HOME="+filepath.Join(codexHome, "stale-home"),
			"--", connectorBinary, "mcp", "proxy",
		); err != nil {
			t.Fatalf("prepare stale owned Codex MCP configuration: %v\n%s", err, output)
		}
		for _, action := range []string{"connect", "connect", "status", "disconnect", "connect"} {
			output, err := run(connectorBinary, "internal", "agent", "codex", action, "--agent-bin", codexBinary)
			if err != nil {
				t.Fatalf("real Codex Adapter %s failed: %v\n%s", action, err, output)
			}
			var status struct {
				State string `json:"state"`
			}
			if err := json.Unmarshal(output, &status); err != nil {
				t.Fatalf("decode real Codex Adapter %s status: %v\n%s", action, err, output)
			}
			wantState := "enabled"
			if action == "disconnect" {
				wantState = "ready"
			}
			if status.State != wantState {
				t.Fatalf("unexpected real Codex Adapter %s status: %s", action, output)
			}
		}
	default:
		t.Fatalf("LAZYMIND_REAL_CONNECTOR_CODEX must be manual or adapter, got %q", mode)
	}
	output, err := run(codexBinary, "mcp", "get", "lazymind", "--json")
	if err != nil {
		t.Fatalf("real Codex MCP configuration is missing after %s setup: %v\n%s", mode, err, output)
	}
	if !strings.Contains(string(output), connectorBinary) || !strings.Contains(string(output), "mcp") || !strings.Contains(string(output), "proxy") {
		t.Fatalf("real Codex MCP configuration does not use the connector: %s", output)
	}
}

func runCodexE2E(t *testing.T, ctx context.Context, codexHome, skillMarker string, knowledge knowledgeFixture) {
	t.Helper()
	codexBinary := realCodexBinary(t)
	prompt := fmt.Sprintf(`Validate the LazyMind connector using only temporary synthetic fixtures. You MUST actually call all six tools from the "lazymind" MCP server. Call skill.list with keyword %q, then skill.get for that one result. Call knowledge.list with keyword %q and use only knowledge base %q. Call knowledge.document.list for it, call knowledge.search with exact query %q and only that knowledge ID, then call knowledge.document.get with include_content=true for the returned document. Do not access any other skill, knowledge base, or document. knowledge.search is retrieval-only. After every tool succeeds, reply exactly: LAZYMIND_CONNECTOR_CODEX_E2E_OK %s`, skillMarker, knowledge.Name, knowledge.ID, knowledge.Query, skillMarker)
	arguments := []string{"exec",
		"--ignore-rules", "--ephemeral", "--skip-git-repo-check",
		"--sandbox", "read-only", "--color", "never", "--json", "-C", t.TempDir(),
	}
	arguments = append(arguments, prompt)
	command := exec.CommandContext(ctx, codexBinary, arguments...)
	command.Env = append(os.Environ(), "CODEX_HOME="+codexHome)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("real Codex connector E2E failed: %v\n%s", err, output)
	}
	transcript := string(output)
	for _, required := range []string{
		"skill.list", "skill.get", "knowledge.list", "knowledge.document.list",
		"knowledge.search", "knowledge.document.get", "LAZYMIND_CONNECTOR_CODEX_E2E_OK " + skillMarker,
	} {
		if !strings.Contains(transcript, required) {
			if len(transcript) > 64<<10 {
				transcript = transcript[len(transcript)-(64<<10):]
			}
			t.Fatalf("real Codex transcript did not prove %q was used or observed\n%s", required, transcript)
		}
	}
}

const realCodexWorkflowPrompt = `Use the "lazymind" MCP server to execute the published LazyMind Workflow named test-workflow. This is a real orchestration validation, not an explanation. You MUST call workflow.list and workflow.get, then workflow.start with a unique idempotency_key and request_context "Codex external Agent Workflow real E2E". Repeatedly call workflow.state, choose exactly one ready step, call workflow.step.begin, follow the returned immutable step_contract, and call workflow.step.submit with outcome succeeded and one inline JSON value for every required output slot. Continue until workflow.state reports completed=true. Then call workflow.artifact.list and workflow.artifact.get for one returned artifact. Do not use any Skill or Knowledge tools. Do not stop early and do not claim success from prose. After all real calls succeed, reply exactly: LAZYMIND_CODEX_WORKFLOW_E2E_OK`

func runCodexWorkflowE2E(t *testing.T, ctx context.Context, codexHome string) string {
	t.Helper()
	codexBinary := realCodexBinary(t)
	arguments := []string{"exec",
		"--ignore-rules", "--skip-git-repo-check",
		"--sandbox", "workspace-write", "--color", "never", "--json", "-C", t.TempDir(),
		realCodexWorkflowPrompt,
	}
	command := exec.CommandContext(ctx, codexBinary, arguments...)
	command.Env = append(os.Environ(), "CODEX_HOME="+codexHome)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("real Codex Workflow E2E failed: %v\n%s", err, output)
	}
	verifyCodexWorkflowTranscript(t, output)
	return codexThreadID(t, output)
}

func runCodexThreadBootstrapE2E(t *testing.T, ctx context.Context, codexHome string) string {
	t.Helper()
	prompt := `Use the "lazymind" MCP server for one ordinary non-Workflow operation. Call knowledge.list exactly once with page_size 1, then reply exactly: LAZYMIND_CODEX_THREAD_BOOTSTRAP_OK`
	command := exec.CommandContext(ctx, realCodexBinary(t), "exec",
		"--ignore-rules", "--skip-git-repo-check", "--sandbox", "workspace-write",
		"--color", "never", "--json", "-C", t.TempDir(), prompt)
	command.Env = append(os.Environ(), "CODEX_HOME="+codexHome)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("real Codex ordinary LazyMind use failed: %v\n%s", err, output)
	}
	transcript := string(output)
	for _, required := range []string{"knowledge.list", "LAZYMIND_CODEX_THREAD_BOOTSTRAP_OK"} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("ordinary Codex transcript did not prove %q\n%s", required, transcript)
		}
	}
	return codexThreadID(t, output)
}

func resumeCodexWorkflowE2E(t *testing.T, ctx context.Context, codexHome, threadID string) {
	t.Helper()
	command := exec.CommandContext(ctx, realCodexBinary(t), "exec", "resume",
		"--ignore-rules", "--skip-git-repo-check", "--json", threadID, realCodexWorkflowPrompt)
	command.Env = append(os.Environ(), "CODEX_HOME="+codexHome)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("real Codex Workflow resume failed: %v\n%s", err, output)
	}
	verifyCodexWorkflowTranscript(t, output)
	if resumed := codexThreadID(t, output); resumed != threadID {
		t.Fatalf("Codex Workflow resume changed thread identity: got %s want %s", resumed, threadID)
	}
}

func verifyCodexWorkflowTranscript(t *testing.T, output []byte) {
	t.Helper()
	transcript := string(output)
	for _, required := range []string{
		"workflow.list", "workflow.get", "workflow.start", "workflow.state", "workflow.step.begin",
		"workflow.step.submit", "workflow.artifact.list", "workflow.artifact.get", "LAZYMIND_CODEX_WORKFLOW_E2E_OK",
	} {
		if !strings.Contains(transcript, required) {
			if len(transcript) > 64<<10 {
				transcript = transcript[len(transcript)-(64<<10):]
			}
			t.Fatalf("real Codex Workflow transcript did not prove %q was used or observed\n%s", required, transcript)
		}
	}
}

func resumeCodexThreadE2E(t *testing.T, ctx context.Context, codexHome, threadID string) {
	t.Helper()
	codexBinary := realCodexBinary(t)
	prompt := `Continue this exact Codex thread. Call workflow.list from the "lazymind" MCP server exactly once, then reply exactly: LAZYMIND_CODEX_THREAD_RESUME_OK`
	command := exec.CommandContext(ctx, codexBinary, "exec", "resume",
		"--ignore-rules", "--skip-git-repo-check", "--json", threadID, prompt)
	command.Env = append(os.Environ(), "CODEX_HOME="+codexHome)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("real Codex thread resume failed: %v\n%s", err, output)
	}
	transcript := string(output)
	for _, required := range []string{"workflow.list", "LAZYMIND_CODEX_THREAD_RESUME_OK"} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("resumed Codex transcript did not prove %q\n%s", required, transcript)
		}
	}
	if resumed := codexThreadID(t, output); resumed != threadID {
		t.Fatalf("Codex resume changed thread identity: got %s want %s", resumed, threadID)
	}
}

func realCodexBinary(t *testing.T) string {
	t.Helper()
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal("real Codex CLI is required for Codex validation")
	}
	resolved, err := filepath.EvalSymlinks(binary)
	if err == nil && strings.TrimSpace(resolved) != "" {
		return resolved
	}
	return binary
}

func codexThreadID(t *testing.T, output []byte) string {
	t.Helper()
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		var event struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
		}
		if json.Unmarshal(line, &event) == nil && event.Type == "thread.started" && strings.TrimSpace(event.ThreadID) != "" {
			return strings.TrimSpace(event.ThreadID)
		}
	}
	t.Fatalf("real Codex transcript did not contain thread.started: %s", output)
	return ""
}

func verifyCodexConversationBootstrap(t *testing.T, ctx context.Context, serverURL, token, threadID string) string {
	t.Helper()
	var runPage struct {
		Runs []struct {
			ConversationID   string `json:"conversation_id"`
			ProviderThreadID string `json:"provider_thread_id"`
			Action           string `json:"action"`
			Status           string `json:"status"`
		} `json:"runs"`
	}
	realAPI(t, ctx, http.MethodGet, serverURL+"/api/core/external-chat/runs", token, nil, &runPage)
	conversationID := ""
	for _, run := range runPage.Runs {
		if run.ProviderThreadID == threadID && run.Action == "observe" && run.Status == "completed" {
			conversationID = run.ConversationID
			break
		}
	}
	if conversationID == "" {
		t.Fatalf("ordinary LazyMind MCP use did not create a conversation for Codex thread %s", threadID)
	}
	var conversations struct {
		Conversations []struct {
			ConversationID string `json:"conversation_id"`
			DisplayName    string `json:"display_name"`
		} `json:"conversations"`
	}
	realAPI(t, ctx, http.MethodGet, serverURL+"/api/core/conversations?page_size=100", token, nil, &conversations)
	visible := false
	for _, conversation := range conversations.Conversations {
		if conversation.ConversationID == conversationID && strings.HasPrefix(conversation.DisplayName, "Codex · ") {
			visible = true
			break
		}
	}
	if !visible {
		t.Fatalf("ordinary Codex MCP conversation %s is absent from LazyMind conversation list", conversationID)
	}
	var history struct {
		History []struct {
			Execution *struct {
				Invocation struct {
					Tools []string `json:"tools"`
				} `json:"invocation"`
			} `json:"execution"`
		} `json:"history"`
	}
	realAPI(t, ctx, http.MethodGet,
		serverURL+"/api/core/conversations/"+url.PathEscape(conversationID)+":history?page_size=100",
		token, nil, &history)
	if len(history.History) != 1 || history.History[0].Execution == nil ||
		!containsString(history.History[0].Execution.Invocation.Tools, "knowledge.list") {
		t.Fatalf("ordinary Codex MCP activity was not projected into LazyMind history: %+v", history)
	}
	return conversationID
}

func verifyCodexConversationProjection(t *testing.T, ctx context.Context, serverURL, token, threadID, wantConversationID string) {
	t.Helper()
	var runPage struct {
		Runs []struct {
			RunID            string `json:"run_id"`
			ConversationID   string `json:"conversation_id"`
			HistoryID        string `json:"history_id"`
			Provider         string `json:"provider"`
			ProviderThreadID string `json:"provider_thread_id"`
			ProviderTurnID   string `json:"provider_turn_id"`
			Action           string `json:"action"`
			Status           string `json:"status"`
		} `json:"runs"`
	}
	realAPI(t, ctx, http.MethodGet, serverURL+"/api/core/external-chat/runs", token, nil, &runPage)
	conversationID := ""
	matchedRuns := 0
	for _, run := range runPage.Runs {
		if run.Provider != "codex" || run.ProviderThreadID != threadID {
			continue
		}
		matchedRuns++
		if run.Action != "observe" || run.Status != "completed" || run.ProviderTurnID == "" || run.HistoryID == "" {
			t.Fatalf("invalid observed Codex run: %+v", run)
		}
		if conversationID == "" {
			conversationID = run.ConversationID
		} else if run.ConversationID != conversationID {
			t.Fatalf("one Codex thread mapped to multiple LazyMind conversations: %s and %s", conversationID, run.ConversationID)
		}
	}
	if matchedRuns < 3 || conversationID == "" || conversationID != wantConversationID {
		t.Fatalf("Codex thread %s produced %d observed runs: %+v", threadID, matchedRuns, runPage.Runs)
	}

	var history struct {
		ConversationID string `json:"conversation_id"`
		History        []struct {
			ID        string `json:"id"`
			Execution *struct {
				Provider              string `json:"provider"`
				Status                string `json:"status"`
				ArtifactCount         int64  `json:"artifact_count"`
				ArtifactRevisionCount int64  `json:"artifact_revision_count"`
				Invocation            struct {
					Total     int      `json:"total"`
					Succeeded int      `json:"succeeded"`
					Tools     []string `json:"tools"`
				} `json:"invocation"`
				Workflows []struct {
					SessionID string `json:"session_id"`
					Status    string `json:"status"`
				} `json:"workflows"`
			} `json:"execution"`
		} `json:"history"`
	}
	realAPI(t, ctx, http.MethodGet,
		serverURL+"/api/core/conversations/"+url.PathEscape(conversationID)+":history?page_size=100",
		token, nil, &history)
	if history.ConversationID != conversationID || len(history.History) < 2 {
		t.Fatalf("external conversation history is incomplete: %+v", history)
	}
	workflowVisible := false
	for _, item := range history.History {
		if item.Execution == nil || item.Execution.Provider != "codex" || item.Execution.Status != "completed" ||
			item.Execution.Invocation.Total == 0 || item.Execution.Invocation.Succeeded == 0 {
			continue
		}
		if len(item.Execution.Workflows) > 0 && item.Execution.Workflows[0].SessionID != "" &&
			item.Execution.Workflows[0].Status == "completed" && item.Execution.ArtifactCount > 0 &&
			item.Execution.ArtifactRevisionCount > 0 {
			workflowVisible = true
		}
	}
	if !workflowVisible {
		t.Fatalf("Codex Workflow and artifacts were not projected into LazyMind history: %+v", history.History)
	}

	var latest struct {
		Session *struct {
			ID     string `json:"session_id"`
			Status string `json:"status"`
			Slots  []any  `json:"slots"`
		} `json:"session"`
	}
	realAPI(t, ctx, http.MethodGet,
		serverURL+"/api/core/conversations/"+url.PathEscape(conversationID)+"/workflow-sessions:latest",
		token, nil, &latest)
	if latest.Session == nil || latest.Session.Status != "completed" || len(latest.Session.Slots) == 0 {
		t.Fatalf("latest Workflow panel projection is incomplete: %+v", latest.Session)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func deleteKnowledgeFixture(t *testing.T, serverURL, token, knowledgeID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	realAPI(t, ctx, http.MethodDelete, serverURL+"/api/core/datasets/"+knowledgeID, token, nil, nil)
}

func deleteSkillFixture(t *testing.T, serverURL, token, skillID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, endpoint := range []string{"/api/core/skills/" + skillID, "/api/core/skills/" + skillID + ":purge"} {
		request, err := http.NewRequestWithContext(ctx, http.MethodDelete, serverURL+endpoint, nil)
		if err != nil {
			t.Errorf("build skill fixture cleanup: %v", err)
			return
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Errorf("clean skill fixture: %v", err)
			return
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			t.Errorf("clean skill fixture returned HTTP %d", response.StatusCode)
			return
		}
	}
}

func realAPI(t *testing.T, ctx context.Context, method, endpoint, token string, input, output any) {
	t.Helper()
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	realRequest(t, request, output)
}

func realRequest(t *testing.T, request *http.Request, output any) {
	t.Helper()
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("real fixture request: %v", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("real fixture request returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if output == nil {
		return
	}
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Result json.RawMessage `json:"result"`
	}
	payload := responseBody
	if json.Unmarshal(responseBody, &envelope) == nil && len(envelope.Data) > 0 {
		payload = envelope.Data
	} else if len(envelope.Result) > 0 {
		payload = envelope.Result
	}
	if err := json.Unmarshal(payload, output); err != nil {
		t.Fatalf("decode real fixture response: %v", err)
	}
}

func callTool(t *testing.T, ctx context.Context, session *mcp.ClientSession, name string, arguments map[string]any) map[string]any {
	return callToolWithMeta(t, ctx, session, name, arguments, nil)
}

func callToolWithMeta(t *testing.T, ctx context.Context, session *mcp.ClientSession, name string,
	arguments map[string]any, meta mcp.Meta) map[string]any {
	t.Helper()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments, Meta: meta})
	if err != nil {
		t.Fatalf("call real %s through stdio bridge: %v", name, err)
	}
	if result.IsError {
		t.Fatalf("real %s returned a tool error: %#v", name, result.Content)
	}
	body, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal %s structured content: %v", name, err)
	}
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("decode %s structured content: %v", name, err)
	}
	return value
}

func objectItems(t *testing.T, value map[string]any) []map[string]any {
	t.Helper()
	raw, ok := value["items"].([]any)
	if !ok {
		t.Fatalf("result has no items array: %#v", value)
	}
	items := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		object, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("items contains a non-object: %#v", item)
		}
		items = append(items, object)
	}
	return items
}

func objectField(t *testing.T, value map[string]any, name string) map[string]any {
	t.Helper()
	object, ok := value[name].(map[string]any)
	if !ok {
		t.Fatalf("%s is not an object: %#v", name, value[name])
	}
	return object
}

func stringField(t *testing.T, value map[string]any, name string) string {
	t.Helper()
	text, ok := value[name].(string)
	if !ok || strings.TrimSpace(text) == "" {
		t.Fatalf("%s is not a non-empty string: %#v", name, value[name])
	}
	return text
}
