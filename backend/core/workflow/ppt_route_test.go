package workflow

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"lazymind/core/workflow/graphengine"
)

func TestPPTGeneralScenarioBypassesAcademicAnalysis(t *testing.T) {
	root := filepath.Join("../../../workflows", "ppt-workflow")
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	compiled := graphengine.Compile(read("workflow.yaml"), read("scenario/state.yml"), read("scenario/scenario.md"), graphengine.ProfilePublish)
	if !compiled.Valid || compiled.Graph == nil {
		t.Fatalf("invalid PPT package: %+v", compiled.Diagnostics)
	}
	for _, general := range []bool{true, false} {
		var materials []graphengine.MaterialValue
		if general {
			materials = append(materials, graphengine.MaterialValue{MaterialID: "skip_academic_sources", RevisionID: "general", Valid: true})
		}
		decision := graphengine.DecideRoute(compiled.Graph, "collect_materials", materials)
		if slices.Contains(decision.Activated, "analyze_academic_sources") == general {
			t.Fatalf("general=%v: wrong academic activation: %+v", general, decision)
		}
		if slices.Contains(decision.Bypassed, "analyze_academic_sources") != general {
			t.Fatalf("general=%v: wrong academic bypass: %+v", general, decision)
		}
		if !slices.Contains(decision.Activated, "plan_background_prompts") || !slices.Contains(decision.Activated, "build_outline") {
			t.Fatalf("background/outline routes must stay available: %+v", decision)
		}
	}
}
