// chat text /api/chat text /api/chat_stream text，
package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"lazymind/core/modelconfig"
)

const (
	chatPath       = "/api/chat"
	streamChatPath = "/api/chat/stream"

	defaultDialTimeout = 10 * time.Second
	// defaultTotalTimeout bounds the whole upstream stream. auto-mode SubAgents block the
	// main SSE (with heartbeats) for long periods, so this must comfortably exceed the
	// longest expected SubAgent runtime. Override via LAZYMIND_CHAT_UPSTREAM_TIMEOUT_SEC.
	defaultTotalTimeout = 2 * time.Hour
	defaultTTFB         = 3 * time.Minute
)

func upstreamTotalTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv("LAZYMIND_CHAT_UPSTREAM_TIMEOUT_SEC")); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return defaultTotalTimeout
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type DatasetFilters struct {
	Subject    []string `json:"subject,omitempty"`
	DatasetIDs []string `json:"kb_id,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Creators   []string `json:"creator,omitempty"`
}

type LazyChatRequest struct {
	Message         ChatMessageOptions         `json:"message"`
	Conversation    ChatConversationOptions    `json:"conversation"`
	Retrieval       ChatRetrievalOptions       `json:"retrieval,omitempty"`
	Runtime         ChatRuntimeOptions         `json:"runtime,omitempty"`
	Personalization ChatPersonalizationOptions `json:"personalization,omitempty"`
	Agent           ChatAgentOptions           `json:"agent,omitempty"`
	Plugin          ChatPluginOptions          `json:"plugin,omitempty"`
}

type ChatMessageOptions struct {
	Query          string              `json:"query"`
	History        []ChatMessage       `json:"history,omitempty"`
	Files          map[string][]string `json:"files,omitempty"`
	CurrentTurnSeq int                 `json:"current_turn_seq,omitempty"`
}

type ChatConversationOptions struct {
	SessionID      string `json:"session_id"`
	ConversationID string `json:"conversation_id,omitempty"`
	UserID         string `json:"user_id"`
	Mode           string `json:"mode,omitempty"`
}

type ChatRetrievalOptions struct {
	Filters        *DatasetFilters `json:"filters,omitempty"`
	Databases      []any           `json:"databases,omitempty"`
	Dataset        string          `json:"dataset,omitempty"`
	LocalFSSources []any           `json:"local_fs_sources,omitempty"`
}

type ChatRuntimeOptions struct {
	Debug              bool           `json:"debug,omitempty"`
	Reasoning          bool           `json:"reasoning"`
	Priority           *int           `json:"priority,omitempty"`
	Trace              bool           `json:"trace,omitempty"`
	EnvironmentContext map[string]any `json:"environment_context,omitempty"`
	LLMConfig          map[string]any `json:"llm_config,omitempty"`
	ToolConfig         map[string]any `json:"tool_config,omitempty"`
	MCPConfig          []any          `json:"mcp_config,omitempty"`
}

type ChatPersonalizationOptions struct {
	Memory         string `json:"memory,omitempty"`
	UserPreference string `json:"user_preference,omitempty"`
	UseMemory      bool   `json:"use_memory"`
}

type ChatAgentOptions struct {
	DisabledTools   []string `json:"disabled_tools,omitempty"`
	AvailableSkills []string `json:"available_skills,omitempty"`
	HasSubagents    bool     `json:"has_subagents"`
	EnableSubagent  *bool    `json:"enable_subagent,omitempty"`
}

type ChatPluginOptions struct {
	EnablePlugin  *bool          `json:"enable_plugin,omitempty"`
	PluginContext map[string]any `json:"plugin_context,omitempty"`
	AskResponse   map[string]any `json:"ask_response,omitempty"`
}

// LazyChatData text data text。
type LazyChatData struct {
	Text          string              `json:"text"`
	Sources       []any               `json:"sources"`
	Status        string              `json:"status"`
	ReasoningText string              `json:"think"`
	TaskCreated   *TaskCreatedEvent   `json:"task_created,omitempty"`
	AskPending    *AskPendingEvent    `json:"ask_pending,omitempty"`
	IntentUpdated *IntentUpdatedEvent `json:"intent_updated,omitempty"`
	Heartbeat     bool                `json:"heartbeat,omitempty"`
	ToolCallTurns int64               `json:"tool_call_turns"`
}

// TaskCreatedEvent is emitted by create_subagent (via translator) on the main SSE.
// seq_in_conversation is NOT included; Go allocates it when creating the record.
type TaskCreatedEvent struct {
	TaskID             string         `json:"task_id"`
	Title              string         `json:"title"`
	AgentType          string         `json:"agent_type"`
	Mode               string         `json:"mode"`
	Objective          string         `json:"objective"`
	Params             map[string]any `json:"params,omitempty"`
	InputArtifactKeys  []string       `json:"input_artifact_keys"`
	OutputArtifactKeys []string       `json:"output_artifact_keys"`
	Tools              []string       `json:"tools,omitempty"`
	Resume             bool           `json:"resume,omitempty"`
}

// AskPendingEvent is emitted by ask_user (via _write_agent_data) on the main SSE stream.
// The frontend renders a clarification UI and submits the user's reply as the next chat turn.
type AskPendingEvent struct {
	AskID    string   `json:"ask_id"`
	Question string   `json:"question"`
	Choices  []string `json:"choices,omitempty"`
}

// IntentUpdatedEvent is emitted by update_intent (via _write_agent_data) on the main SSE stream.
// Go writes the intent to DB and pushes an intent_updated convEvent so the frontend refreshes
// the session immediately without requiring a manual page reload.
type IntentUpdatedEvent struct {
	SessionID string `json:"session_id"`
	Scope     string `json:"scope"` // "session" | "step"
	Content   string `json:"content"`
	StepID    string `json:"step_id,omitempty"`
}

// LazyChatResponse text /api/chat textResponse。
type LazyChatResponse struct {
	Code int          `json:"code"`
	Msg  string       `json:"msg"`
	Data LazyChatData `json:"data"`
	Cost float64      `json:"cost"`
}

// LazyStreamData text /api/chat_stream text。
type LazyStreamData struct {
	RawText string
	Resp    *LazyChatResponse
}

// ChatService text（/api/chat text /api/chat_stream）。
type ChatService struct {
	chatURL       string
	streamChatURL string
	client        *http.Client
}

// NewChatServiceWithEndpoint Createtext endpoint text ChatService，endpoint text http://host:port。
func NewChatServiceWithEndpoint(endpoint string) *ChatService {
	endpoint = strings.TrimRight(endpoint, "/")
	if endpoint == "" {
		panic("invalid chat endpoint")
	}
	dialTimeout := defaultDialTimeout
	totalTimeout := upstreamTotalTimeout()
	ttfb := defaultTTFB

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   dialTimeout,
				KeepAlive: 5 * time.Minute,
			}).DialContext,
			ForceAttemptHTTP2:     false,
			MaxIdleConns:          10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ResponseHeaderTimeout: ttfb,
		},
		Timeout: totalTimeout,
	}
	return &ChatService{
		chatURL:       endpoint + chatPath,
		streamChatURL: endpoint + streamChatPath,
		client:        client,
	}
}

// Chat text /api/chat，Gettext。
func (c *ChatService) Chat(ctx context.Context, req *LazyChatRequest) (*LazyChatResponse, error) {
	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	fmt.Printf(
		"[Core] [CHAT_UPSTREAM_REQUEST] [stream=false] [url=%s] [session_id=%s] [user_id=%s] [reasoning=%v] [%s]\n",
		c.chatURL, req.Conversation.SessionID, req.Conversation.UserID, req.Runtime.Reasoning,
		modelconfig.SummarizeLLMConfigForLog(req.Runtime.LLMConfig),
	)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.chatURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("upstream /api/chat returned non-200")
	}
	var out LazyChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StreamChat text /api/chat_stream，text channel；ctx Unsettext channel text。
func (c *ChatService) StreamChat(ctx context.Context, req *LazyChatRequest) (<-chan *LazyStreamData, error) {
	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	fmt.Printf(
		"[Core] [CHAT_UPSTREAM_REQUEST] [stream=true] [url=%s] [session_id=%s] [user_id=%s] [reasoning=%v] [%s]\n",
		c.streamChatURL, req.Conversation.SessionID, req.Conversation.UserID, req.Runtime.Reasoning,
		modelconfig.SummarizeLLMConfigForLog(req.Runtime.LLMConfig),
	)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.streamChatURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		fmt.Println("[Core] [CHAT_UPSTREAM_FAILED] url=", c.streamChatURL, " err=", err)
		return nil, err
	}
	fmt.Println("[Core] [CHAT_UPSTREAM_RESPONSE] url=", c.streamChatURL, " status=", resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, errors.New("upstream /api/chat_stream returned non-200")
	}

	return lazyStreamHandler(ctx, resp), nil
}

func lazyStreamHandler(ctx context.Context, resp *http.Response) <-chan *LazyStreamData {
	scanner := bufio.NewScanner(resp.Body)
	dataChan := make(chan *LazyStreamData)
	go func() {
		defer func() {
			close(dataChan)
			_ = resp.Body.Close()
		}()
		// text
		scanner.Buffer(nil, 512*1024)
		for scanner.Scan() && ctx.Err() == nil {
			text := strings.TrimSpace(scanner.Text())
			if text == "" {
				continue
			}
			data := &LazyStreamData{}
			var streamResp LazyChatResponse
			if err := json.Unmarshal([]byte(text), &streamResp); err != nil {
				data.RawText = text
			} else {
				data.Resp = &streamResp
			}
			select {
			case dataChan <- data:
			case <-ctx.Done():
				return
			}
		}
	}()
	return dataChan
}

// UpstreamStreamChunk text ChatConversations text，text LazyChatResponse.Data。
type UpstreamStreamChunk struct {
	Text          string              `json:"text"`
	Think         string              `json:"think"`
	Status        string              `json:"status"`
	Sources       []any               `json:"sources"`
	ReasoningText string              `json:"reasoning_text"` // text think
	TaskCreated   *TaskCreatedEvent   `json:"task_created,omitempty"`
	AskPending    *AskPendingEvent    `json:"ask_pending,omitempty"`
	IntentUpdated *IntentUpdatedEvent `json:"intent_updated,omitempty"`
	Heartbeat     bool                `json:"heartbeat,omitempty"`
	ToolCallTurns int64               `json:"tool_call_turns"`
}

type upstreamStreamLine struct {
	Code int                 `json:"code"`
	Msg  string              `json:"msg"`
	Data UpstreamStreamChunk `json:"data"`
}

func buildLazyChatRequest(body map[string]any) *LazyChatRequest {
	req := &LazyChatRequest{
		Runtime: ChatRuntimeOptions{
			Reasoning: true,
		},
		Personalization: ChatPersonalizationOptions{
			UseMemory: true,
		},
	}
	if q, ok := body["query"].(string); ok {
		req.Message.Query = q
	}
	if s, ok := body["session_id"].(string); ok {
		req.Conversation.SessionID = s
	}
	req.Message.History = chatMessagesFromAny(body["history"])
	req.Message.Files = filesMapFromAny(body["files"])
	req.Retrieval.Filters = datasetFiltersFromAny(body["filters"])
	if reasoning, ok := body["reasoning"].(bool); ok {
		req.Runtime.Reasoning = reasoning
	}
	if databases, ok := body["databases"].([]any); ok {
		req.Retrieval.Databases = databases
	}
	if dataset, ok := body["dataset"].(string); ok {
		req.Retrieval.Dataset = strings.TrimSpace(dataset)
	}
	req.Retrieval.LocalFSSources = anySlice(body["local_fs_sources"])
	req.Agent.DisabledTools = stringSlice(body["disabled_tools"])
	req.Agent.AvailableSkills = stringSlice(body["available_skills"])
	if memory, ok := body["memory"].(string); ok {
		req.Personalization.Memory = memory
	}
	if preference, ok := body["user_preference"].(string); ok {
		req.Personalization.UserPreference = preference
	}
	if useMemory, ok := body["use_memory"].(bool); ok {
		req.Personalization.UseMemory = useMemory
	}
	if environmentContext, ok := body["environment_context"].(map[string]any); ok {
		req.Runtime.EnvironmentContext = environmentContext
	}
	if userID, ok := body["user_id"].(string); ok {
		req.Conversation.UserID = strings.TrimSpace(userID)
	}
	if mode, ok := body["mode"].(string); ok {
		req.Conversation.Mode = strings.TrimSpace(mode)
	}
	if hasSubagents, ok := body["has_subagents"].(bool); ok {
		req.Agent.HasSubagents = hasSubagents
	}
	if convID, ok := body["conversation_id"].(string); ok {
		req.Conversation.ConversationID = strings.TrimSpace(convID)
	}
	if debug, ok := body["debug"].(bool); ok {
		req.Runtime.Debug = debug
	}
	if priority, ok := body["priority"]; ok {
		req.Runtime.Priority = intPointerFromAny(priority)
	}
	if trace, ok := body["trace"].(bool); ok {
		req.Runtime.Trace = trace
	}
	if llmConfig, ok := body["llm_config"].(map[string]any); ok {
		req.Runtime.LLMConfig = llmConfig
	}
	if toolConfig, ok := body["tool_config"].(map[string]string); ok {
		tc := make(map[string]any, len(toolConfig))
		for k, v := range toolConfig {
			if value := normalizeToolConfigValue(v); value != nil {
				tc[k] = value
			}
		}
		if len(tc) > 0 {
			req.Runtime.ToolConfig = tc
		}
	} else if toolConfigAny, ok := body["tool_config"].(map[string]any); ok {
		tc := make(map[string]any, len(toolConfigAny))
		for k, v := range toolConfigAny {
			if value := normalizeToolConfigValue(v); value != nil {
				tc[k] = value
			} else if values, ok := v.([]any); ok {
				keys := make([]string, 0, len(values))
				for _, value := range values {
					if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
						keys = append(keys, strings.TrimSpace(s))
					}
				}
				if len(keys) > 0 {
					tc[k] = keys
				}
			}
		}
		if len(tc) > 0 {
			req.Runtime.ToolConfig = tc
		}
	}
	if mcpConfig, ok := body["mcp_config"].([]any); ok {
		req.Runtime.MCPConfig = mcpConfig
	} else if mcpConfigAny, ok := body["mcp_config"].([]map[string]any); ok {
		req.Runtime.MCPConfig = make([]any, 0, len(mcpConfigAny))
		for _, item := range mcpConfigAny {
			req.Runtime.MCPConfig = append(req.Runtime.MCPConfig, item)
		}
	}
	if pluginContext, ok := body["plugin_context"].(map[string]any); ok && len(pluginContext) > 0 {
		req.Plugin.PluginContext = pluginContext
	}
	if askResponse, ok := body["ask_response"].(map[string]any); ok && len(askResponse) > 0 {
		req.Plugin.AskResponse = askResponse
	}
	// current_turn_seq is an int in the body map. JSON numbers decode as float64.
	switch v := body["current_turn_seq"].(type) {
	case int:
		req.Message.CurrentTurnSeq = v
	case int64:
		req.Message.CurrentTurnSeq = int(v)
	case float64:
		req.Message.CurrentTurnSeq = int(v)
	}
	if v, ok := body["enable_plugin"].(bool); ok {
		req.Plugin.EnablePlugin = &v
	}
	if v, ok := body["enable_subagent"].(bool); ok {
		req.Agent.EnableSubagent = &v
	}
	return req
}

func intPointerFromAny(v any) *int {
	switch value := v.(type) {
	case int:
		return &value
	case int64:
		converted := int(value)
		return &converted
	case float64:
		converted := int(value)
		return &converted
	default:
		return nil
	}
}

func chatMessagesFromAny(v any) []ChatMessage {
	raw, ok := v.([]map[string]string)
	if ok {
		messages := make([]ChatMessage, 0, len(raw))
		for _, h := range raw {
			messages = append(messages, ChatMessage{Role: h["role"], Content: h["content"]})
		}
		return messages
	}

	rawAny, ok := v.([]any)
	if !ok {
		return nil
	}
	messages := make([]ChatMessage, 0, len(rawAny))
	for _, item := range rawAny {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		role, _ := m["role"].(string)
		content, _ := m["content"].(string)
		messages = append(messages, ChatMessage{Role: role, Content: content})
	}
	if len(messages) == 0 {
		return nil
	}
	return messages
}

func datasetFiltersFromAny(v any) *DatasetFilters {
	m, _ := v.(map[string]any)
	if m == nil {
		return nil
	}
	filters := &DatasetFilters{
		Subject:    stringSlice(m["subject"]),
		DatasetIDs: stringSlice(m["kb_id"]),
		Tags:       stringSlice(m["tags"]),
		Creators:   stringSlice(m["creator"]),
	}
	if len(filters.Subject) == 0 && len(filters.DatasetIDs) == 0 && len(filters.Tags) == 0 && len(filters.Creators) == 0 {
		return nil
	}
	return filters
}

func filesMapFromAny(v any) map[string][]string {
	// Fast path: already the correct type (set by buildChatRequestBody).
	if m, ok := v.(map[string][]string); ok {
		if len(m) == 0 {
			return nil
		}
		return m
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string][]string, len(m))
	for k, val := range m {
		switch xs := val.(type) {
		case []any:
			paths := make([]string, 0, len(xs))
			for _, it := range xs {
				if s, ok := it.(string); ok && strings.TrimSpace(s) != "" {
					paths = append(paths, s)
				}
			}
			if len(paths) > 0 {
				out[k] = paths
			}
		case []string:
			if len(xs) > 0 {
				out[k] = xs
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func stringSlice(v any) []string {
	if raw, ok := v.([]string); ok {
		if len(raw) == 0 {
			return nil
		}
		return raw
	}
	rawAny, ok := v.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(rawAny))
	for _, item := range rawAny {
		s, _ := item.(string)
		if strings.TrimSpace(s) != "" {
			result = append(result, s)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func anySlice(v any) []any {
	if raw, ok := v.([]any); ok {
		if len(raw) == 0 {
			return nil
		}
		return raw
	}
	rawMaps, ok := v.([]map[string]any)
	if !ok || len(rawMaps) == 0 {
		return nil
	}
	out := make([]any, 0, len(rawMaps))
	for _, item := range rawMaps {
		out = append(out, item)
	}
	return out
}

func debugJSON(v any) string {
	safe := redactForLog(v)
	b, err := json.Marshal(safe)
	if err != nil {
		return fmt.Sprintf("<%T>", v)
	}
	return string(b)
}

func redactForLog(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<%T>", v)
	}
	var decoded any
	if err := json.Unmarshal(b, &decoded); err != nil {
		return fmt.Sprintf("<%T>", v)
	}
	return redactDecodedForLog(decoded)
}

func redactDecodedForLog(v any) any {
	switch value := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for k, item := range value {
			if strings.EqualFold(k, "tool_config") {
				out[k] = summarizeSecretMapForLog(item)
				continue
			}
			out[k] = redactDecodedForLog(item)
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = redactDecodedForLog(item)
		}
		return out
	default:
		return value
	}
}

func summarizeSecretMapForLog(v any) map[string]string {
	result := map[string]string{}
	if values, ok := v.(map[string]any); ok {
		for k, item := range values {
			if secret, ok := item.(string); ok {
				result[k] = fmt.Sprintf("<redacted len=%d>", len(strings.TrimSpace(secret)))
			} else {
				result[k] = "<redacted>"
			}
		}
	}
	if len(result) == 0 {
		result["_"] = "<redacted>"
	}
	return result
}

// StreamChatUpstream text：text ChatConversations text，text ChatService.StreamChat text。
// body textRequest JSON text map text，baseURL text endpoint（text /api/...）。
func StreamChatUpstream(ctx context.Context, baseURL string, body map[string]any) (<-chan UpstreamStreamChunk, error) {
	service := NewChatServiceWithEndpoint(baseURL)
	req := buildLazyChatRequest(body)

	streamChan, err := service.StreamChat(ctx, req)
	if err != nil {
		return nil, err
	}

	out := make(chan UpstreamStreamChunk, 1)
	go func() {
		defer close(out)
		for d := range streamChan {
			if d == nil {
				continue
			}
			if d.Resp == nil {
				// textFailedtext RawText：text，text，text
				continue
			}
			chunk := UpstreamStreamChunk{
				Text:          d.Resp.Data.Text,
				Think:         d.Resp.Data.ReasoningText,
				Status:        d.Resp.Data.Status,
				Sources:       d.Resp.Data.Sources,
				ReasoningText: d.Resp.Data.ReasoningText,
				TaskCreated:   d.Resp.Data.TaskCreated,
				AskPending:    d.Resp.Data.AskPending,
				IntentUpdated: d.Resp.Data.IntentUpdated,
				Heartbeat:     d.Resp.Data.Heartbeat,
				ToolCallTurns: d.Resp.Data.ToolCallTurns,
			}
			select {
			case out <- chunk:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
