import { describe, expect, it } from "vitest";

import { buildEvoProcessDashboard } from "./dashboard";
import { createThreadRestoreWorkflowRuntimeState } from "./runtimeState";

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
});
