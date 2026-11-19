import { cleanup, render, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { PublicProcessStep } from "@/modules/chat/types/ordinaryTask";
import { TaskProcessTimeline } from "./TaskProcessTimeline";

vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
afterEach(cleanup);

it("stops stale process spinners on pause, preserves finished steps and resumes correctly", () => {
  const steps: PublicProcessStep[] = (["running", "pending", "succeeded", "failed", "canceled"] as const).map((status, order) => ({
    step_id: status, revision: 1, order, title: status, status,
    started_at: null, finished_at: null, elapsed_ms: null,
  }));
  const view = render(<TaskProcessTimeline steps={steps} state="running" />);
  expect(view.container.querySelectorAll(".ordinary-task-spinner")).toHaveLength(1);
  view.rerender(<TaskProcessTimeline steps={steps} state="paused" />);
  expect(view.container.querySelector(".ordinary-task-spinner")).toBeNull();
  expect(view.container.querySelector('[aria-current="step"]')).toBeNull();
  const items = view.getAllByRole("listitem");
  for (const index of [0, 1]) expect(within(items[index]).getByText("taskCenter.statusPaused")).toBeInTheDocument();
  for (const [index, status] of [[2, "Succeeded"], [3, "Failed"], [4, "Canceled"]] as const) {
    expect(within(items[index]).getByText(`taskCenter.status${status}`)).toBeInTheDocument();
  }
  view.rerender(<TaskProcessTimeline steps={steps} state="running" />);
  expect(view.container.querySelectorAll(".ordinary-task-spinner")).toHaveLength(1);
  expect(steps[0].status).toBe("running");
});

it("keeps measured estimates for ordinary tasks and completes only on success", () => {
  const plan = ["Read", "Check", "Write", "Publish"];
  const view = render(<TaskProcessTimeline steps={[]} legacyPlan={plan} state="running" progress={25} />);
  expect(view.getByRole("progressbar")).toHaveAttribute("value", "25");
  expect(view.getByText("Check").closest("li")).toHaveAttribute("aria-current", "step");
  view.rerender(<TaskProcessTimeline steps={[]} legacyPlan={plan} state="failed" progress={100} />);
  expect(view.getByRole("progressbar")).toHaveAttribute("value", "99");
  expect(view.getByText("Publish").closest("li")).toHaveClass("is-failed");
  view.rerender(<TaskProcessTimeline steps={[]} legacyPlan={plan} state="complete" />);
  expect(view.getByRole("progressbar")).toHaveAttribute("value", "100");
  expect(view.container.querySelectorAll(".ordinary-plan-item.is-complete")).toHaveLength(4);
});
