package chat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"lazymind/core/common/orm"
)

type forkError struct{ Code string }

func (e *forkError) Error() string { return e.Code }
func forkFail(code string) error   { return &forkError{Code: code} }

type conversationConfigSnapshot struct {
	Version          int             `json:"version"`
	Completeness     string          `json:"completeness"`
	Model            *chatModelRoute `json:"model,omitempty"`
	ThinkingDepth    string          `json:"thinking_depth,omitempty"`
	ChatExecutor     string          `json:"chat_executor,omitempty"`
	EnableWorkflow   *bool           `json:"enable_workflow,omitempty"`
	EnableSubagent   *bool           `json:"enable_subagent,omitempty"`
	WorkflowMode     string          `json:"workflow_mode,omitempty"`
	Filters          map[string]any  `json:"filters"`
	LocalFSSourceIDs []string        `json:"local_fs_source_ids"`
	MaxInputTokens   string          `json:"max_input_tokens,omitempty"`
	Reasoning        bool            `json:"reasoning"`
	UseMemory        bool            `json:"use_memory"`
	Mode             string          `json:"mode"`
	RunID            string          `json:"run_id,omitempty"`
}

// Only resolved, public configuration belongs in a history snapshot.
func mergeConversationConfigSnapshot(raw json.RawMessage, body map[string]any) json.RawMessage {
	s := conversationConfigSnapshot{Version: 1, Completeness: "complete", Model: chatModelRouteFromBody(body), ChatExecutor: ChatExecutorLazyMind, WorkflowMode: workflowModeFromReqBody(body), Filters: map[string]any{}}
	s.ThinkingDepth, _ = body["thinking_depth"].(string)
	s.Reasoning, _ = body["reasoning"].(bool)
	s.UseMemory, _ = body["use_memory"].(bool)
	s.Mode, _ = body["mode"].(string)
	s.RunID, _ = body["run_id"].(string)
	s.LocalFSSourceIDs = []string{}
	if sources, ok := body["local_fs_sources"].([]map[string]any); ok {
		for _, source := range sources {
			if id, ok := source["source_id"].(string); ok {
				s.LocalFSSourceIDs = append(s.LocalFSSourceIDs, id)
			}
		}
	}
	if config, ok := body["llm_config"].(map[string]any); ok {
		if llm, ok := config["llm"].(map[string]any); ok {
			s.MaxInputTokens, _ = llm["max_input_tokens"].(string)
		}
	}
	if value, ok := body["enable_workflow"].(bool); ok {
		s.EnableWorkflow = &value
	}
	if value, ok := body["enable_subagent"].(bool); ok {
		s.EnableSubagent = &value
	}
	if filters, ok := body["filters"].(map[string]any); ok {
		s.Filters = map[string]any{}
		for _, key := range []string{"kb_id", "doc_id", "metadata_filter", "file_type", "creator", "tags"} {
			if v, ok := filters[key]; ok {
				s.Filters[key] = v
			}
		}
	}
	if s.Model == nil || s.ThinkingDepth == "" || s.EnableWorkflow == nil || s.EnableSubagent == nil {
		s.Completeness = "partial"
	}
	ext := map[string]any{}
	_ = json.Unmarshal(raw, &ext)
	if ext == nil {
		ext = map[string]any{}
	}
	ext["conversation_config_snapshot"] = s
	return marshalChatHistoryExt(ext)
}

func forkConfigFromHistory(h orm.ChatHistory) conversationConfigSnapshot {
	var ext struct {
		Snapshot conversationConfigSnapshot `json:"conversation_config_snapshot"`
	}
	_ = json.Unmarshal(h.Ext, &ext)
	s := ext.Snapshot
	if s.Version != 1 {
		s = conversationConfigSnapshot{Completeness: "missing"}
	}
	if s.Model == nil {
		s.Model = chatModelRouteFromHistoryExt(h.Ext)
	}
	return s
}

func forkConfigForConversation(c orm.Conversation) *conversationConfigSnapshot {
	var ext struct {
		Config *conversationConfigSnapshot `json:"fork_config"`
	}
	_ = json.Unmarshal(c.Ext, &ext)
	return ext.Config
}

// Request fields set by the user still take precedence. The frontend initializes
// them from this conversation, and omits global mode/memory defaults for forks.
func applyForkRequestDefaults(raw map[string]any, c orm.Conversation, hasExplicitSearchConfig bool) {
	s := forkConfigForConversation(c)
	if s == nil {
		return
	}
	for key, value := range map[string]any{"mode": s.Mode, "use_memory": s.UseMemory, "reasoning": s.Reasoning, "thinking_depth": c.ThinkingDepth} {
		if _, present := raw[key]; !present {
			raw[key] = value
		}
	}
	if raw["filters"] == nil && !hasExplicitSearchConfig {
		filters := map[string]any{}
		for key, value := range s.Filters {
			filters[key] = value
		}
		var search map[string]any
		_ = json.Unmarshal(c.SearchConfig, &search)
		delete(filters, "kb_id")
		delete(filters, "creator")
		delete(filters, "tags")
		for key, value := range filtersFromSearchConfig(search) {
			filters[key] = value
		}
		raw["filters"] = filters
	}
}

func validateForkPrefix(histories []orm.ChatHistory) error {
	if len(histories) == 0 {
		return forkFail("SOURCE_UNAVAILABLE")
	}
	seqs := map[int]bool{}
	for _, h := range histories {
		if seqs[h.Seq] {
			return forkFail("ANSWER_SELECTION_REQUIRED")
		}
		seqs[h.Seq] = true
		switch h.RunStatus {
		case "completed", "failed", "cancelled", "interrupted":
		case "":
			// Legacy rows predate run tracking and only a nonempty persisted answer proves completion.
			if h.Result == "" || h.RunID != "" {
				return forkFail("SOURCE_NOT_SETTLED")
			}
		default:
			return forkFail("SOURCE_NOT_SETTLED")
		}
		s := forkConfigFromHistory(h)
		if s.RunID != "" && s.RunID != h.RunID {
			return forkFail("SOURCE_NOT_SETTLED")
		}
	}
	return nil
}

func forkCanonicalJSON(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func forkDigest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "v1:" + hex.EncodeToString(sum[:]), nil
}

var forkExtKeys = []string{"input", "display_query", "mentions", "trail", "model_route", "conversation_config_snapshot", "ask_answers", "ask_answers_structured"}

func forkInheritableExt(raw json.RawMessage) (map[string]any, error) {
	v, err := forkCanonicalJSON(raw)
	if err != nil {
		return nil, err
	}
	ext, _ := v.(map[string]any)
	out := map[string]any{}
	for _, key := range forkExtKeys {
		if value, ok := ext[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func forkPrefixRevision(histories []orm.ChatHistory) (string, error) {
	items := make([]any, 0, len(histories))
	for _, h := range histories {
		ext, err := forkInheritableExt(h.Ext)
		if err != nil {
			return "", err
		}
		sources, err := forkCanonicalJSON(h.RetrievalResult)
		if err != nil {
			return "", err
		}
		items = append(items, map[string]any{"id": h.ID, "seq": h.Seq, "raw_content": h.RawContent, "content": h.Content, "result": h.Result, "run_status": h.RunStatus, "run_id": h.RunID, "ext": ext, "retrieval_result": sources})
	}
	return forkDigest(items)
}

func remapForkReferences(value any, ids map[string]string) any {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if key == "parent_history_id" || key == "source_history_id" || key == "history_id" {
				old, _ := child.(string)
				if id, ok := ids[old]; ok {
					v[key] = id
				} else {
					delete(v, key)
				}
			} else if key == "reference_history_ids" {
				out := []string{}
				if list, ok := child.([]any); ok {
					for _, item := range list {
						if id, ok := ids[fmt.Sprint(item)]; ok {
							out = append(out, id)
						}
					}
				}
				v[key] = out
			} else {
				v[key] = remapForkReferences(child, ids)
			}
		}
	case []any:
		for i := range v {
			v[i] = remapForkReferences(v[i], ids)
		}
	}
	return value
}

func copyForkHistories(source []orm.ChatHistory, conversationID string) ([]orm.ChatHistory, error) {
	ids := make(map[string]string, len(source))
	for _, h := range source {
		ids[h.ID] = newConversationID()
	}
	out := make([]orm.ChatHistory, 0, len(source))
	now := time.Now().UTC()
	for i, h := range source {
		ext, err := forkInheritableExt(h.Ext)
		if err != nil {
			return nil, err
		}
		remapForkReferences(ext, ids)
		if s, ok := ext["conversation_config_snapshot"].(map[string]any); ok {
			delete(s, "run_id")
		}
		revision, err := forkPrefixRevision([]orm.ChatHistory{h})
		if err != nil {
			return nil, err
		}
		ext["fork_origin"] = map[string]any{"source_history_id": h.ID, "source_history_revision": revision}
		ext["fork_read_only"] = true
		status := h.RunStatus
		if status == "" {
			status = "completed"
		}
		out = append(out, orm.ChatHistory{ID: ids[h.ID], ConversationID: conversationID, Seq: i + 1,
			RawContent: h.RawContent, Content: h.Content, Result: h.Result, RetrievalResult: append(json.RawMessage(nil), h.RetrievalResult...),
			Version: h.Version, RunStatus: status, Ext: marshalChatHistoryExt(ext),
			TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now}})
	}
	return out, nil
}
