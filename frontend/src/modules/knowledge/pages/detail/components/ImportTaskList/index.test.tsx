import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, waitFor, screen, renderWithProviders } from "@/test/testUtils";
import ImportTaskList from "./index";

const listTasksMock = vi.fn();
const suspendTaskMock = vi.fn();
const resumeTaskMock = vi.fn();
const deleteTaskMock = vi.fn();

vi.mock("@/modules/knowledge/utils/request", () => ({
  TaskServiceApi: () => ({
    listTasks: (...args: unknown[]) => listTasksMock(...args),
    suspendTask: (...args: unknown[]) => suspendTaskMock(...args),
    resumeTask: (...args: unknown[]) => resumeTaskMock(...args),
    deleteTask: (...args: unknown[]) => deleteTaskMock(...args),
  }),
}));

vi.mock("@/modules/knowledge/store/dataset_permission", () => ({
  useDatasetPermissionStore: (selector: (state: any) => unknown) =>
    selector({
      hasOnlyReadPermission: () => false,
      hasUploadPermission: () => false,
      hasWritePermission: () => true,
    }),
}));

describe("ImportTaskList", () => {
  beforeEach(() => {
    listTasksMock.mockReset().mockResolvedValue({
      data: {
        tasks: [
          { task_id: "t1", display_name: "file.pdf", create_time: 1700000000000 },
        ],
        total_size: 1,
      },
    });
    suspendTaskMock.mockReset().mockResolvedValue({});
    resumeTaskMock.mockReset().mockResolvedValue({});
    deleteTaskMock.mockReset().mockResolvedValue({});
  });

  it("loads and displays the running task list on mount", async () => {
    renderWithProviders(
      <ImportTaskList datasetId="ds-1" onClose={vi.fn()} />,
    );

    await waitFor(() => {
      expect(listTasksMock).toHaveBeenCalled();
    });
    expect(await screen.findByText("file.pdf")).toBeInTheDocument();
  });

  it("switches to the failed tab and refetches with the failed status", async () => {
    renderWithProviders(
      <ImportTaskList datasetId="ds-1" onClose={vi.fn()} />,
    );

    await waitFor(() => expect(listTasksMock).toHaveBeenCalled());
    listTasksMock.mockClear();

    fireEvent.click(screen.getByText("knowledge.importFailedTitle"));

    await waitFor(() => {
      expect(listTasksMock).toHaveBeenCalledWith(
        "ds-1",
        expect.objectContaining({ taskStatus: "failed" }),
        undefined,
      );
    });
  });

  it("calls onClose when the close icon is clicked", async () => {
    const onClose = vi.fn();
    const { container } = renderWithProviders(
      <ImportTaskList datasetId="ds-1" onClose={onClose} />,
    );

    await waitFor(() => expect(listTasksMock).toHaveBeenCalled());

    const closeIcon = container.querySelector(".closeIcon");
    expect(closeIcon).toBeTruthy();
    fireEvent.click(closeIcon!);
    expect(onClose).toHaveBeenCalled();
  });

  it("suspends a running task when the suspend action is clicked", async () => {
    renderWithProviders(
      <ImportTaskList datasetId="ds-1" onClose={vi.fn()} />,
    );

    await screen.findByText("file.pdf");

    fireEvent.click(screen.getByText("knowledge.suspend"));

    await waitFor(() => {
      expect(suspendTaskMock).toHaveBeenCalledWith("ds-1", "t1");
    });
  });
});
