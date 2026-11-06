package workflow

import (
	"testing"

	"lazymind/core/workflow/graphengine"
)

func TestExecutorHostForStep(t *testing.T) {
	cases := []struct {
		name         string
		node         graphengine.CompiledNode
		checks       []graphengine.PostStepCheck
		wantExternal bool
	}{
		{name: "prompt only", wantExternal: true},
		{name: "package tool", node: graphengine.CompiledNode{LegacyTools: []string{"package_tool"}}},
		{name: "platform tool", node: graphengine.CompiledNode{LegacyTools: []string{"image_generator"}}},
		{name: "generic tool", node: graphengine.CompiledNode{LegacyTools: []string{"web_search"}}},
		{name: "terminal tool", node: graphengine.CompiledNode{TerminalTools: []string{"package_tool"}}},
		{name: "post-step check", checks: []graphengine.PostStepCheck{{StepID: "step", Tool: "check_ready"}}},
		{name: "other step check", checks: []graphengine.PostStepCheck{{StepID: "other", Tool: "check_ready"}}, wantExternal: true},
		{name: "mode flags only", node: graphengine.CompiledNode{ToolsOnly: true, TerminalToolsOnly: true}, wantExternal: true},
		{name: "external policy with native tools and checks", node: graphengine.CompiledNode{
			LegacyTools: []string{"native_generator"}, TerminalTools: []string{"native_generator"}, ToolsOnly: true,
			ExternalExecution: &graphengine.ExternalStepExecution{Version: "1", ValidationOwner: "external-agent", Prompt: "Use external generation"},
		}, checks: []graphengine.PostStepCheck{{StepID: "step", Tool: "native_check"}}, wantExternal: true},
		{name: "unknown external version retains native routing", node: graphengine.CompiledNode{
			LegacyTools: []string{"native_generator"}, ExternalExecution: &graphengine.ExternalStepExecution{Version: "2", ValidationOwner: "external-agent", Prompt: "Generate"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.node.ID = "step"
			for _, controller := range []string{"external-agent", "lazymind"} {
				want := "lazymind"
				if controller == "external-agent" && tc.wantExternal {
					want = controller
				}
				got := executorHostForStep(controller, tc.node, graphengine.RuntimePolicy{PostStepChecks: tc.checks})
				if got != want {
					t.Fatalf("controller=%q host=%q, want %q", controller, got, want)
				}
			}
		})
	}
}
