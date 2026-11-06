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

func TestPPTOutlineChoiceRecordsSkippedPromptPlanning(t *testing.T) {
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
	for _, selected := range []string{"generate_ppt", "plan_page_prompts"} {
		t.Run(selected, func(t *testing.T) {
			decision := graphengine.SelectRouteTarget(compiled.Graph, "build_outline", selected, graphengine.DecideRoute(compiled.Graph, "build_outline", nil))
			if len(decision.Activated) != 1 || decision.Activated[0] != selected {
				t.Fatalf("outline must select one path, got %+v", decision)
			}
			projection := graphengine.Project(compiled.Graph, graphengine.RuntimeSnapshot{
				Attempts: []graphengine.AttemptFact{{StepID: "build_outline", Status: "succeeded", Validity: "effective"}, {StepID: selected, Status: "running", Validity: "effective"}},
				Routes:   []graphengine.RouteFact{{From: "build_outline", Activated: decision.Activated, Pruned: decision.Pruned, Validity: "effective"}},
			})
			prompts := projection.Nodes["plan_page_prompts"]
			if selected == "generate_ppt" {
				if prompts.Execution != "none" || prompts.Branch != "pruned" {
					t.Fatalf("quick path must explicitly skip prompt planning: %+v", prompts)
				}
			} else if prompts.Execution != "running" || prompts.Branch != "active" {
				t.Fatalf("full/template path must execute prompt planning: %+v", prompts)
			}
			if slides := projection.Nodes["generate_ppt"]; slides.Branch != "active" {
				t.Fatalf("slides remain on both paths, even before prompt planning finishes: %+v", slides)
			}
		})
	}
}
