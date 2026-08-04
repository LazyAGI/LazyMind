import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import TaskCenterPage from "./index";

vi.mock("./index.scss", () => ({}));

vi.mock("./Workbench", () => ({
  default: ({ active }: { active: boolean }) => (
    <div data-testid="workbench">workbench-active:{String(active)}</div>
  ),
}));
vi.mock("./TaskList", () => ({
  default: ({ active }: { active: boolean }) => (
    <div data-testid="task-list">task-list-active:{String(active)}</div>
  ),
}));
vi.mock("./ScheduleList", () => ({
  default: ({ active }: { active: boolean }) => (
    <div data-testid="schedule-list">schedule-list-active:{String(active)}</div>
  ),
}));

describe("TaskCenterPage", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders the title, description, and workbench tab active by default", () => {
    renderWithProviders(<TaskCenterPage />);
    expect(screen.getByText("taskCenter.title")).toBeInTheDocument();
    expect(screen.getByText("taskCenter.description")).toBeInTheDocument();
    expect(screen.getByTestId("workbench")).toHaveTextContent("workbench-active:true");
  });

  it("switches to the all-tasks tab when clicked", () => {
    renderWithProviders(<TaskCenterPage />);
    fireEvent.click(screen.getByText("taskCenter.allTasks"));
    expect(screen.getByTestId("task-list")).toHaveTextContent("task-list-active:true");
  });

  it("switches to the schedules tab when clicked", () => {
    renderWithProviders(<TaskCenterPage />);
    fireEvent.click(screen.getByText("taskCenter.schedulePlans"));
    expect(screen.getByTestId("schedule-list")).toHaveTextContent("schedule-list-active:true");
  });
});
