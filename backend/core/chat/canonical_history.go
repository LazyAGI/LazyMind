package chat

import (
	"encoding/json"
	"lazymind/core/common/orm"
)

type canonicalHistory struct {
	Version  int              `json:"version"`
	RunID    string           `json:"run_id"`
	Messages []map[string]any `json:"messages"`
}

// Dual-answer scheduling predates chatOutputProjection. Track only its private
// replay state here without changing either answer's delivery scheduler.
type canonicalOutput struct {
	owner      chatModelOutput
	projection chatOutputProjection
}

func (s *canonicalOutput) consume(chunk UpstreamStreamChunk) error {
	accepted, err := s.owner.accept(chunk)
	if err != nil || chunk.Err != nil {
		s.owner.rollback(&s.projection)
		if err != nil {
			return err
		}
		return chunk.Err
	}
	if !accepted {
		return nil
	}
	s.owner.apply(chunk.ModelOutput, &s.projection)
	if chunk.RuntimeEvent != nil && chunk.RuntimeEvent.Type == RuntimeEventRunFinished {
		s.owner.rollback(&s.projection)
	}
	s.projection.appendCanonical(chunk.CanonicalMessages)
	return nil
}

func (s *canonicalOutput) ext(raw json.RawMessage) json.RawMessage {
	return withCanonicalHistory(raw, s.owner.cursor.RunID, s.owner.committed(s.projection).durableCanonical())
}

func (p chatOutputProjection) durableCanonical() []map[string]any {
	if !p.canonicalEnabled {
		return nil
	}
	return p.canonicalMessages
}

func (p *chatOutputProjection) appendCanonical(messages []map[string]any) {
	for _, message := range messages {
		role, _ := message["role"].(string)
		if role != "assistant" && role != "tool" && role != "user" {
			continue
		}
		// Legacy upstream tool events lack a canonical assistant. They must not
		// activate a partial sidecar that would hide the existing transcript.
		if len(p.canonicalMessages) == 0 && role == "tool" {
			continue
		}
		raw, err := json.Marshal(message)
		if err != nil {
			continue
		}
		var clone map[string]any
		if json.Unmarshal(raw, &clone) != nil {
			continue
		}
		// Copy on append: model-output checkpoints share immutable prior slices.
		p.canonicalMessages = append(append([]map[string]any(nil), p.canonicalMessages...), clone)
		p.canonicalTextOffset = len(p.text)
		p.canonicalEnabled = true
	}
}

func (p *chatOutputProjection) appendCanonicalSteering(guidance []steeringApplied) {
	if len(guidance) == 0 {
		return
	}
	enabled := p.canonicalEnabled
	defer func() { p.canonicalEnabled = enabled }()
	if p.canonicalTextOffset < len(p.text) {
		p.appendCanonical([]map[string]any{{"role": "assistant", "content": p.text[p.canonicalTextOffset:]}})
	}
	for _, item := range guidance {
		p.appendCanonical([]map[string]any{{"role": "user", "content": item.Text}})
	}
}

func withCanonicalHistory(raw json.RawMessage, runID string, messages []map[string]any) json.RawMessage {
	var ext map[string]any
	_ = json.Unmarshal(raw, &ext)
	if ext == nil {
		ext = map[string]any{}
	}
	delete(ext, "canonical_messages")
	if len(messages) > 0 {
		ext["canonical_messages"] = canonicalHistory{Version: 1, RunID: runID, Messages: messages}
	}
	return marshalChatHistoryExt(ext)
}

func canonicalMessagesFromHistory(history orm.ChatHistory) []map[string]any {
	var ext struct {
		Canonical canonicalHistory `json:"canonical_messages"`
	}
	if json.Unmarshal(history.Ext, &ext) != nil || ext.Canonical.Version != 1 || ext.Canonical.RunID != history.RunID {
		return nil
	}
	return ext.Canonical.Messages
}
