package workflow

import (
	"lazymind/core/workflow/graphengine"
	"os"
	"path/filepath"
	"testing"
)

func TestPaperWorkflowPackagesCompile(t *testing.T) {
	for _, id := range []string{"paper_expansion", "paper_workflow_mock", "paper_ppt_with_reflection_mock"} {
		t.Run(id, func(t *testing.T) {
			root := filepath.Join("../../../workflows", id)
			w, err := os.ReadFile(filepath.Join(root, "workflow.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			state, err := os.ReadFile(filepath.Join(root, "scenario/state.yml"))
			if err != nil {
				t.Fatal(err)
			}
			result := graphengine.Compile(string(w), string(state), "", graphengine.ProfilePublish)
			if result.Graph == nil || len(result.Diagnostics) > 0 {
				t.Fatalf("invalid workflow: %+v", result.Diagnostics)
			}
		})
	}
}
