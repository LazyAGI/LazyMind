import { describe, expect, it } from "vitest";

import { buildEvoProcessDashboard } from "./dashboard";
import { createInitialWorkflowRuntimeState, createThreadRestoreWorkflowRuntimeState, getTerminalFlowStepStatus } from "./runtimeState";

describe("EVO dashboard status precedence", () => {
  it("shows a currently running repair even when history contains an older completion", () => {
    const dashboard = buildEvoProcessDashboard(
      [
        {
          key: "old-repair-completed",
          type: "done",
          stage: "repair",
          payload: { status: "completed", current_step: "repair" },
        },
      ],
      createThreadRestoreWorkflowRuntimeState(),
      true,
      undefined,
      { repair: "running" },
    );

    expect(
      dashboard.overview.find((item) => item.stage === "repair")?.step.status,
    ).toBe("running");
    expect(dashboard.activeStage).toBe("repair");
  });
  it("keeps a confirmed canceled task canceled even without stage events", () => {
    const dashboard = buildEvoProcessDashboard(
      [], createInitialWorkflowRuntimeState(), true, "canceled", { dataset: "running" },
    );
    expect(dashboard.overview[0].step.status).toBe("canceled");
    expect(dashboard.overview.slice(1).every(item => item.step.status === "pending")).toBe(true);
  });

  it("honors each completion status accepted by Core over stale running steps", () => {
    for (const status of ["ended", "completed", "succeeded"]) {
      const dashboard = buildEvoProcessDashboard(
        [], createInitialWorkflowRuntimeState(), true, getTerminalFlowStepStatus(status), { dataset: "running" },
      );
      expect(dashboard.overview[0].step.status).toBe("done");
    }
  });
});
