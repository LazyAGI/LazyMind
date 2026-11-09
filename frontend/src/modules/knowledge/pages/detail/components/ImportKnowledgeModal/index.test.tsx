import { createRef } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import ImportKnowledgeModal, { type IImportKnowledgeModalRef } from "./index";

const mocks = vi.hoisted(() => ({
  availability: "missing",
  wait: vi.fn().mockResolvedValue(undefined),
  build: vi.fn().mockResolvedValue([{ upload_file_id: "upload-1" }]),
  create: vi.fn().mockResolvedValue({ data: { tasks: [{ task_id: "task-1" }] } }),
  start: vi.fn().mockResolvedValue({ data: {} }),
}));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("@/modules/modelProvider/contexts/RAGComponentContext", () => ({
  useRAGComponent: () => ({ availability: mocks.availability }),
}));
vi.mock("@/runtime/readiness", () => ({
  waitForRuntimeCapability: mocks.wait,
  RuntimeReadinessError: class extends Error {},
}));
vi.mock("@/components/request", () => ({ localizeErrorCode: (code: string) => code }));
vi.mock("@/modules/knowledge/utils/uploadByHash", () => ({ buildUploadTaskItems: mocks.build }));
vi.mock("@/modules/knowledge/utils/request", () => ({
  getKnowledgeOptions: vi.fn().mockResolvedValue({
    upload: {
      max_files: 20, max_file_bytes: 500 * 1024 * 1024,
      max_batch_bytes: 1024 * 1024 * 1024, file_types: ["pdf"],
    },
  }),
  DocumentServiceApi: () => ({ documentServiceAllDocumentTags: async () => ({ data: { tags: [] } }) }),
  TaskServiceApi: () => ({ createTasks: mocks.create, startTasks: mocks.start }),
}));
vi.mock("@/modules/knowledge/store/dataset_permission", () => ({
  useDatasetPermissionStore: (selector: (state: unknown) => unknown) => selector({
    hasOnlyReadPermission: () => false, hasUploadPermission: () => false, hasWritePermission: () => true,
  }),
}));
vi.mock("@/modules/knowledge/components/TagSelect", () => ({ default: () => null }));
vi.mock("../DragUpload", () => ({
  ALLOWED_FILE_TYPES: ["pdf"],
  default: ({ onChange }: { onChange: (files: unknown[]) => void }) => (
    <button type="button" onClick={() => onChange([{ originFile: new File(["PDF"], "paper.pdf"), path: "paper.pdf" }])}>select PDF</button>
  ),
}));
afterEach(() => { cleanup(); vi.clearAllMocks(); });

describe("storage-only uploads", () => {
  it.each(["create", "start"] as const)("clears upload activity when %s submission fails", async (phase) => {
    mocks[phase].mockRejectedValueOnce(new Error("提交失败"));
    const log = vi.spyOn(console, "error").mockImplementation(() => {});
    const onUploadSettled = vi.fn();
    const onOk = vi.fn();
    const ref = createRef<IImportKnowledgeModalRef>();
    try {
      render(<ImportKnowledgeModal ref={ref} onOk={onOk} onUploadSettled={onUploadSettled} />);
      await act(async () => ref.current!.handleOpen({ dataset_id: "papers" }));
      fireEvent.click(screen.getByText("select PDF"));
      fireEvent.click(screen.getByRole("button", { name: "上传并保存" }));
      await waitFor(() => expect(onUploadSettled).toHaveBeenCalledTimes(1));
      expect(onOk).not.toHaveBeenCalled();
      expect((await screen.findAllByText("提交失败")).length).toBeGreaterThan(0);
    } finally { log.mockRestore(); }
  });
  it.each(["create", "start"] as const)("keeps upload activity until %s submission finishes", async (phase) => {
    let finishSubmission!: (response: { data: { tasks: { task_id: string }[] } }) => void;
    mocks[phase].mockImplementationOnce(() => new Promise((resolve) => { finishSubmission = resolve; }));
    const onUploadStart = vi.fn();
    const onUploadSettled = vi.fn();
    const ref = createRef<IImportKnowledgeModalRef>();
    render(<ImportKnowledgeModal ref={ref} onOk={vi.fn()} onUploadStart={onUploadStart} onUploadSettled={onUploadSettled} />);
    await act(async () => ref.current!.handleOpen({ dataset_id: "papers" }));
    fireEvent.click(screen.getByText("select PDF"));
    fireEvent.click(screen.getByRole("button", { name: "上传并保存" }));
    await waitFor(() => expect(mocks[phase]).toHaveBeenCalled());
    expect(onUploadStart).toHaveBeenCalledTimes(1);
    expect(onUploadSettled).not.toHaveBeenCalled();
    await act(async () => finishSubmission({ data: { tasks: [{ task_id: "task-1" }] } }));
    await waitFor(() => expect(onUploadSettled).toHaveBeenCalledTimes(1));
  });
  it("reports browser upload activity before parsing is submitted", async () => {
    let finishUpload!: (items: { upload_file_id: string }[]) => void;
    mocks.build.mockImplementationOnce(() => new Promise((resolve) => { finishUpload = resolve; }));
    const onUploadStart = vi.fn();
    const onUploadSettled = vi.fn();
    const ref = createRef<IImportKnowledgeModalRef>();
    render(<ImportKnowledgeModal ref={ref} onOk={vi.fn()} onUploadStart={onUploadStart} onUploadSettled={onUploadSettled} />);
    await act(async () => ref.current!.handleOpen({ dataset_id: "papers" }));
    fireEvent.click(screen.getByText("select PDF"));
    fireEvent.click(screen.getByRole("button", { name: "上传并保存" }));
    await waitFor(() => expect(mocks.build).toHaveBeenCalled());
    expect(onUploadStart).toHaveBeenCalledTimes(1);
    expect(onUploadSettled).not.toHaveBeenCalled();
    await act(async () => finishUpload([{ upload_file_id: "upload-1" }]));
    await waitFor(() => expect(onUploadSettled).toHaveBeenCalledTimes(1));
  });
  it.each([
    ["missing", false],
    ["ready", true],
  ])("uploads without parser readiness when availability=%s, storageOnly=%s", async (availability, storageOnly) => {
    mocks.availability = availability as string;
    const ref = createRef<IImportKnowledgeModalRef>();
    const onOk = vi.fn();
    render(<ImportKnowledgeModal ref={ref} onOk={onOk} storageOnly={storageOnly as boolean} />);
    await act(async () => ref.current!.handleOpen({ dataset_id: "papers" }));
    expect(screen.getByText(storageOnly
      ? "文件将保留在知识库内，仅保存原文件，不解析、不建立索引。"
      : "无需安装 RAG 即可保存解析文本和基础切片。安装组件并配置 embedding 后自动建立索引；扫描件需要 OCR。")).toBeInTheDocument();
    fireEvent.click(screen.getByText("select PDF"));
    fireEvent.click(screen.getByRole("button", { name: "上传并保存" }));
    await waitFor(() => expect(onOk).toHaveBeenCalled());
    expect(mocks.wait).not.toHaveBeenCalled();
    expect(mocks.create).toHaveBeenCalledWith("papers", { items: [{ upload_file_id: "upload-1" }] });
    expect(mocks.start).toHaveBeenCalledWith("papers", { task_ids: ["task-1"], start_mode: "DEFAULT" });
  });
});
