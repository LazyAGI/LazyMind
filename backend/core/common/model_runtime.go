package common

import (
	"encoding/json"
	"errors"
)

type ModelRuntimeEvent struct {
	SchemaVersion int             `json:"schema_version"`
	EventID       string          `json:"event_id"`
	RunID         string          `json:"run_id"`
	Type          string          `json:"type"`
	Data          json.RawMessage `json:"data"`
}

type ModelSwitchIdentity struct {
	CandidateID     string `json:"candidate_id"`
	Source          string `json:"source"`
	ModelID         string `json:"model_id"`
	ProviderGroupID string `json:"provider_group_id"`
	ProviderName    string `json:"provider_name,omitempty"`
	ModelName       string `json:"model_name,omitempty"`
}
type ModelSwitchedData struct {
	SwitchIndex        int                 `json:"switch_index"`
	From               ModelSwitchIdentity `json:"from"`
	To                 ModelSwitchIdentity `json:"to"`
	ReasonCode         string              `json:"reason_code"`
	FromMaxInputTokens *int64              `json:"from_max_input_tokens"`
	ToMaxInputTokens   *int64              `json:"to_max_input_tokens"`
	ContextCompacted   bool                `json:"context_compacted"`
}
type ModelSwitchRecord struct {
	ModelSwitchedData
	EventID string `json:"event_id"`
}

type ModelFallbackInitialSwitch struct {
	From               ModelSwitchIdentity `json:"from"`
	ReasonCode         string              `json:"reason_code"`
	FromMaxInputTokens *int64              `json:"from_max_input_tokens"`
}

// ModelOutput identifies a Host-owned output boundary, independently of SDK calls.
type ModelOutput struct {
	RunID     string `json:"run_id"`
	CallIndex int    `json:"call_index"`
	Phase     string `json:"phase,omitempty"`
}

// Keys define the public model failure contract. Values retain Auto's existing
// policy: true marks this model unavailable for the next automatic selection.
var ValidModelFailureCodes = map[string]bool{
	"invalid_request": false, "authentication_failed": true, "permission_denied": true, "not_found": true,
	"rate_limited": true, "usage_limit_exceeded": true, "concurrency_limited": true, "quota_exhausted": true,
	"balance_exhausted": true, "organization_spend_limit_exceeded": true, "project_spend_limit_exceeded": true,
	"input_filtered": false, "output_filtered": false, "token_limit": false, "request_timeout": true,
	"provider_overloaded": true, "service_unavailable": true, "provider_internal_error": true,
	"provider_rejected": false, "conflict": false, "unprocessable_entity": false, "protocol_error": false,
	"transport_error": true, "model_configuration": true,
}

func ValidateModelSwitched(raw json.RawMessage) error {
	var data ModelSwitchedData
	if json.Unmarshal(raw, &data) != nil || data.SwitchIndex < 1 {
		return errors.New("invalid model_switched data")
	}
	for _, identity := range []ModelSwitchIdentity{data.From, data.To} {
		if identity.ModelID == "" || identity.CandidateID != identity.Source+":"+identity.ModelID || identity.Source != "own" && identity.Source != "shared" && identity.Source != "cloud" {
			return errors.New("invalid model switch identity")
		}
	}
	if data.From.CandidateID == data.To.CandidateID {
		return errors.New("model switch must change candidate")
	}
	if _, ok := ValidModelFailureCodes[data.ReasonCode]; !ok && data.ReasonCode != "model_config_unavailable" {
		return errors.New("invalid model switch reason")
	}
	for _, window := range []*int64{data.FromMaxInputTokens, data.ToMaxInputTokens} {
		if window != nil && *window <= 0 {
			return errors.New("invalid model switch window")
		}
	}
	return nil
}
func SameModelSwitchIdentity(got, expected ModelSwitchIdentity) bool {
	return got.CandidateID == expected.CandidateID && got.Source == expected.Source && got.ModelID == expected.ModelID && got.ProviderGroupID == expected.ProviderGroupID
}
