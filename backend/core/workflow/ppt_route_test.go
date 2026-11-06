package workflow

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"lazymind/core/workflow/graphengine"
)

func TestPPTMaterialsRouteDirectlyToPresentationPlanning(t *testing.T) {
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
	decision := graphengine.DecideRoute(compiled.Graph, "collect_materials", nil)
	for _, removed := range []string{"analyze_academic_sources", "prepare_paper_reflection"} {
		if slices.Contains(decision.Activated, removed) || slices.Contains(decision.Bypassed, removed) {
			t.Fatalf("removed standalone step remains in route: %+v", decision)
		}
	}
	if !slices.Contains(decision.Activated, "plan_background_prompts") || !slices.Contains(decision.Activated, "build_outline") {
		t.Fatalf("background/outline routes must stay available: %+v", decision)
	}
}
