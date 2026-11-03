package chat

import (
	"encoding/json"
	"errors"
	"strings"

	"lazymind/core/common"
	"lazymind/core/common/orm"
)

type ChatModelOutput = common.ModelOutput

func validateModelOutputChunk(chunk UpstreamStreamChunk) error {
	m := chunk.ModelOutput
	if m == nil {
		return nil
	}
	if strings.TrimSpace(m.RunID) == "" || m.CallIndex < 1 || m.Phase != "" && m.Phase != "begin" && m.Phase != "commit" && m.Phase != "rollback" {
		return errors.New("invalid model output boundary")
	}
	if m.Phase == "" {
		if chunk.RuntimeEvent != nil || chunk.TaskCreated != nil || chunk.ArtifactCreated != nil || chunk.AskPending != nil || chunk.ToolLimitPending != nil || chunk.IntentUpdated != nil || chunk.ModelContextUpdated != nil || chunk.WorkflowPreflightUpdated != nil || chunk.CapabilityDependency != nil || chunk.ToolConfiguration != nil || chunk.Heartbeat {
			return errors.New("model output content contains an independent event")
		}
		return nil
	}
	if hasBusinessStreamPayload(chunk) || chunk.ExportSnapshot != nil || chunk.ModelContextUpdated != nil {
		return errors.New("model output boundary contains semantic content")
	}
	if event := chunk.RuntimeEvent; event != nil && !(m.Phase == "commit" && event.Type == RuntimeEventModelSwitched || m.Phase == "rollback" && event.Type == RuntimeEventRunFinished) {
		return errors.New("model output boundary contains an unrelated runtime event")
	}
	return nil
}

type chatOutputProjection struct {
	text, result, pendingThink           string
	sources                              []any
	exportSnapshot                       *ChatExportSnapshot
	thinkingActive, sawToolResultPreview bool
}

func (p chatOutputProjection) rawResult() string {
	if p.pendingThink != "" {
		return p.result + "<think>" + p.pendingThink + "</think>"
	}
	return p.result
}

// Only the current call and its checkpoint are retained; closed calls need no registry.
type chatModelOutput struct {
	cursor     ChatModelOutput
	checkpoint *chatOutputProjection
}

func newChatModelOutput(runID string) chatModelOutput {
	return chatModelOutput{cursor: ChatModelOutput{RunID: runID, Phase: "commit"}}
}
func (o *chatModelOutput) enabled() bool { return o.cursor.CallIndex > 0 }
func (o *chatModelOutput) metadata() *ChatModelOutput {
	copy := o.cursor
	return &copy
}

// Check the whole frame before route or projection state can change.
func (o *chatModelOutput) accept(chunk UpstreamStreamChunk) (bool, error) {
	if err := validateModelOutputChunk(chunk); err != nil {
		return false, err
	}
	m := chunk.ModelOutput
	if m == nil {
		if o.enabled() && chunk.RuntimeEvent != nil && chunk.RuntimeEvent.Type == RuntimeEventModelSwitched {
			return false, errors.New("model switch has no output commit")
		}
		return true, nil
	}
	if m.RunID != o.cursor.RunID || m.CallIndex < o.cursor.CallIndex {
		return false, nil
	}
	if m.Phase == "begin" {
		if m.CallIndex == o.cursor.CallIndex {
			return false, nil
		}
		if o.checkpoint != nil || m.CallIndex != o.cursor.CallIndex+1 {
			return false, errors.New("model output sequence mismatch")
		}
		return true, nil
	}
	if m.CallIndex != o.cursor.CallIndex {
		return false, errors.New("model output sequence mismatch")
	}
	if o.checkpoint == nil && m.Phase == "rollback" && chunk.RuntimeEvent != nil && chunk.RuntimeEvent.Type == RuntimeEventRunFinished {
		return true, nil
	}
	return o.checkpoint != nil, nil
}
func (o *chatModelOutput) apply(m *ChatModelOutput, p *chatOutputProjection) {
	if m == nil {
		return
	}
	switch m.Phase {
	case "begin":
		copy := *p
		o.checkpoint = &copy
		o.cursor = *m
	case "commit":
		o.checkpoint = nil
		o.cursor = *m
	case "rollback":
		o.rollback(p)
	}
}
func (o *chatModelOutput) rollback(p *chatOutputProjection) bool {
	if o.checkpoint == nil {
		return false
	}
	*p = *o.checkpoint
	o.checkpoint = nil
	o.cursor.Phase = "rollback"
	return true
}
func (o *chatModelOutput) committed(p chatOutputProjection) chatOutputProjection {
	if o.checkpoint != nil {
		return *o.checkpoint
	}
	return p
}
func (o *chatModelOutput) ext(raw json.RawMessage, p chatOutputProjection, convID, historyID string) json.RawMessage {
	if !o.enabled() {
		return raw
	}
	raw = mergeModelOutputIntoExt(raw, o.metadata())
	p = o.committed(p)
	var exports []ChatExport
	if p.exportSnapshot != nil {
		exports = finalizeChatExports(p.exportSnapshot, convID, historyID, o.cursor.RunID)
	}
	return withChatExports(raw, exports)
}
func mergeModelOutputIntoExt(raw json.RawMessage, metadata *ChatModelOutput) json.RawMessage {
	var ext map[string]any
	_ = json.Unmarshal(raw, &ext)
	if ext == nil {
		ext = map[string]any{}
	}
	delete(ext, "model_output")
	if metadata != nil {
		ext["model_output"] = metadata
	}
	return marshalChatHistoryExt(ext)
}
func modelOutputFromExt(raw json.RawMessage) *ChatModelOutput {
	var ext struct {
		ModelOutput *ChatModelOutput `json:"model_output"`
	}
	_ = json.Unmarshal(raw, &ext)
	return ext.ModelOutput
}
func (o *chatModelOutput) replacement(p chatOutputProjection, ext json.RawMessage, convID, historyID string, seq int) *ChatChunkResponse {
	exports := []ChatExport{}
	if p.exportSnapshot != nil {
		exports = finalizeChatExports(p.exportSnapshot, convID, historyID, o.cursor.RunID)
	}
	sources := p.sources
	if sources == nil {
		sources = []any{}
	}
	raw := p.rawResult()
	tail := steeringTail(raw, ext, o.cursor.RunID)
	text := stripToolTags(p.text)
	if len(tail) != len(raw) {
		text = stripThinkTags(stripToolTags(tail))
	}
	return &ChatChunkResponse{ConversationID: convID, HistoryID: historyID, Seq: int32(seq), Delta: text, DeltaMode: ChatDeltaModeReplace, ReasoningContent: extractThinkContent(tail), Sources: sources, Exports: &exports, ModelOutput: o.metadata()}
}

// The first DB/REST frame hydrates the same projection as a cache snapshot.
func addModelOutputSnapshot(fields map[string]any, history orm.ChatHistory) {
	metadata := modelOutputFromExt(history.Ext)
	if metadata == nil || metadata.RunID != history.RunID {
		return
	}
	fields["model_output"] = metadata
	fields["reasoning_content"] = extractThinkContent(steeringTail(history.Result, history.Ext, history.RunID))
	if values, ok := fields["sources"].([]any); !ok || values == nil {
		fields["sources"] = []any{}
	}
	if exports := chatExportsFromExt(history.Ext); len(exports) > 0 {
		fields["exports"] = exports
	} else {
		fields["exports"] = []ChatExport{}
	}
}

// Absolute fenced snapshots must carry empty values; legacy frames retain their encoding.
func (c ChatChunkResponse) MarshalJSON() ([]byte, error) {
	type plain ChatChunkResponse
	if c.ModelOutput == nil || c.DeltaMode != ChatDeltaModeReplace {
		return json.Marshal(plain(c))
	}
	sources, exports := c.Sources, c.Exports
	if sources == nil {
		sources = []any{}
	}
	if exports == nil {
		empty := []ChatExport{}
		exports = &empty
	}
	return json.Marshal(struct {
		plain
		Sources          []any         `json:"sources"`
		ReasoningContent string        `json:"reasoning_content"`
		Exports          *[]ChatExport `json:"exports"`
	}{plain(c), sources, c.ReasoningContent, exports})
}
