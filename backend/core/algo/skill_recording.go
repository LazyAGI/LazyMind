package algo

import (
	"context"
	"encoding/json"
)

type RecordingEvidence struct {
	Events      []json.RawMessage `json:"events"`
	Limitations []string          `json:"limitations"`
}

type RecordingFrame struct {
	Image   string  `json:"image"`
	Seconds float64 `json:"seconds"`
}
type RecordingSkillResult struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Content     string   `json:"content"`
	Missing     []string `json:"missing"`
	Error       string   `json:"error"`
}

func GenerateRecordingSkill(ctx context.Context, frames []RecordingFrame, notes string, evidence RecordingEvidence, config map[string]any) (RecordingSkillResult, error) {
	if evidence.Events == nil {
		evidence.Events = []json.RawMessage{}
	}
	if evidence.Limitations == nil {
		evidence.Limitations = []string{}
	}
	var out RecordingSkillResult
	_, err := postReviewJSON(ctx, "/api/chat/recording_skill", map[string]any{"frames": frames, "notes": notes, "evidence": evidence, "model_configs": config}, &out)
	return out, err
}
