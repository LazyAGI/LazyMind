import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import OrganizerSteps from "./OrganizerSteps";
import type { OrganizerRun } from "./api";

vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string, args?: {current: number; total: number}) => args ? `${key} ${args.current}/${args.total}` : key }) }));

const run = {
  steps: [
    { id: "snapshot", status: "completed", detail: "snapshotLocked", current: 0, total: 0, completed: 0 },
    { id: "preparation", status: "completed", detail: "reused", current: 0, total: 0, completed: 0 },
    { id: "organization", status: "active", detail: "auditing", current: 2, total: 3, completed: 1 },
    { id: "review", status: "pending", current: 0, total: 0, completed: 0 },
    { id: "application", status: "pending", current: 0, total: 0, completed: 0 },
  ],
} as OrganizerRun;

describe("organizer steps", () => {
  it("shows five steps and keeps scope review within organization", () => {
    const { unmount } = render(<OrganizerSteps run={run} />);
    for (const id of ["snapshot", "preparation", "organization", "review", "application"]) expect(screen.getByText(`conversationOrganizer.steps.${id}`)).toBeTruthy();
    expect(screen.getByText("conversationOrganizer.steps.organization").getAttribute("aria-current")).toBe("step");
    expect(screen.getByText("conversationOrganizer.steps.auditing")).toBeTruthy();
    expect(screen.getByText("conversationOrganizer.steps.reused")).toBeTruthy();
    expect(screen.getByText("conversationOrganizer.steps.snapshotLocked")).toBeTruthy();
    expect(screen.queryByText("conversationOrganizer.steps.reviewDetail")).toBeNull();
    unmount();
  });
  it("renders the server batch and terminal states without active progress", () => {
    const active = { ...run, steps: run.steps!.map(s => s.id === "organization" ? { ...s, detail: undefined } : s) };
    const { rerender, unmount } = render(<OrganizerSteps run={active} />);
    expect(screen.getByText("conversationOrganizer.steps.batch 2/3")).toBeTruthy();
    for (const status of ["failed", "canceled"] as const) {
      rerender(<OrganizerSteps run={{ ...run, steps: run.steps!.map(s => s.id === "organization" ? { ...s, status } : s) }} />);
      expect(screen.getByText(`conversationOrganizer.steps.${status}`)).toBeTruthy();
      expect(screen.queryByText("conversationOrganizer.steps.auditing")).toBeNull();
      expect(screen.queryByRole("progressbar")).toBeNull();
    }
    unmount();
  });
});
