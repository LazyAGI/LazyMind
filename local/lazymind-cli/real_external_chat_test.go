package agentconnector_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestRealExternalAgentChat(t *testing.T) {
	if os.Getenv("LAZYMIND_REAL_EXTERNAL_CHAT_E2E") != "1" {
		t.Skip("set LAZYMIND_REAL_EXTERNAL_CHAT_E2E=1 with `lazymind agent host run` active")
	}
	for _, provider := range realExternalChatProviders(t) {
		t.Run(provider, func(t *testing.T) { testRealExternalAgentChat(t, provider) })
	}
}

func TestRealExternalAgentImageWorkflowArtifactDelivery(t *testing.T) {
	if os.Getenv("LAZYMIND_REAL_EXTERNAL_IMAGE_WORKFLOW_E2E") != "1" {
		t.Skip("set LAZYMIND_REAL_EXTERNAL_IMAGE_WORKFLOW_E2E=1 with the Codex Host active")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	serverURL, token := realCredentials(t)
	conversationID := fmt.Sprintf("ext-image-codex-%x", time.Now().UnixNano())
	turn := runExternalChatTurn(t, ctx, serverURL, token, "codex", conversationID,
		"必须使用 lazymind MCP 完整执行 image-workflow，生成一张小猫在公园里抓蝴蝶的方形高清图片。必须按 step_contract 逐步提交每个中间产物，使用真实图像生成能力生成图片文件并通过 local_path 提交，直到 workflow.state 返回 completed=true；然后调用 workflow.artifact.list，并在最终回答中写出标记 LM_IMAGE_WORKFLOW_E2E_OK 和该列表 value.url 返回的 `[下载原图](...)`。不得输出 /Users、/tmp、file:// 或其他本机路径。", true)
	if !strings.Contains(turn.Message, "LM_IMAGE_WORKFLOW_E2E_OK") ||
		strings.Contains(turn.Message, "/Users/") || strings.Contains(turn.Message, "file://") ||
		!strings.Contains(turn.Message, "/static-files/workflow-artifacts/") {
		t.Fatalf("Codex final answer did not use the managed Workflow artifact: %s", turn.Message)
	}
	// A real image turn can outlive the short access-token window. The running
	// Connector refreshes the installed credentials; reload them before the
	// post-run read and download assertions.
	serverURL, token = realCredentials(t)

	var latest struct {
		Session *struct {
			ID     string `json:"session_id"`
			Status string `json:"status"`
			Slots  []struct {
				SlotID        string          `json:"slot_id"`
				ContentType   string          `json:"content_type"`
				ArtifactValue json.RawMessage `json:"artifact_value"`
			} `json:"slots"`
		} `json:"session"`
	}
	realAPI(t, ctx, http.MethodGet, serverURL+"/api/core/conversations/"+url.PathEscape(conversationID)+"/workflow-sessions:latest", token, nil, &latest)
	if latest.Session == nil || latest.Session.Status != "completed" {
		t.Fatalf("image Workflow is not completed: %#v", latest.Session)
	}
	requiredSlots := map[string]bool{
		"subject_analysis": false, "workflow_routing": false, "material_summary": false,
		"prompt_used": false, "generated_image_output": false,
	}
	signedImageURL := ""
	for _, slot := range latest.Session.Slots {
		if _, required := requiredSlots[slot.SlotID]; required {
			requiredSlots[slot.SlotID] = true
		}
		if slot.SlotID == "generated_image_output" {
			var imageValue map[string]any
			if err := json.Unmarshal(slot.ArtifactValue, &imageValue); err != nil {
				t.Fatalf("decode generated image slot: %v value=%s", err, slot.ArtifactValue)
			}
			signedImageURL, _ = imageValue["url"].(string)
			if !strings.HasPrefix(slot.ContentType, "image/") || signedImageURL == "" || imageValue["path"] != nil {
				t.Fatalf("image slot is not browser-safe: %#v", slot)
			}
		}
	}
	for slot, present := range requiredSlots {
		if !present {
			t.Fatalf("Workflow panel is missing selected slot %s: %#v", slot, latest.Session.Slots)
		}
	}

	var artifacts struct {
		Artifacts []struct {
			ID          string          `json:"artifact_id"`
			Slot        string          `json:"slot"`
			ContentType string          `json:"content_type"`
			Value       json.RawMessage `json:"value"`
		} `json:"artifacts"`
	}
	realAPI(t, ctx, http.MethodGet, serverURL+"/api/core/workflow-sessions/"+url.PathEscape(latest.Session.ID)+"/artifacts", token, nil, &artifacts)
	imageArtifactID := ""
	for _, artifact := range artifacts.Artifacts {
		if artifact.Slot != "generated_image_output" {
			continue
		}
		imageArtifactID = artifact.ID
		var imageValue map[string]any
		if err := json.Unmarshal(artifact.Value, &imageValue); err != nil {
			t.Fatalf("decode listed image artifact: %v value=%s", err, artifact.Value)
		}
		if imageValue["path"] != nil || imageValue["content_base64"] != nil ||
			!strings.HasPrefix(stringField(t, imageValue, "url"), "/static-files/workflow-artifacts/") {
			t.Fatalf("artifact list leaked bytes or a private path: %#v", imageValue)
		}
	}
	if imageArtifactID == "" {
		t.Fatal("artifact list has no generated image")
	}
	inlineValue := realWorkflowArtifactValue(t, ctx, serverURL, token, imageArtifactID)
	encoded, _ := inlineValue["content_base64"].(string)
	imageBytes, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(imageBytes) < 1024 || inlineValue["storage"] != "inline_base64" {
		t.Fatalf("explicit artifact read did not return image bytes: size=%d storage=%v err=%v", len(imageBytes), inlineValue["storage"], err)
	}

	staticEndpoint := signedImageURL
	if strings.HasPrefix(staticEndpoint, "/static-files/") {
		staticEndpoint = serverURL + "/api/core" + staticEndpoint
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		staticEndpoint+map[bool]string{true: "&download=1", false: "?download=1"}[strings.Contains(staticEndpoint, "?")], nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	downloaded, readErr := io.ReadAll(io.LimitReader(response.Body, 24<<20))
	_ = response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "image/") ||
		!bytes.Equal(downloaded, imageBytes) {
		t.Fatalf("browser download mismatch: status=%d type=%q size=%d read=%v", response.StatusCode, response.Header.Get("Content-Type"), len(downloaded), readErr)
	}
	t.Logf("real Codex image Workflow passed: conversation=%s history=%s session=%s artifacts=%d image_bytes=%d",
		conversationID, turn.HistoryID, latest.Session.ID, len(artifacts.Artifacts), len(imageBytes))
}

func realWorkflowArtifactValue(t *testing.T, ctx context.Context, serverURL, token, artifactID string) map[string]any {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		serverURL+"/api/core/workflow-artifacts/"+url.PathEscape(artifactID), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("read real Workflow artifact: status=%d err=%v body=%s", response.StatusCode, err, strings.TrimSpace(string(body)))
	}
	var envelope struct {
		Result struct {
			Value map[string]any `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Result.Value
}

func testRealExternalAgentChat(t *testing.T, provider string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	serverURL, token := realCredentials(t)
	var hostStatus struct {
		Provider   string `json:"provider"`
		Installed  bool   `json:"installed"`
		HostOnline bool   `json:"host_online"`
		Available  bool   `json:"available"`
	}
	realAPI(t, ctx, http.MethodGet, serverURL+"/api/core/external-chat/hosts/"+url.PathEscape(provider)+"/status", token, nil, &hostStatus)
	if !hostStatus.Installed || !hostStatus.HostOnline || !hostStatus.Available {
		t.Fatalf("real %s Agent host is not ready: %#v", provider, hostStatus)
	}
	var catalog struct {
		Executors []struct {
			ID         string `json:"id"`
			Installed  bool   `json:"installed"`
			HostOnline bool   `json:"host_online"`
			Available  bool   `json:"available"`
		} `json:"executors"`
	}
	realAPI(t, ctx, http.MethodGet, serverURL+"/api/core/chat/executors", token, nil, &catalog)
	found := false
	for _, executor := range catalog.Executors {
		if executor.ID == provider {
			found = executor.Installed && executor.HostOnline && executor.Available
		}
	}
	if !found {
		t.Fatalf("real %s Agent is not ready in the Core executor catalog: %#v", provider, catalog.Executors)
	}

	conversationID := fmt.Sprintf("ext-chat-%s-%x", provider, time.Now().UnixNano())
	first := runExternalChatTurn(t, ctx, serverURL, token, provider, conversationID,
		"必须调用 lazymind MCP 的 skill.list，然后回复标记 LM-AGENT-TURN-1，并简要说明工具调用成功。", true)
	if !strings.Contains(first.Message, "LM-AGENT-TURN-1") {
		t.Fatalf("first external Chat turn missed marker: %q", first.Message)
	}
	if strings.Count(first.Message, "LM-AGENT-TURN-1") != 1 {
		t.Fatalf("first external Chat turn duplicated streamed output: %q", first.Message)
	}
	second := runExternalChatTurn(t, ctx, serverURL, token, provider, conversationID,
		"继续当前对话，必须调用 lazymind MCP 的 knowledge.list，然后回复标记 LM-AGENT-TURN-2，并复述上一轮的 LM-AGENT-TURN-1。", false)
	if !strings.Contains(second.Message, "LM-AGENT-TURN-2") || !strings.Contains(second.Message, "LM-AGENT-TURN-1") {
		t.Fatalf("second external Chat turn did not preserve %s context: %q", provider, second.Message)
	}
	if strings.Count(second.Message, "LM-AGENT-TURN-2") != 1 {
		t.Fatalf("second external Chat turn duplicated streamed output: %q", second.Message)
	}
	resumed := resumeExternalChatTurn(t, ctx, serverURL, token, conversationID, second.HistoryID)
	if resumed.HistoryID != second.HistoryID || !strings.Contains(resumed.Message, "LM-AGENT-TURN-2") {
		t.Fatalf("resumed external Chat did not return the persisted turn: %#v", resumed)
	}
	third := runExternalChatTurn(t, ctx, serverURL, token, provider, conversationID,
		`使用 lazymind MCP 完整执行已发布的 test-workflow，而不是解释步骤。必须依次调用 workflow.list、workflow.get、workflow.start，然后循环调用 workflow.state、workflow.step.begin，并严格按 step_contract 为每个 required output 提交内联 JSON，直到 workflow.state 的 completed=true；最后调用 workflow.artifact.list 和 workflow.artifact.get。全部真实调用成功后只回复 LAZYMIND_CHAT_WORKFLOW_E2E_OK。`, false)
	if !strings.Contains(third.Message, "LAZYMIND_CHAT_WORKFLOW_E2E_OK") {
		t.Fatalf("external Chat did not complete the LazyMind Workflow: %q", third.Message)
	}
	if strings.Count(third.Message, "LAZYMIND_CHAT_WORKFLOW_E2E_OK") != 1 {
		t.Fatalf("external Workflow Chat duplicated streamed output: %q", third.Message)
	}
	projection := realHistoryExecutionProjection(
		t, ctx, serverURL, token, conversationID, third.HistoryID,
	)
	if third.Execution == nil || third.Execution.RunID != projection.RunID ||
		projection.Provider != provider || projection.Status != "completed" || !projection.HostOnline ||
		projection.Invocation.Total == 0 || len(projection.Workflows) != 1 ||
		projection.ArtifactCount == 0 || projection.ArtifactRevisionCount == 0 {
		t.Fatalf("external execution projection is incomplete: SSE=%#v history=%#v", third.Execution, projection)
	}

	var detail struct {
		Conversation struct {
			ChatExecutor string `json:"chat_executor"`
		} `json:"conversation"`
	}
	realAPI(t, ctx, http.MethodGet, serverURL+"/api/core/conversations/"+url.PathEscape(conversationID)+":detail", token, nil, &detail)
	if detail.Conversation.ChatExecutor != provider {
		t.Fatalf("conversation executor=%q, want %s", detail.Conversation.ChatExecutor, provider)
	}
	var latestWorkflow struct {
		Session *struct {
			ID     string `json:"session_id"`
			Status string `json:"status"`
			Slots  []any  `json:"slots"`
		} `json:"session"`
	}
	realAPI(t, ctx, http.MethodGet, serverURL+"/api/core/conversations/"+url.PathEscape(conversationID)+"/workflow-sessions:latest", token, nil, &latestWorkflow)
	if latestWorkflow.Session == nil || latestWorkflow.Session.Status != "completed" || len(latestWorkflow.Session.Slots) == 0 {
		t.Fatalf("external Workflow was not attached to the LazyMind conversation with artifacts: %#v", latestWorkflow.Session)
	}

	var runPage struct {
		Runs []struct {
			RunID            string `json:"run_id"`
			Action           string `json:"action"`
			Status           string `json:"status"`
			ProviderThreadID string `json:"provider_thread_id"`
			ClaimCount       int    `json:"claim_count"`
			EventCount       int64  `json:"event_count"`
		} `json:"runs"`
	}
	realAPI(t, ctx, http.MethodGet, serverURL+"/api/core/external-chat/runs?conversation_id="+url.QueryEscape(conversationID), token, nil, &runPage)
	if len(runPage.Runs) != 3 || runPage.Runs[0].Action != "resume" || runPage.Runs[1].Action != "resume" || runPage.Runs[2].Action != "start" ||
		runPage.Runs[0].Status != "completed" || runPage.Runs[1].Status != "completed" || runPage.Runs[2].Status != "completed" ||
		runPage.Runs[0].ProviderThreadID == "" || runPage.Runs[0].ProviderThreadID != runPage.Runs[1].ProviderThreadID ||
		runPage.Runs[0].ProviderThreadID != runPage.Runs[2].ProviderThreadID {
		t.Fatalf("unexpected external run lineage: %#v", runPage.Runs)
	}
	for _, run := range runPage.Runs {
		if run.ClaimCount != 1 || run.EventCount < 3 {
			t.Fatalf("external run was not durably claimed and journaled: %#v", run)
		}
	}
	assertExternalChatCursorResume(t, ctx, serverURL, token, conversationID, second.HistoryID,
		runPage.Runs[1].EventCount-1, runPage.Runs[1].EventCount)
	for _, run := range runPage.Runs {
		var invocations struct {
			Invocations []struct {
				ToolName    string `json:"tool_name"`
				ExternalRef string `json:"external_ref"`
				Status      string `json:"status"`
			} `json:"invocations"`
		}
		realAPI(t, ctx, http.MethodGet, serverURL+"/api/core/agent-invocations?external_ref="+url.QueryEscape(run.RunID), token, nil, &invocations)
		if len(invocations.Invocations) == 0 {
			t.Fatalf("external run %s has no linked MCP invocation", run.RunID)
		}
		for _, invocation := range invocations.Invocations {
			if invocation.ExternalRef != run.RunID || invocation.Status != "succeeded" {
				t.Fatalf("invalid linked MCP invocation: %#v", invocation)
			}
		}
	}
	t.Logf("real external Chat passed: conversation=%s histories=%s,%s,%s workflow=%s thread=%s",
		conversationID, first.HistoryID, second.HistoryID, third.HistoryID,
		latestWorkflow.Session.ID, runPage.Runs[0].ProviderThreadID)
}

func TestRealCodexHostUsesCompleteDistribution(t *testing.T) {
	if os.Getenv("LAZYMIND_REAL_CODEX_HOST_E2E") != "1" {
		t.Skip("set LAZYMIND_REAL_CODEX_HOST_E2E=1 with the Assistant Bridge active")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	serverURL, token := realCredentials(t)
	conversationID := fmt.Sprintf("codex-host-%x", time.Now().UnixNano())
	result := runExternalChatTurn(t, ctx, serverURL, token, "codex", conversationID,
		"必须调用 lazymind MCP 的 workflow.list 一次；真实返回后只回复 CODEX_HOST_DISTRIBUTION_OK。", true)
	if !strings.Contains(result.Message, "CODEX_HOST_DISTRIBUTION_OK") ||
		strings.Count(result.Message, "CODEX_HOST_DISTRIBUTION_OK") != 1 {
		t.Fatalf("Codex Host result=%q", result.Message)
	}
	projection := realHistoryExecutionProjection(
		t, ctx, serverURL, token, conversationID, result.HistoryID,
	)
	if projection.Provider != "codex" || projection.Status != "completed" ||
		projection.Invocation.Total == 0 {
		t.Fatalf("Codex Host execution=%#v", projection)
	}
}

func TestRealLazyBoundExternalSessionResume(t *testing.T) {
	if os.Getenv("LAZYMIND_REAL_BOUND_SESSION_E2E") != "1" {
		t.Skip("set LAZYMIND_REAL_BOUND_SESSION_E2E=1 with a bound provider session")
	}
	provider := strings.TrimSpace(os.Getenv("LAZYMIND_REAL_BOUND_SESSION_PROVIDER"))
	conversationID := strings.TrimSpace(os.Getenv("LAZYMIND_REAL_BOUND_SESSION_CONVERSATION"))
	if provider == "" || conversationID == "" {
		t.Fatal("LAZYMIND_REAL_BOUND_SESSION_PROVIDER and LAZYMIND_REAL_BOUND_SESSION_CONVERSATION are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	serverURL, token := realCredentials(t)
	result := runExternalChatTurn(t, ctx, serverURL, token, provider, conversationID,
		"调用 lazymind MCP 的 workflow.list 一次；真实返回后回复 LAZY_BOUND_RESUME_OK。", false)
	if !strings.Contains(result.Message, "LAZY_BOUND_RESUME_OK") {
		t.Fatalf("lazy-bound %s result=%q", provider, result.Message)
	}
	projection := realHistoryExecutionProjection(t, ctx, serverURL, token, conversationID, result.HistoryID)
	if projection.Provider != provider || projection.Status != "completed" || projection.Invocation.Total == 0 {
		t.Fatalf("lazy-bound execution=%#v", projection)
	}
}

func TestRealExternalAgentChatStop(t *testing.T) {
	if os.Getenv("LAZYMIND_REAL_EXTERNAL_CHAT_E2E") != "1" {
		t.Skip("set LAZYMIND_REAL_EXTERNAL_CHAT_E2E=1 with `lazymind agent host run` active")
	}
	for _, provider := range realExternalChatProviders(t) {
		t.Run(provider, func(t *testing.T) { testRealExternalAgentChatStop(t, provider) })
	}
}

func TestRealExternalAgentChatCoreRestart(t *testing.T) {
	if os.Getenv("LAZYMIND_REAL_EXTERNAL_CHAT_RESTART_E2E") != "1" {
		t.Skip("set LAZYMIND_REAL_EXTERNAL_CHAT_RESTART_E2E=1 to restart Core during a real Codex turn")
	}
	repo := strings.TrimSpace(os.Getenv("LAZYMIND_REAL_EXTERNAL_CHAT_REPO"))
	if repo == "" {
		t.Fatal("LAZYMIND_REAL_EXTERNAL_CHAT_REPO is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	serverURL, token := realCredentials(t)
	conversationID := fmt.Sprintf("ext-restart-codex-%x", time.Now().UnixNano())
	payload, err := json.Marshal(map[string]any{
		"conversation_id": conversationID,
		"conversation":    map[string]any{"display_name": "External Codex Core restart E2E"},
		"stream":          true,
		"input": []map[string]any{{
			"input_type": "text",
			"text":       "先使用终端执行 sleep 20，再调用 lazymind MCP 的 skill.list，最后只回复 LM-CORE-RESTART-OK。",
		}},
		"initial_conversation_settings": map[string]any{
			"chat_executor": "codex", "enable_workflow": true,
			"workflow_mode": "dynamic", "enable_subagent": true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	initial := make(chan externalChatStreamOutcome, 1)
	go func() {
		result, finishReason, streamErr := externalChatStream(
			ctx, serverURL+"/api/core/conversations:chat", token, payload,
		)
		initial <- externalChatStreamOutcome{result: result, finishReason: finishReason, err: streamErr}
	}()

	type runRecord struct {
		RunID      string `json:"run_id"`
		HistoryID  string `json:"history_id"`
		Status     string `json:"status"`
		ClaimCount int    `json:"claim_count"`
		EventCount int64  `json:"event_count"`
	}
	var page struct {
		Runs []runRecord `json:"runs"`
	}
	deadline := time.Now().Add(45 * time.Second)
	for len(page.Runs) == 0 || page.Runs[0].Status != "running" || page.Runs[0].ClaimCount != 1 {
		select {
		case outcome := <-initial:
			t.Fatalf("external Chat ended before Core restart: result=%#v finish=%s err=%v", outcome.result, outcome.finishReason, outcome.err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("real Codex turn was not durably claimed before Core restart")
		}
		page.Runs = nil
		realAPI(t, ctx, http.MethodGet, serverURL+"/api/core/external-chat/runs?conversation_id="+url.QueryEscape(conversationID), token, nil, &page)
		if len(page.Runs) == 0 || page.Runs[0].Status != "running" || page.Runs[0].ClaimCount != 1 {
			time.Sleep(200 * time.Millisecond)
		}
	}

	restart := exec.CommandContext(ctx, "docker", "compose", "restart", "core")
	restart.Dir = repo
	if output, restartErr := restart.CombinedOutput(); restartErr != nil {
		t.Fatalf("restart real Core: %v: %s", restartErr, strings.TrimSpace(string(output)))
	}
	waitForRealCore(t, ctx, serverURL, token)
	resumePayload, err := json.Marshal(map[string]any{"conversation_id": conversationID})
	if err != nil {
		t.Fatal(err)
	}
	resumed := performExternalChatStream(t, ctx, serverURL+"/api/core/conversations:resumeChat", token, resumePayload)
	if strings.Count(resumed.Message, "LM-CORE-RESTART-OK") != 1 {
		t.Fatalf("Core restart resume lost or duplicated the provider result: %q", resumed.Message)
	}
	page.Runs = nil
	realAPI(t, ctx, http.MethodGet, serverURL+"/api/core/external-chat/runs?conversation_id="+url.QueryEscape(conversationID), token, nil, &page)
	if len(page.Runs) != 1 || page.Runs[0].Status != "completed" || page.Runs[0].ClaimCount != 1 ||
		page.Runs[0].EventCount < 3 || page.Runs[0].HistoryID != resumed.HistoryID {
		t.Fatalf("Core restart changed durable run identity or delivery: %#v", page.Runs)
	}
	t.Logf("real Core restart recovery passed: conversation=%s run=%s history=%s events=%d",
		conversationID, page.Runs[0].RunID, resumed.HistoryID, page.Runs[0].EventCount)
}

func TestRealExternalAgentChatHostRestart(t *testing.T) {
	if os.Getenv("LAZYMIND_REAL_EXTERNAL_CHAT_HOST_RESTART_E2E") != "1" {
		t.Skip("set LAZYMIND_REAL_EXTERNAL_CHAT_HOST_RESTART_E2E=1 to replace the Codex Host during a real turn")
	}
	connector := strings.TrimSpace(os.Getenv("LAZYMIND_REAL_CONNECTOR_BIN"))
	if connector == "" {
		t.Fatal("LAZYMIND_REAL_CONNECTOR_BIN is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	serverURL, token := realCredentials(t)
	hostA := startRealCodexHost(t, ctx, connector)
	t.Cleanup(func() { hostA.stop() })

	conversationID := fmt.Sprintf("host-restart-%x", time.Now().UnixNano())
	t.Cleanup(func() { stopRealExternalChatFixture(serverURL, token, conversationID) })
	payload, err := json.Marshal(map[string]any{
		"conversation_id": conversationID,
		"conversation":    map[string]any{"display_name": "External Codex Host restart E2E"},
		"stream":          true,
		"input": []map[string]any{{
			"input_type": "text",
			"text":       `使用 lazymind MCP 完整执行已发布的 test-workflow。必须依次调用 workflow.list、workflow.get、workflow.start，然后循环调用 workflow.state、workflow.step.begin，并严格按 step_contract 提交每个 required output，直到 completed=true；随后调用 workflow.artifact.list 和 workflow.artifact.get。确认 Workflow 和产物完成后，必须使用终端执行 sleep 30，最后只回复 LM-HOST-RECOVERY-OK。`,
		}},
		"initial_conversation_settings": map[string]any{
			"chat_executor": "codex", "enable_workflow": true,
			"workflow_mode": "dynamic", "enable_subagent": true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	stream := make(chan externalChatStreamOutcome, 1)
	go func() {
		result, finishReason, streamErr := externalChatStream(
			ctx, serverURL+"/api/core/conversations:chat", token, payload,
		)
		stream <- externalChatStreamOutcome{result: result, finishReason: finishReason, err: streamErr}
	}()

	type runRecord struct {
		RunID            string `json:"run_id"`
		HistoryID        string `json:"history_id"`
		Status           string `json:"status"`
		HostID           string `json:"host_id"`
		ProviderThreadID string `json:"provider_thread_id"`
		ClaimCount       int    `json:"claim_count"`
		EventCount       int64  `json:"event_count"`
	}
	var runPage struct {
		Runs []runRecord `json:"runs"`
	}
	var workflowBefore struct {
		Session *struct {
			ID     string `json:"session_id"`
			Status string `json:"status"`
			Slots  []any  `json:"slots"`
		} `json:"session"`
	}
	deadline := time.Now().Add(5 * time.Minute)
	for {
		select {
		case outcome := <-stream:
			t.Fatalf("Codex turn ended before Host crash: result=%#v finish=%s err=%v", outcome.result, outcome.finishReason, outcome.err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("real Workflow did not reach a durable completed state before Host crash")
		}
		runPage.Runs = nil
		realAPI(t, ctx, http.MethodGet, serverURL+"/api/core/external-chat/runs?conversation_id="+url.QueryEscape(conversationID), token, nil, &runPage)
		workflowBefore.Session = nil
		realAPI(t, ctx, http.MethodGet, serverURL+"/api/core/conversations/"+url.PathEscape(conversationID)+"/workflow-sessions:latest", token, nil, &workflowBefore)
		if len(runPage.Runs) == 1 && runPage.Runs[0].Status == "running" && runPage.Runs[0].ClaimCount == 1 &&
			runPage.Runs[0].HostID == hostA.id && runPage.Runs[0].ProviderThreadID != "" &&
			workflowBefore.Session != nil && workflowBefore.Session.Status == "completed" && len(workflowBefore.Session.Slots) > 0 {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	initialRun := runPage.Runs[0]
	artifactsBefore := realWorkflowArtifacts(t, ctx, serverURL, token, workflowBefore.Session.ID)
	if len(artifactsBefore) == 0 {
		t.Fatal("real Workflow completed without durable artifacts")
	}

	hostA.kill(t)
	hostB := startRealCodexHost(t, ctx, connector)
	t.Cleanup(func() { hostB.stop() })
	select {
	case outcome := <-stream:
		if outcome.err != nil || outcome.finishReason != "FINISH_REASON_STOP" ||
			strings.Count(outcome.result.Message, "LM-HOST-RECOVERY-OK") != 1 || outcome.result.Execution == nil {
			t.Fatalf("recovered Host returned invalid output: result=%#v finish=%s err=%v", outcome.result, outcome.finishReason, outcome.err)
		}
		if outcome.result.HistoryID != initialRun.HistoryID {
			t.Fatalf("Host recovery changed history: before=%s after=%s", initialRun.HistoryID, outcome.result.HistoryID)
		}
	case <-time.After(5 * time.Minute):
		t.Fatal("replacement Host did not complete the reclaimed Codex turn")
	}

	runPage.Runs = nil
	realAPI(t, ctx, http.MethodGet, serverURL+"/api/core/external-chat/runs?conversation_id="+url.QueryEscape(conversationID), token, nil, &runPage)
	if len(runPage.Runs) != 1 || runPage.Runs[0].RunID != initialRun.RunID || runPage.Runs[0].HistoryID != initialRun.HistoryID ||
		runPage.Runs[0].Status != "completed" || runPage.Runs[0].ClaimCount != 2 || runPage.Runs[0].HostID != hostB.id ||
		runPage.Runs[0].ProviderThreadID != initialRun.ProviderThreadID || runPage.Runs[0].EventCount < 5 {
		t.Fatalf("Host recovery changed durable run identity or lineage: %#v", runPage.Runs)
	}
	var sessions struct {
		Sessions []struct {
			ID string `json:"session_id"`
		} `json:"sessions"`
	}
	realAPI(t, ctx, http.MethodGet, serverURL+"/api/core/conversations/"+url.PathEscape(conversationID)+"/workflow-sessions", token, nil, &sessions)
	if len(sessions.Sessions) != 1 || sessions.Sessions[0].ID != workflowBefore.Session.ID {
		t.Fatalf("Host recovery duplicated the Workflow session: %#v", sessions.Sessions)
	}
	artifactsAfter := realWorkflowArtifacts(t, ctx, serverURL, token, workflowBefore.Session.ID)
	if strings.Join(artifactsAfter, "\n") != strings.Join(artifactsBefore, "\n") {
		t.Fatalf("Host recovery changed artifact identities or revisions: before=%v after=%v", artifactsBefore, artifactsAfter)
	}
	var invocations struct {
		Invocations []struct {
			Status string `json:"status"`
		} `json:"invocations"`
	}
	realAPI(t, ctx, http.MethodGet, serverURL+"/api/core/agent-invocations?external_ref="+url.QueryEscape(initialRun.RunID), token, nil, &invocations)
	if len(invocations.Invocations) == 0 {
		t.Fatal("recovered Codex run has no MCP invocation audit")
	}
	for _, invocation := range invocations.Invocations {
		if invocation.Status == "running" {
			t.Fatalf("Host recovery left a running MCP invocation: %#v", invocations.Invocations)
		}
	}
	projection := realHistoryExecutionProjection(
		t, ctx, serverURL, token, conversationID, initialRun.HistoryID,
	)
	if projection.RunID != initialRun.RunID || projection.Status != "completed" ||
		projection.ClaimCount != 2 || projection.RecoveryCount != 1 || !projection.HostOnline ||
		len(projection.Workflows) != 1 || projection.ArtifactRevisionCount == 0 {
		t.Fatalf("recovered execution projection lost authoritative state: %#v", projection)
	}
	t.Logf("real Host recovery passed: run=%s history=%s thread=%s hosts=%s→%s workflow=%s artifacts=%d events=%d",
		initialRun.RunID, initialRun.HistoryID, initialRun.ProviderThreadID, hostA.id, hostB.id,
		workflowBefore.Session.ID, len(artifactsAfter), runPage.Runs[0].EventCount)
}

type realCodexHost struct {
	command *exec.Cmd
	done    chan error
	id      string
}

func startRealCodexHost(t *testing.T, ctx context.Context, connector string) *realCodexHost {
	t.Helper()
	command := exec.CommandContext(ctx, connector, "agent", "host", "run", "--provider", "codex")
	command.Env = os.Environ()
	command.Stdout = io.Discard
	stderr, err := command.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatalf("start real Codex Host: %v", err)
	}
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			const marker = "LazyMind external Agent host ready: codex/"
			if strings.HasPrefix(line, marker) {
				select {
				case ready <- strings.TrimPrefix(line, marker):
				default:
				}
			}
		}
	}()
	host := &realCodexHost{command: command, done: make(chan error, 1)}
	go func() { host.done <- command.Wait() }()
	select {
	case host.id = <-ready:
		if host.id == "" {
			t.Fatal("real Codex Host reported an empty identity")
		}
		return host
	case err := <-host.done:
		host.command = nil
		t.Fatalf("real Codex Host exited before ready: %v", err)
	case <-time.After(30 * time.Second):
		host.kill(t)
		t.Fatal("real Codex Host did not become ready")
	}
	return nil
}

func (h *realCodexHost) kill(t *testing.T) {
	t.Helper()
	if h == nil || h.command == nil || h.command.Process == nil {
		return
	}
	if err := h.command.Process.Kill(); err != nil {
		t.Fatalf("kill real Codex Host: %v", err)
	}
	select {
	case <-h.done:
	case <-time.After(10 * time.Second):
		t.Fatal("killed real Codex Host did not exit")
	}
	h.command = nil
}

func (h *realCodexHost) stop() {
	if h == nil || h.command == nil || h.command.Process == nil {
		return
	}
	_ = h.command.Process.Signal(os.Interrupt)
	select {
	case <-h.done:
	case <-time.After(10 * time.Second):
		_ = h.command.Process.Kill()
		<-h.done
	}
	h.command = nil
}

func realWorkflowArtifacts(t *testing.T, ctx context.Context, serverURL, token, sessionID string) []string {
	t.Helper()
	var page struct {
		Artifacts []struct {
			ID       string `json:"artifact_id"`
			Revision int    `json:"revision"`
		} `json:"artifacts"`
	}
	realAPI(t, ctx, http.MethodGet, serverURL+"/api/core/workflow-sessions/"+url.PathEscape(sessionID)+"/artifacts", token, nil, &page)
	values := make([]string, 0, len(page.Artifacts))
	for _, artifact := range page.Artifacts {
		values = append(values, fmt.Sprintf("%s@%d", artifact.ID, artifact.Revision))
	}
	sort.Strings(values)
	return values
}

func stopRealExternalChatFixture(serverURL, token, conversationID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	payload, err := json.Marshal(map[string]string{"conversation_id": conversationID})
	if err != nil {
		return
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		serverURL+"/api/core/conversations:stopChatGeneration", bytes.NewReader(payload))
	if err != nil {
		return
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
}

func waitForRealCore(t *testing.T, ctx context.Context, serverURL, token string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet,
			serverURL+"/api/core/external-chat/hosts/codex/status", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatal("real Core did not become ready within 60 seconds after restart")
}

func testRealExternalAgentChatStop(t *testing.T, provider string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	serverURL, token := realCredentials(t)
	conversationID := fmt.Sprintf("ext-stop-%s-%x", provider, time.Now().UnixNano())
	payload := map[string]any{
		"conversation_id": conversationID,
		"conversation":    map[string]any{"display_name": "External " + provider + " stop E2E"},
		"stream":          true,
		"input": []map[string]any{{
			"input_type": "text",
			"text":       "请先使用终端执行 sleep 30，等待结束后再回复 LM-SHOULD-NOT-COMPLETE。",
		}},
		"initial_conversation_settings": map[string]any{
			"chat_executor": provider, "enable_workflow": true,
			"workflow_mode": "dynamic", "enable_subagent": true,
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan externalChatStreamOutcome, 1)
	go func() {
		result, finishReason, streamErr := externalChatStream(
			ctx, serverURL+"/api/core/conversations:chat", token, encoded,
		)
		finished <- externalChatStreamOutcome{result: result, finishReason: finishReason, err: streamErr}
	}()

	var runPage struct {
		Runs []struct {
			RunID  string `json:"run_id"`
			Status string `json:"status"`
		} `json:"runs"`
	}
	deadline := time.Now().Add(30 * time.Second)
	for len(runPage.Runs) == 0 || runPage.Runs[0].Status != "running" {
		select {
		case outcome := <-finished:
			t.Fatalf("external Chat ended before it could be stopped: result=%#v finish=%s err=%v", outcome.result, outcome.finishReason, outcome.err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("external Chat run was not claimed within 30 seconds")
		}
		runPage.Runs = nil
		realAPI(t, ctx, http.MethodGet, serverURL+"/api/core/external-chat/runs?conversation_id="+url.QueryEscape(conversationID), token, nil, &runPage)
		if len(runPage.Runs) == 0 || runPage.Runs[0].Status != "running" {
			time.Sleep(200 * time.Millisecond)
		}
	}
	realAPI(t, ctx, http.MethodPost, serverURL+"/api/core/conversations:stopChatGeneration", token,
		map[string]any{"conversation_id": conversationID}, nil)

	select {
	case outcome := <-finished:
		if outcome.err != nil || outcome.finishReason != "FINISH_REASON_STOP" {
			t.Fatalf("stopped external Chat returned an invalid stream: result=%#v finish=%s err=%v", outcome.result, outcome.finishReason, outcome.err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("stopped external Chat stream did not terminate")
	}

	runPage.Runs = nil
	realAPI(t, ctx, http.MethodGet, serverURL+"/api/core/external-chat/runs?conversation_id="+url.QueryEscape(conversationID), token, nil, &runPage)
	if len(runPage.Runs) != 1 || runPage.Runs[0].Status != "stopped" {
		t.Fatalf("external Chat run was not persisted as stopped: %#v", runPage.Runs)
	}
	t.Logf("real external Chat stop passed: conversation=%s run=%s", conversationID, runPage.Runs[0].RunID)
}

type externalChatTurnResult struct {
	HistoryID     string
	Message       string
	EventSequence int64
	Execution     *externalExecutionProjection
}

type externalExecutionProjection struct {
	RunID                 string `json:"run_id"`
	Provider              string `json:"provider"`
	Status                string `json:"status"`
	HostOnline            bool   `json:"host_online"`
	ClaimCount            int    `json:"claim_count"`
	RecoveryCount         int    `json:"recovery_count"`
	ArtifactCount         int64  `json:"artifact_count"`
	ArtifactRevisionCount int64  `json:"artifact_revision_count"`
	Invocation            struct {
		Total int      `json:"total"`
		Tools []string `json:"tools"`
	} `json:"invocation"`
	Workflows []struct {
		SessionID  string `json:"session_id"`
		WorkflowID string `json:"workflow_id"`
		Status     string `json:"status"`
	} `json:"workflows"`
}

type externalChatStreamOutcome struct {
	result       externalChatTurnResult
	finishReason string
	err          error
}

func assertExternalChatCursorResume(
	t *testing.T,
	ctx context.Context,
	serverURL, token, conversationID, historyID string,
	after, wantSequence int64,
) {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"conversation_id": conversationID, "history_id": historyID, "after_sequence": after,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, finishReason, err := externalChatStream(
		ctx, serverURL+"/api/core/conversations:resumeChat", token, encoded,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.HistoryID != historyID || result.Message != "" || result.EventSequence != wantSequence || finishReason != "FINISH_REASON_STOP" {
		t.Fatalf("cursor resume replayed acknowledged output or lost its terminal cursor: result=%#v finish=%s", result, finishReason)
	}
}

func realHistoryExecutionProjection(
	t *testing.T,
	ctx context.Context,
	serverURL, token, conversationID, historyID string,
) externalExecutionProjection {
	t.Helper()
	var page struct {
		History []struct {
			ID        string                       `json:"id"`
			Execution *externalExecutionProjection `json:"execution"`
		} `json:"history"`
	}
	realAPI(
		t, ctx, http.MethodGet,
		serverURL+"/api/core/conversations/"+url.PathEscape(conversationID)+":history?page_size=100",
		token, nil, &page,
	)
	for _, item := range page.History {
		if item.ID == historyID && item.Execution != nil {
			return *item.Execution
		}
	}
	t.Fatalf("history %s has no external execution projection: %#v", historyID, page.History)
	return externalExecutionProjection{}
}

func runExternalChatTurn(t *testing.T, ctx context.Context, serverURL, token, provider, conversationID, prompt string, initial bool) externalChatTurnResult {
	t.Helper()
	payload := map[string]any{
		"conversation_id": conversationID,
		"conversation":    map[string]any{"display_name": "External " + provider + " real-service E2E"},
		"stream":          true,
		"input":           []map[string]any{{"input_type": "text", "text": prompt}},
	}
	if initial {
		payload["initial_conversation_settings"] = map[string]any{
			"chat_executor": provider, "enable_workflow": true,
			"workflow_mode": "dynamic", "enable_subagent": true,
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return performExternalChatStream(t, ctx, serverURL+"/api/core/conversations:chat", token, encoded)
}

func realExternalChatProviders(t *testing.T) []string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv("LAZYMIND_REAL_EXTERNAL_CHAT_PROVIDERS"))
	if value == "" {
		value = "codex,cursor,workbuddy"
	}
	allowed := map[string]bool{"codex": true, "cursor": true, "workbuddy": true}
	seen := map[string]bool{}
	providers := make([]string, 0, 3)
	for _, provider := range strings.Split(value, ",") {
		provider = strings.ToLower(strings.TrimSpace(provider))
		if !allowed[provider] {
			t.Fatalf("unsupported real external Chat provider %q", provider)
		}
		if !seen[provider] {
			seen[provider] = true
			providers = append(providers, provider)
		}
	}
	return providers
}

func resumeExternalChatTurn(t *testing.T, ctx context.Context, serverURL, token, conversationID, historyID string) externalChatTurnResult {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"conversation_id": conversationID, "history_id": historyID})
	if err != nil {
		t.Fatal(err)
	}
	return performExternalChatStream(t, ctx, serverURL+"/api/core/conversations:resumeChat", token, encoded)
}

func performExternalChatStream(t *testing.T, ctx context.Context, endpoint, token string, encoded []byte) externalChatTurnResult {
	t.Helper()
	result, finishReason, err := externalChatStream(ctx, endpoint, token, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if finishReason == "FINISH_REASON_UNKNOWN" {
		t.Fatalf("external Chat failed after partial response: %q", result.Message)
	}
	if result.HistoryID == "" || strings.TrimSpace(result.Message) == "" {
		t.Fatalf("external Chat returned no persisted result: %#v", result)
	}
	return result
}

func externalChatStream(ctx context.Context, endpoint, token string, encoded []byte) (externalChatTurnResult, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return externalChatTurnResult{}, "", err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return externalChatTurnResult{}, "", fmt.Errorf("real external Chat stream request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return externalChatTurnResult{}, "", fmt.Errorf("real external Chat stream HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	result := externalChatTurnResult{}
	finishReason := ""
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var envelope struct {
			Result struct {
				HistoryID             string                       `json:"history_id"`
				Message               string                       `json:"message"`
				Delta                 string                       `json:"delta"`
				FinishReason          string                       `json:"finish_reason"`
				ExternalEventSequence int64                        `json:"external_event_sequence"`
				Execution             *externalExecutionProjection `json:"execution"`
			} `json:"result"`
		}
		if json.Unmarshal([]byte(data), &envelope) != nil {
			continue
		}
		if envelope.Result.HistoryID != "" {
			result.HistoryID = envelope.Result.HistoryID
		}
		if envelope.Result.Message != "" {
			result.Message = envelope.Result.Message
		} else if envelope.Result.Delta != "" {
			result.Message += envelope.Result.Delta
		}
		if envelope.Result.FinishReason != "" {
			finishReason = envelope.Result.FinishReason
		}
		if envelope.Result.ExternalEventSequence > result.EventSequence {
			result.EventSequence = envelope.Result.ExternalEventSequence
		}
		if envelope.Result.Execution != nil {
			result.Execution = envelope.Result.Execution
		}
	}
	if err := scanner.Err(); err != nil {
		return result, finishReason, fmt.Errorf("read external Chat SSE: %w", err)
	}
	return result, finishReason, nil
}
