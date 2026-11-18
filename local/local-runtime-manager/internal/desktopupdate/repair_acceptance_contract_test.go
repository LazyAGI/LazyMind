package desktopupdate

import (
	"encoding/json"
	"strings"
	"testing"
)

func repairPolicyFixture(t *testing.T) (*policyFixture, map[string]any) {
	t.Helper()
	f := newV2PolicyFixture(t, true)
	rawTransition, err := json.Marshal(f.policy["edges"].([]any)[0].(map[string]any)["componentTransitions"])
	if err != nil {
		t.Fatal(err)
	}
	var rag map[string]any
	if err := json.Unmarshal(rawTransition, &rag); err != nil {
		t.Fatal(err)
	}
	rag["rag"].(map[string]any)["targetComponentsSHA256"] = fixtureArtifact("r3-components")["sha256"]
	f.policy["repairAcceptances"] = []any{map[string]any{
		"sourceReleaseId": "r1", "failedTargetReleaseId": "r2", "repairReleaseId": "r3",
		"sourceDataProfile": "format-v2-fixture", "targetDataProfile": "format-v2-fixture",
		"componentTransitions":   rag,
		"targetComponentsSHA256": fixtureArtifact("r3-components")["sha256"],
	}}
	request := map[string]any{
		"sourceReleaseId": "r1", "failedTargetReleaseId": "r2", "currentReleaseId": "r3",
		"dataProfile": "format-v2-fixture", "sourceHadRAG": true,
		"components": map[string]string{"rag": strings.Repeat("c", 64)},
		"componentMetadata": map[string]any{"rag": map[string]any{
			"id": "rag", "revision": strings.Repeat("c", 64), "sha256": strings.Repeat("d", 64),
			"tag": strings.Repeat("a", 64), "pythonAbi": "cp311", "platform": "darwin", "arch": "arm64",
			"baseFingerprint": strings.Repeat("b", 64), "packages": map[string]string{"fixture-package": "1"},
		}},
	}
	f.config.ReleaseID, f.config.Version = "r3", "1.2.0"
	return f, request
}

func TestRepairAcceptanceAuthorizesOnlyExplicitFixedForwardTarget(t *testing.T) {
	f, request := repairPolicyFixture(t)
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := CheckRepairAcceptance(t.Context(), f.envelope(t), f.config, raw, f.root, policyTime)
	if err != nil || decision.Status != "accepted" || decision.Next == nil || decision.Next.ID != "r3" {
		t.Fatalf("signed manual repair was not accepted: %+v %v", decision, err)
	}
	if decision.Next.Components.SHA256 != fixtureArtifact("r3-components")["sha256"] {
		t.Fatal("repair selected a mutable/different component artifact")
	}
}

func TestRepairAcceptanceRejectsUnboundSourceFormatAndCurrentBundle(t *testing.T) {
	for _, scenario := range []string{"missing-contract", "wrong-source", "wrong-failed-target", "wrong-current", "wrong-format", "rag-lost", "wrong-abi"} {
		t.Run(scenario, func(t *testing.T) {
			f, request := repairPolicyFixture(t)
			switch scenario {
			case "missing-contract":
				f.policy["repairAcceptances"] = []any{}
			case "wrong-source":
				request["sourceReleaseId"] = "another-source"
			case "wrong-failed-target":
				request["failedTargetReleaseId"] = "r3"
			case "wrong-current":
				request["currentReleaseId"] = "r2"
			case "wrong-format":
				request["dataProfile"] = "another-format"
			case "rag-lost":
				request["components"] = map[string]string{}
				request["componentMetadata"] = map[string]any{}
			case "wrong-abi":
				request["componentMetadata"].(map[string]any)["rag"].(map[string]any)["pythonAbi"] = "cp312"
			}
			raw, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			decision, err := CheckRepairAcceptance(t.Context(), f.envelope(t), f.config, raw, f.root, policyTime)
			if err != nil {
				t.Fatalf("valid envelope with incompatible repair needs a bounded rejection: %v", err)
			}
			if decision.Status != "blocked" || decision.Reason != "repair_target_not_accepted" {
				t.Fatalf("unaccepted repair authorized: %+v", decision)
			}
		})
	}
}

func TestRepairAcceptanceDoesNotCreateAnAutomaticUpgradeEdge(t *testing.T) {
	f, _ := repairPolicyFixture(t)
	f.config.ReleaseID, f.config.Version = "r1", "1.0.0"
	f.policy["edges"] = []any{}
	decision, err := f.check(t)
	if err != nil || decision.Status != "manual_required" || decision.Reason != "no_compatible_path" {
		t.Fatalf("manual-only repair silently became OTA: %+v %v", decision, err)
	}
}
