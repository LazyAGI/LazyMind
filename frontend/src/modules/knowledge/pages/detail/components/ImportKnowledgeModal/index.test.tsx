import { createRef } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, waitFor, screen, renderWithProviders } from "@/test/testUtils";
import ImportKnowledgeModal, { IImportKnowledgeModalRef } from "./index";

const allDocumentTagsMock = vi.fn();
const createTasksMock = vi.fn();
const startTasksMock = vi.fn();
const buildUploadTaskItemsMock = vi.fn();

vi.mock("@/modules/knowledge/utils/request", () => ({
  DocumentServiceApi: () => ({
    documentServiceAllDocumentTags: (...args: unknown[]) =>
      allDocumentTagsMock(...args),
  }),
  TaskServiceApi: () => ({
    createTasks: (...args: unknown[]) => createTasksMock(...args),
    startTasks: (...args: unknown[]) => startTasksMock(...args),
  }),
}));

vi.mock("@/modules/knowledge/utils/uploadByHash", () => ({
  buildUploadTaskItems: (...args: unknown[]) => buildUploadTaskItemsMock(...args),
}));

vi.mock("@/modules/knowledge/store/dataset_permission", () => ({
  useDatasetPermissionStore: (selector: (state: any) => unknown) =>
    selector({
      hasOnlyReadPermission: () => false,
      hasUploadPermission: () => false,
      hasWritePermission: () => true,
    }),
}));

vi.mock("@/runtime/readiness", () => ({
  RuntimeReadinessError: class RuntimeReadinessError extends Error {},
  waitForRuntimeCapability: vi.fn().mockResolvedValue(undefined),
}));

vi.mock("../DragUpload", () => ({
  __esModule: true,
  default: (props: { onChange?: (value: unknown[]) => void }) => (
    <button
      type="button"
      data-testid="drag-upload-stub"
      onClick={() =>
        props.onChange?.([
          { originFile: new File(["a"], "a.pdf"), path: "a.pdf" },
        ])
      }
    >
      upload
    </button>
  ),
  ALLOWED_FILE_TYPES: ["pdf"],
}));

describe("ImportKnowledgeModal", () => {
  beforeEach(() => {
    allDocumentTagsMock.mockReset().mockResolvedValue({ data: { tags: ["a"] } });
    buildUploadTaskItemsMock.mockReset().mockResolvedValue([{ upload_file_id: "u1" }]);
    createTasksMock.mockReset().mockResolvedValue({ data: { tasks: [{ task_id: "t1" }] } });
    startTasksMock.mockReset().mockResolvedValue({});
  });

  it("is hidden until handleOpen is called via ref", () => {
    const ref = createRef<IImportKnowledgeModalRef>();
    renderWithProviders(<ImportKnowledgeModal ref={ref} onOk={vi.fn()} />);

    expect(screen.queryByText("knowledge.importFileTitle")).not.toBeInTheDocument();
  });

  it("opens with the import file title when handleOpen is invoked", async () => {
    const ref = createRef<IImportKnowledgeModalRef>();
    renderWithProviders(<ImportKnowledgeModal ref={ref} onOk={vi.fn()} />);

    ref.current?.handleOpen({ dataset_id: "ds-1" });

    await waitFor(() => {
      expect(screen.getByText("knowledge.importFileTitle")).toBeInTheDocument();
    });
  });

  it("uploads files then creates and starts tasks, calling onOk with the parent id", async () => {
    const onOk = vi.fn();
    const ref = createRef<IImportKnowledgeModalRef>();
    renderWithProviders(<ImportKnowledgeModal ref={ref} onOk={onOk} />);

    ref.current?.handleOpen({ dataset_id: "ds-1", p_id: "parent-1" });

    await waitFor(() => {
      expect(screen.getByTestId("drag-upload-stub")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByTestId("drag-upload-stub"));
    fireEvent.click(screen.getByText("knowledge.parseAndImport"));

    await waitFor(() => {
      expect(buildUploadTaskItemsMock).toHaveBeenCalled();
    });
    await waitFor(() => {
      expect(createTasksMock).toHaveBeenCalledWith("ds-1", {
        items: [{ upload_file_id: "u1" }],
      });
    });
    await waitFor(() => {
      expect(startTasksMock).toHaveBeenCalledWith("ds-1", {
        task_ids: ["t1"],
        start_mode: "DEFAULT",
      });
    });
    await waitFor(() => {
      expect(onOk).toHaveBeenCalledWith({ pId: "parent-1" });
    });
  });
});
