import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createRef } from "react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => ({ list: vi.fn(), resume: vi.fn() }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("@/components/request", () => ({ localizeErrorCode: (_code: string, fallback = "") => fallback || "import failed" }));
vi.mock("@/modules/knowledge/utils/request", () => ({ TaskServiceApi: () => ({ listTasks: api.list, resumeTask: api.resume }) }));
vi.mock("@/modules/knowledge/store/dataset_permission", () => ({
  useDatasetPermissionStore: (selector: (state: unknown) => unknown) => selector({ hasOnlyReadPermission: () => false, hasUploadPermission: () => false, hasWritePermission: () => true }),
}));

import ImportTaskList, { TaskTab } from "./index";
import ImportTaskManage, { type IImportTaskManageRef } from "../ImportTaskManage";

describe("import task error visibility", () => {
  beforeEach(() => { vi.clearAllMocks(); vi.spyOn(console, "error").mockImplementation(() => {}); });
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

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
