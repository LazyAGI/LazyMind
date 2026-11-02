package taskdisplay

import (
	"encoding/json"
	"errors"
	"lazymind/core/capability"
	"strings"
)

// KnowledgeOperation contains a trusted operation receipt, never raw tool logs.
type KnowledgeOperation struct {
	CallID            string                              `json:"call_id"`
	Action            string                              `json:"action"`
	Status            string                              `json:"status"`
	Summary           capability.ResourceOperationSummary `json:"summary"`
	ProcessingSummary map[string]int                      `json:"processing_summary,omitempty"`
	Items             []capability.ResourceOperationItem  `json:"items"`
}

func DecodeKnowledgeOperation(raw json.RawMessage) (KnowledgeOperation, error) {
	var out KnowledgeOperation
	if len(raw) > 1<<20 || json.Unmarshal(raw, &out) != nil || out.CallID == "" || len(out.CallID) > 128 || !strings.HasPrefix(out.Action, "knowledge.") || out.Items == nil || len(out.Items) > MaxProcessSteps {
		return out, errors.New("invalid knowledge operation receipt")
	}
	return out, nil
}
