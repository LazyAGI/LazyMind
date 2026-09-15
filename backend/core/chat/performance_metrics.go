package chat

import (
	"errors"
	"math"
)

// RunPerformanceMetrics is the normalized, content-free summary produced by
// the Algorithm service for one authoritative chat run. Optional facts use
// pointers so an unknown value cannot be confused with a measured zero.
type RunPerformanceMetrics struct {
	SchemaVersion      int      `json:"schema_version"`
	TurnSeq            *int     `json:"turn_seq,omitempty"`
	Steps              int      `json:"steps"`
	ModelSteps         int      `json:"model_steps"`
	ToolSteps          int      `json:"tool_steps"`
	WallMS             *int64   `json:"wall_ms,omitempty"`
	ModelMS            *int64   `json:"model_ms,omitempty"`
	ToolMS             *int64   `json:"tool_ms,omitempty"`
	TTFTMS             *int64   `json:"ttft_ms,omitempty"`
	Model              string   `json:"model,omitempty"`
	InputTokens        *int64   `json:"input_tokens,omitempty"`
	OutputTokens       *int64   `json:"output_tokens,omitempty"`
	TotalTokens        *int64   `json:"total_tokens,omitempty"`
	CachedTokens       *int64   `json:"cached_tokens,omitempty"`
	CacheInputTokens   *int64   `json:"cache_input_tokens,omitempty"`
	ReasoningTokens    *int64   `json:"reasoning_tokens,omitempty"`
	MaxInputTokens     *int64   `json:"max_input_tokens,omitempty"`
	ContextInputTokens *int64   `json:"context_input_tokens,omitempty"`
	CacheHitRate       *float64 `json:"cache_hit_rate,omitempty"`
	TokS               *float64 `json:"tok_s,omitempty"`
	ContextRatio       *float64 `json:"context_ratio,omitempty"`
}

func (m *RunPerformanceMetrics) Validate() error {
	if m == nil {
		return errors.New("performance metrics are nil")
	}
	if m.SchemaVersion != 1 {
		return errors.New("unsupported performance metrics schema_version")
	}
	if m.Steps < 0 || m.ModelSteps < 0 || m.ToolSteps < 0 {
		return errors.New("performance step counts must be non-negative")
	}
	if m.TurnSeq != nil && *m.TurnSeq < 0 {
		return errors.New("performance turn_seq must be non-negative")
	}
	for _, value := range []*int64{
		m.WallMS, m.ModelMS, m.ToolMS, m.TTFTMS,
		m.InputTokens, m.OutputTokens, m.TotalTokens, m.CachedTokens,
		m.CacheInputTokens, m.ReasoningTokens, m.MaxInputTokens, m.ContextInputTokens,
	} {
		if value != nil && *value < 0 {
			return errors.New("performance numeric facts must be non-negative")
		}
	}
	for _, value := range []*float64{m.CacheHitRate, m.TokS, m.ContextRatio} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0) {
			return errors.New("performance derived values must be finite and non-negative")
		}
	}
	return nil
}
