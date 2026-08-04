import { createRef } from "react";
import { describe, it, expect, vi } from "vitest";
import { fireEvent, waitFor, screen, renderWithProviders } from "@/test/testUtils";
import ImportTaskManage, { IImportTaskManageRef } from "./index";

vi.mock("../ImportTaskList", () => ({
  __esModule: true,
  default: (props: {
    datasetId?: string;
    onClose: () => void;
    onSuspendSuccess?: () => void;
  }) => (
    <div>
      <span data-testid="import-task-list-dataset">{props.datasetId}</span>
      <button onClick={() => props.onClose()}>close-list</button>
      <button onClick={() => props.onSuspendSuccess?.()}>suspend-one</button>
    </div>
  ),
}));

describe("ImportTaskManage", () => {
  it("is closed by default and does not render the task list", () => {
    renderWithProviders(<ImportTaskManage onClose={vi.fn()} />);

    expect(screen.queryByTestId("import-task-list-dataset")).not.toBeInTheDocument();
  });

  it("opens the drawer with the given dataset id when handleOpen is called", async () => {
    const ref = createRef<IImportTaskManageRef>();
    renderWithProviders(<ImportTaskManage ref={ref} onClose={vi.fn()} />);

    ref.current?.handleOpen({ dataset_id: "ds-1" });

    await waitFor(() => {
      expect(screen.getByTestId("import-task-list-dataset")).toHaveTextContent("ds-1");
    });
  });

  it("calls onClose with false when closed without any suspended task", async () => {
    const onClose = vi.fn();
    const ref = createRef<IImportTaskManageRef>();
    renderWithProviders(<ImportTaskManage ref={ref} onClose={onClose} />);

    ref.current?.handleOpen({ dataset_id: "ds-1" });
    await waitFor(() => {
      expect(screen.getByTestId("import-task-list-dataset")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText("close-list"));

    expect(onClose).toHaveBeenCalledWith(false);
  });

  it("calls onClose with true when a task was suspended before closing", async () => {
    const onClose = vi.fn();
    const ref = createRef<IImportTaskManageRef>();
    renderWithProviders(<ImportTaskManage ref={ref} onClose={onClose} />);

    ref.current?.handleOpen({ dataset_id: "ds-1" });
    await waitFor(() => {
      expect(screen.getByTestId("import-task-list-dataset")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText("suspend-one"));
    fireEvent.click(screen.getByText("close-list"));

    expect(onClose).toHaveBeenCalledWith(true);
  });
});
