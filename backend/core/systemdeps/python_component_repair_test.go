package systemdeps

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeRepairAdoptedRepairCanReplaceBoundCorruptTarget(t *testing.T) {
	f := newMaintenanceFixture(t, false)
	f.record["phase"] = "repair"
	f.record["repairTarget"] = map[string]any{"releaseId": "target", "targetComponent": f.target, "componentsPath": f.record["componentsPath"], "componentsSHA256": f.record["componentsSHA256"], "componentsSize": f.record["componentsSize"], "acceptanceDigest": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}
	f.save(t)
	damaged := pythonComponentDir(f.runtimeRoot, f.target)
	writeMaintenanceFile(t, filepath.Join(damaged, ".ready"), []byte("corrupt"))
	before, _ := os.ReadFile(filepath.Join(damaged, ".ready"))
	result := f.run(t, "stage", "prepared", "", true)
	t.Logf("stage=%v requests=%d originalCorruptMarker=%s", result, f.requests, before)
	retained, err := filepath.Glob(filepath.Join(f.runtimeRoot, "deps", "python-components", ".native-stages", f.record["transactionId"].(string), "preserved", f.target.Revision+"-*", ".ready"))
	if err != nil || len(retained) != 1 {
		t.Fatalf("damaged evidence not preserved: %v %v", retained, err)
	}
	if raw, err := os.ReadFile(retained[0]); err != nil || string(raw) != string(before) {
		t.Fatal("original evidence changed")
	}
	if f.requests != 1 {
		t.Fatalf("fixed target download count=%d", f.requests)
	}
	f.record["componentStage"] = result
	f.save(t)
	buildMaintenanceProbe(t, f)
	f.run(t, "activate", "activated", "", true)
	if err := readMaintenanceSelection(f.runtimeRoot, f.target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.probeLog); err != nil {
		t.Fatal("repaired payload skipped target probe")
	}
	if raw, err := os.ReadFile(retained[0]); err != nil || string(raw) != string(before) {
		t.Fatal("activation removed retained evidence")
	}
}

func TestNativeRepairCannotMoveUnacceptedOrAliasedTarget(t *testing.T) {
	for _, scenario := range []string{"ordinary-update", "unaccepted-repair", "linked-target"} {
		t.Run(scenario, func(t *testing.T) {
			f := newMaintenanceFixture(t, false)
			dir := pythonComponentDir(f.runtimeRoot, f.target)
			if scenario != "ordinary-update" {
				f.record["phase"] = "repair"
				f.record["repairTarget"] = map[string]any{"releaseId": "target", "targetComponent": f.target, "componentsPath": f.record["componentsPath"], "componentsSHA256": f.record["componentsSHA256"], "componentsSize": f.record["componentsSize"], "acceptanceDigest": "unaccepted"}
			}
			if scenario == "linked-target" {
				f.record["repairTarget"].(map[string]any)["acceptanceDigest"] = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
				if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), dir); err != nil {
					t.Fatal(err)
				}
			} else {
				writeMaintenanceFile(t, filepath.Join(dir, ".ready"), []byte("corrupt"))
			}
			f.save(t)
			f.run(t, "stage", "blocked", "component_state_unknown", scenario != "ordinary-update")
			if _, err := os.Lstat(dir); err != nil || f.requests != 0 {
				t.Fatal("unsafe repair moved or downloaded a target")
			}
		})
	}
}
