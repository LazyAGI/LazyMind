package desktopupdate

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestSignedForwardRepairMakesInterruptedInstallingReadyForStaging(t *testing.T) {
	for _, phase := range []string{"repair", "installing"} {
		t.Run(phase, func(t *testing.T) {
			f := newNativeStateFixture(t)
			f.record["sourceReleaseId"], f.record["targetReleaseId"] = "r1", "r2"
			f.create(t)
			stage := NativeComponentStage{Status: "prepared", TransactionID: f.tx, ComponentMode: "absent"}
			prepared := map[string]any{"transactionId": f.tx, "stage": stage}
			f.command(t, "prepared", prepared, "prepared", "")
			f.command(t, "installing", map[string]any{"transactionId": f.tx, "dataProfile": "format-v2-fixture", "componentMode": "absent", "sourcePaused": true, "sourceActive": 0}, "installing", "")
			if phase == "repair" {
				f.command(t, "repair", map[string]any{"transactionId": f.tx, "reason": "component_target_verification_failed"}, "repair", "")
			}
			current, err := ReadNativeRecord(f.root)
			if err != nil || current == nil || current.Phase != phase || current.ComponentStage == nil {
				t.Fatalf("invalid original stage: %+v %v", current, err)
			}
			p, req := repairPolicyFixture(t)
			req["sourceHadRAG"], req["components"], req["componentMetadata"] = false, map[string]string{}, map[string]any{}
			decision, err := CheckRepairAcceptance(t.Context(), p.envelope(t), p.config, nativeJSON(t, req), f.root, policyTime)
			if err != nil || decision.Status != "accepted" || decision.Next == nil {
				t.Fatalf("signed repair permission: %+v %v", decision, err)
			}
			target := decision.Next
			repair := NativeRepairTarget{ReleaseID: target.ID, Version: target.Version, PackageSHA256: target.Package.SHA256,
				ComponentsSHA256: target.Components.SHA256, DataProfile: current.SourceDataProfile}
			f.command(t, "adopt-repair", map[string]any{"transactionId": f.tx, "sourceReleaseId": current.SourceReleaseID,
				"failedTargetReleaseId": current.TargetReleaseID, "handoffTerminated": true,
				"acceptanceDigest": decision.PolicyDigest, "repairTarget": repair}, "repair", "")
			adopted, err := ReadNativeRecord(f.root)
			repair.AcceptanceDigest = decision.PolicyDigest
			if err != nil || adopted == nil || adopted.Phase != "repair" || adopted.ComponentStage != nil || !reflect.DeepEqual(adopted.RepairTarget, &repair) {
				t.Fatalf("adoption did not persist a clean repair phase: %+v %v", adopted, err)
			}
			f.assertOriginalBindings(t)
			f.command(t, "prepared", prepared, "prepared", "")
			staged, err := ReadNativeRecord(f.root)
			if err != nil || staged == nil || !reflect.DeepEqual(staged.ComponentStage, &stage) {
				t.Fatalf("new repair stage missing: %+v %v", staged, err)
			}
		})
	}
}

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
