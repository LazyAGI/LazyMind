import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createRef, useRef, type ReactNode } from "react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// Keep children mounted through the exit animation, which jsdom otherwise skips.
vi.mock("antd", async (importOriginal) => ({
  ...await importOriginal<typeof import("antd")>(),
  Drawer: ({ open, children }: { open: boolean; children: ReactNode }) => {
    const opened = useRef(false);
    if (open) opened.current = true;
    return opened.current ? <div>{children}</div> : null;
  },
}));

const api = vi.hoisted(() => ({ list: vi.fn(), resume: vi.fn() }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("@/components/request", () => ({ localizeErrorCode: (_code: string, fallback = "") => fallback || "import failed" }));
vi.mock("@/modules/knowledge/utils/request", () => ({ TaskServiceApi: () => ({ listTasks: api.list, resumeTask: api.resume }) }));
vi.mock("@/modules/knowledge/store/dataset_permission", () => ({
  useDatasetPermissionStore: (selector: (state: unknown) => unknown) => selector({ hasOnlyReadPermission: () => false, hasUploadPermission: () => false, hasWritePermission: () => true }),
}));

import ImportTaskList, { TaskTab } from "./index";
import { IMPORT_TASK_POLL_INTERVAL } from "@/modules/knowledge/constants/common";
import ImportTaskManage, { type IImportTaskManageRef } from "../ImportTaskManage";

describe("import task error visibility", () => {
  beforeEach(() => { vi.clearAllMocks(); vi.spyOn(console, "error").mockImplementation(() => {}); });
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it.each([TaskTab.Running, TaskTab.Successed, TaskTab.Failed])("does not request an empty dataset while closing tab %s", async (initialTab) => {
    api.list.mockImplementation((datasetId: string) => datasetId
      ? Promise.resolve({ data: { tasks: [], total_size: 0 } })
      : Promise.reject(new Error("dataset not found")));
    const ref = createRef<IImportTaskManageRef>();
    const onClose = vi.fn();
    render(<MemoryRouter><ImportTaskManage ref={ref} onClose={onClose} /></MemoryRouter>);
    act(() => ref.current?.handleOpen({ dataset_id: "library", initialTab }));
    await waitFor(() => expect(api.list).toHaveBeenCalledTimes(1));

    await act(async () => { fireEvent.click(screen.getByRole("img", { name: "close" })); });

    expect(onClose).toHaveBeenCalledTimes(1);
    expect(api.list.mock.calls.map(([datasetId]) => datasetId)).toEqual(["library"]);
    act(() => ref.current?.handleOpen({ dataset_id: "next-library", initialTab }));
    await waitFor(() => expect(api.list.mock.calls.map(([datasetId]) => datasetId)).toEqual(["library", "next-library"]));
  });

  it("does not request tasks without a dataset", () => {
    api.list.mockResolvedValue({ data: { tasks: [] } });
    render(<MemoryRouter><ImportTaskList datasetId="" onClose={() => {}} /></MemoryRouter>);
    expect(api.list).not.toHaveBeenCalled();
  });

  it("stops polling as soon as the drawer starts closing", async () => {
    vi.useFakeTimers();
    try {
      api.list.mockResolvedValue({ data: { tasks: [{ task_id: "running-task", task_state: "RUNNING" }], total_size: 1 } });
      const view = render(<MemoryRouter><ImportTaskList datasetId="library" active onClose={() => {}} /></MemoryRouter>);
      await act(async () => { await Promise.resolve(); });
      expect(api.list).toHaveBeenCalledTimes(1);
      view.rerender(<MemoryRouter><ImportTaskList datasetId="library" active={false} onClose={() => {}} /></MemoryRouter>);
      await act(async () => { await vi.advanceTimersByTimeAsync(IMPORT_TASK_POLL_INTERVAL * 2); });
      expect(api.list).toHaveBeenCalledTimes(1);
      view.unmount();
    } finally {
      vi.useRealTimers();
    }
  });

  it("ignores a failed request after the drawer starts closing", async () => {
    let rejectRequest!: (error: Error) => void;
    api.list.mockImplementation(() => new Promise((_resolve, reject) => { rejectRequest = reject; }));
    const { rerender } = render(<MemoryRouter><ImportTaskList datasetId="library" active initialTab={TaskTab.Failed} onClose={() => {}} /></MemoryRouter>);
    await waitFor(() => expect(api.list).toHaveBeenCalledTimes(1));
    rerender(<MemoryRouter><ImportTaskList datasetId="library" active={false} initialTab={TaskTab.Failed} onClose={() => {}} /></MemoryRouter>);
    await act(async () => rejectRequest(new Error("dataset not found")));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("shows a persistent error when silent polling fails", async () => {
    api.list.mockRejectedValue(new Error("import status service unavailable"));
    render(<MemoryRouter><ImportTaskList datasetId="library" onClose={() => {}} /></MemoryRouter>);
    expect(await screen.findByRole("alert")).toHaveTextContent("import status service unavailable");
  });

  it("opens the failed task tab when requested from an import failure", async () => {
    api.list.mockResolvedValue({ data: { tasks: [], total_size: 0 } });
    const ref = createRef<IImportTaskManageRef>();
    render(<MemoryRouter><ImportTaskManage ref={ref} onClose={() => {}} /></MemoryRouter>);
    act(() => ref.current?.handleOpen({ dataset_id: "library", initialTab: TaskTab.Failed }));
    await waitFor(() => expect(api.list).toHaveBeenCalledWith("library", expect.objectContaining({ taskStatus: "failed" }), undefined));
    expect(screen.getByRole("radio", { name: "knowledge.importFailedTitle" })).toBeChecked();
  });

  it("registers a successful retry with the parent even if it leaves the failed list immediately", async () => {
    const task = { task_id: "retry-task", display_name: "paper.pdf", target_pid: "folder", task_state: "FAILED", err_msg: "parser unavailable" };
    api.list.mockResolvedValueOnce({ data: { tasks: [task], total_size: 1 } }).mockResolvedValue({ data: { tasks: [], total_size: 0 } });
    api.resume.mockResolvedValue({ data: { tasks: [{ task_id: task.task_id, status: "STARTED" }] } });
    const onRetrySuccess = vi.fn();
    const ref = createRef<IImportTaskManageRef>();
    render(<MemoryRouter><ImportTaskManage ref={ref} onClose={() => {}} onRetrySuccess={onRetrySuccess} /></MemoryRouter>);
    act(() => ref.current?.handleOpen({ dataset_id: "library", initialTab: TaskTab.Failed }));

    fireEvent.click(await screen.findByText("knowledge.retry"));

    await waitFor(() => expect(onRetrySuccess).toHaveBeenCalledWith(expect.objectContaining({ task_id: "retry-task", display_name: "paper.pdf", target_pid: "folder" })));
    await waitFor(() => expect(screen.queryByText("knowledge.retry")).not.toBeInTheDocument());
  });

  it("does not register a rejected retry as a new attempt", async () => {
    api.list.mockResolvedValue({ data: { tasks: [{ task_id: "retry-task", task_state: "FAILED", err_msg: "first failure" }], total_size: 1 } });
    api.resume.mockRejectedValue(new Error("retry rejected"));
    const onRetrySuccess = vi.fn();
    render(<MemoryRouter><ImportTaskList datasetId="library" initialTab={TaskTab.Failed} onClose={() => {}} onRetrySuccess={onRetrySuccess} /></MemoryRouter>);

    fireEvent.click(await screen.findByText("knowledge.retry"));

    expect(await screen.findByRole("alert")).toHaveTextContent("retry rejected");
    expect(onRetrySuccess).not.toHaveBeenCalled();
  });
});
