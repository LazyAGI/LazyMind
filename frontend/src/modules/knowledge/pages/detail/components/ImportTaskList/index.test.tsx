import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { createRef } from "react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => ({ list: vi.fn() }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("@/components/request", () => ({ localizeErrorCode: (_code: string, fallback = "") => fallback || "import failed" }));
vi.mock("@/modules/knowledge/utils/request", () => ({ TaskServiceApi: () => ({ listTasks: api.list }) }));
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
});
