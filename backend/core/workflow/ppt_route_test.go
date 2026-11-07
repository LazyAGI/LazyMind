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

func TestPPTOutlineAlwaysPlansPromptsBeforeSlides(t *testing.T) {
	root := filepath.Join("../../../workflows", "ppt-workflow")
	read := func(path string) string {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	compiled := graphengine.Compile(read("workflow.yaml"), read("scenario/state.yml"), read("scenario/scenario.md"), graphengine.ProfilePublish)
	if !compiled.Valid || compiled.Graph == nil {
		t.Fatalf("invalid PPT package: %+v", compiled.Diagnostics)
	}
	following := graphengine.DecideRoute(compiled.Graph, "plan_page_prompts", nil)
	if len(following.Activated) != 1 || following.Activated[0] != "generate_ppt" {
		t.Fatalf("completed prompt planning must lead to slide generation: %+v", following)
	}
	for _, selected := range []string{"generate_ppt", "plan_page_prompts"} {
		t.Run(selected, func(t *testing.T) {
			decision := graphengine.SelectRouteTarget(compiled.Graph, "build_outline", selected, graphengine.DecideRoute(compiled.Graph, "build_outline", nil))
			if len(decision.Activated) != 1 || decision.Activated[0] != "plan_page_prompts" {
				t.Fatalf("outline must plan prompts even when an old client requests direct generation: %+v", decision)
			}
			projection := graphengine.Project(compiled.Graph, graphengine.RuntimeSnapshot{
				Attempts: []graphengine.AttemptFact{{StepID: "build_outline", Status: "succeeded", Validity: "effective"}, {StepID: "plan_page_prompts", Status: "running", Validity: "effective"}},
				Routes:   []graphengine.RouteFact{{From: "build_outline", Activated: decision.Activated, Pruned: decision.Pruned, Validity: "effective"}},
			})
			prompts := projection.Nodes["plan_page_prompts"]
			if prompts.Execution != "running" || prompts.Branch != "active" {
				t.Fatalf("prompt planning must execute before slide generation: %+v", prompts)
			}
			if slides := projection.Nodes["generate_ppt"]; slides.Branch != "active" || slides.Execution != "none" {
				t.Fatalf("slides must remain pending until prompt planning finishes: %+v", slides)
			}
		})
	}
}
