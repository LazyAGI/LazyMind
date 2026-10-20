package algo

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"lazymind/core/common"
	"net/http"
	"time"
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
	if progress, ok := ctx.Value(recordingProgressKey{}).(func(int)); ok {
		return streamRecordingSkill(ctx, frames, notes, evidence, config, progress)
	}
	var out RecordingSkillResult
	_, err := postReviewJSON(ctx, "/api/chat/recording_skill", map[string]any{"frames": frames, "notes": notes, "evidence": evidence, "model_configs": config}, &out)
	return out, err
}

type recordingProgressKey struct{}

// WithRecordingProgress reports completed frame analysis without exposing captured data.
func WithRecordingProgress(ctx context.Context, update func(int)) context.Context {
	return context.WithValue(ctx, recordingProgressKey{}, update)
}

func streamRecordingSkill(ctx context.Context, frames []RecordingFrame, notes string, evidence RecordingEvidence, config map[string]any, progress func(int)) (RecordingSkillResult, error) {
	var result RecordingSkillResult
	body, err := json.Marshal(map[string]any{"frames": frames, "notes": notes, "evidence": evidence, "model_configs": config})
	if err != nil {
		return result, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, common.JoinURL(common.ChatServiceEndpoint(), "/api/chat/recording_skill_stream"), bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("recording stream HTTP %d", resp.StatusCode)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var event struct {
			Progress *int                  `json:"progress"`
			Result   *RecordingSkillResult `json:"result"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return result, err
		}
		if event.Progress != nil && *event.Progress >= 0 && *event.Progress <= 90 {
			progress(*event.Progress)
		}
		if event.Result != nil {
			return *event.Result, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return result, err
	}
	return result, fmt.Errorf("recording stream ended without result")
}
