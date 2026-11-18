package desktopupdate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNativeRepairRepairAdoptionMustStayAheadOfAlreadyAdoptedTarget(t *testing.T) {
	f := newNativeStateFixture(t)
	f.record["sourceReleaseId"], f.record["targetReleaseId"] = "r1", "r2"
	f.installing(t)
	f.command(t, "repair", map[string]any{"transactionId": f.tx, "reason": "component_target_verification_failed"}, "repair", "")
	adopt := func(id, version string) map[string]any {
		return map[string]any{"transactionId": f.tx, "sourceReleaseId": "r1", "failedTargetReleaseId": "r2", "acceptanceDigest": strings.Repeat("e", 64), "handoffTerminated": true, "repairTarget": map[string]any{"releaseId": id, "version": version, "packageSHA256": strings.Repeat("f", 64), "componentsSHA256": strings.Repeat("a", 64), "dataProfile": "format-v2-fixture"}}
	}
	f.command(t, "adopt-repair", adopt("repair-newer", "1.3.0"), "repair", "")
	f.command(t, "adopt-repair", adopt("repair-newer", "1.3.0"), "repair", "")
	f.command(t, "adopt-repair", adopt("repair-another", "1.3.0"), "blocked", "repair_target_not_accepted")
	// Current repair is 1.3.0, but a signed acceptance from original failed B
	// admits 1.2.0. The source and failed target of that contract stay immutable.
	p, request := repairPolicyFixture(t)
	request["sourceHadRAG"], request["components"], request["componentMetadata"] = false, map[string]string{}, map[string]any{}
	raw, _ := json.Marshal(request)
	decision, err := CheckRepairAcceptance(t.Context(), p.envelope(t), p.config, raw, p.root, policyTime)
	if err != nil || decision.Status != "accepted" {
		t.Fatalf("signed original-source acceptance control: %+v %v", decision, err)
	}
	t.Logf("signed contract accepted repair %s/%s after original failed B; already adopted version is 1.3.0", decision.Next.ID, decision.Next.Version)
	f.command(t, "adopt-repair", adopt(decision.Next.ID, decision.Next.Version), "blocked", "repair_target_not_accepted")
}
