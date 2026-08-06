import { createRef } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, waitFor, screen, renderWithProviders } from "@/test/testUtils";
import RestartKnowledgeModal, {
  IRestartKnowledgeProps,
} from "./index";

const createTasksMock = vi.fn();
const startTasksMock = vi.fn();

vi.mock("@/modules/knowledge/utils/request", () => ({
  TaskServiceApi: () => ({
    createTasks: (...args: unknown[]) => createTasksMock(...args),
    startTasks: (...args: unknown[]) => startTasksMock(...args),
  }),
}));

vi.mock("@/runtime/readiness", () => ({
  RuntimeReadinessError: class RuntimeReadinessError extends Error {},
  waitForRuntimeCapability: vi.fn().mockResolvedValue(undefined),
}));

describe("RestartKnowledgeModal", () => {
  beforeEach(() => {
    createTasksMock.mockReset().mockResolvedValue({
      data: { tasks: [{ task_id: "t1" }] },
    });
    startTasksMock.mockReset().mockResolvedValue({ data: { started_count: 1 } });
  });

  it("is hidden until onOpen is called via ref", () => {
    const ref = createRef<IRestartKnowledgeProps>();
    renderWithProviders(<RestartKnowledgeModal ref={ref} onFinish={vi.fn()} />);

    expect(screen.queryByText("knowledge.reparseTarget")).not.toBeInTheDocument();
  });

  it("opens with the given title and defaults reparse_scope to rebuild", async () => {
    const ref = createRef<IRestartKnowledgeProps>();
    renderWithProviders(<RestartKnowledgeModal ref={ref} onFinish={vi.fn()} />);

    ref.current?.onOpen({
      dataset: "ds-1",
      ids: ["d1"],
      names: ["report.pdf"],
      title: "Reparse report.pdf",
    });

    await waitFor(() => {
      expect(screen.getByText("Reparse report.pdf")).toBeInTheDocument();
    });
    expect(screen.getByText("knowledge.reparseTarget")).toBeInTheDocument();
  });

  it("shows a required-target error and does not call the API when submitting without a selection", async () => {
    const ref = createRef<IRestartKnowledgeProps>();
    renderWithProviders(<RestartKnowledgeModal ref={ref} onFinish={vi.fn()} />);

    ref.current?.onOpen({
      dataset: "ds-1",
      ids: ["d1"],
      title: "Reparse",
    });

    await waitFor(() => {
      expect(screen.getByText("Reparse")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: "OK" }));

    await waitFor(() => {
      expect(createTasksMock).not.toHaveBeenCalled();
    });
  });

  it("calls onFinish via onCancel after a successful reparse when using the full rebuild scope", async () => {
    const onFinish = vi.fn();
    const ref = createRef<IRestartKnowledgeProps>();
    renderWithProviders(<RestartKnowledgeModal ref={ref} onFinish={onFinish} />);

    ref.current?.onOpen({
      dataset: "ds-1",
      ids: ["d1"],
      names: ["report.pdf"],
      title: "Reparse report.pdf",
    });

    await waitFor(() => {
      expect(screen.getByText("Reparse report.pdf")).toBeInTheDocument();
    });

    // Select "all" segment in the TreeSelect via its combobox input (the
    // first of the two comboxes on this modal; the second is the scope Select).
    const [treeSelectCombobox] = screen.getAllByRole("combobox");
    fireEvent.mouseDown(treeSelectCombobox);

    await waitFor(() => {
      expect(screen.getByText("knowledge.segmentAll")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText("knowledge.segmentAll"));

    fireEvent.click(screen.getByRole("button", { name: "OK" }));

    await waitFor(() => {
      expect(createTasksMock).toHaveBeenCalled();
    });
    const createArgs = createTasksMock.mock.calls[0];
    expect(createArgs[0]).toBe("ds-1");
    expect(createArgs[1].items[0].task.task_type).toBe("TASK_TYPE_REPARSE");

    await waitFor(() => {
      expect(startTasksMock).toHaveBeenCalledWith("ds-1", { task_ids: ["t1"] });
    });
    await waitFor(() => {
      expect(onFinish).toHaveBeenCalled();
    });
  });
});
