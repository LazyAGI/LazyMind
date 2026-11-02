package common

import (
	"encoding/json"
	"testing"
)

func TestCapacitySwitchReasonIsNotAProviderFailure(t *testing.T) {
	data := ModelSwitchedData{
		SwitchIndex: 1,
		From:        ModelSwitchIdentity{CandidateID: "own:small", Source: "own", ModelID: "small"},
		To:          ModelSwitchIdentity{CandidateID: "own:large", Source: "own", ModelID: "large"},
		ReasonCode:  "model_context_not_fit",
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateModelSwitched(raw); err != nil {
		t.Fatalf("capacity selection must be a valid switch: %v", err)
	}
	if _, exists := ValidModelFailureCodes[data.ReasonCode]; exists {
		t.Fatal("local capacity selection must not become a provider failure code")
	}
	data.ReasonCode = "unknown-switch-reason"
	raw, _ = json.Marshal(data)
	if err := ValidateModelSwitched(raw); err == nil {
		t.Fatal("unknown switch reasons must still be rejected")
	}
}
