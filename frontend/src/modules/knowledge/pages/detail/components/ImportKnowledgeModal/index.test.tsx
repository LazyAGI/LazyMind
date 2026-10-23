import { createRef } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ConfigProvider, message } from "antd";

const api = vi.hoisted(() => ({ availability: "ready", wait: vi.fn(), build: vi.fn(), create: vi.fn(), start: vi.fn() }));
vi.mock("@/modules/modelProvider/contexts/RAGComponentContext", () => ({
  useRAGComponent: () => ({ availability: api.availability }),
}));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("@/components/request", () => ({ localizeErrorCode: (_code: string, fallback = "") => fallback || "import failed" }));
vi.mock("@/modules/knowledge/utils/request", () => ({
  DocumentServiceApi: () => ({ documentServiceAllDocumentTags: () => Promise.resolve({ data: { tags: [] } }) }),
  TaskServiceApi: () => ({ createTasks: api.create, startTasks: api.start }),
}));
vi.mock("@/modules/knowledge/utils/uploadByHash", () => ({ buildUploadTaskItems: api.build }));
vi.mock("@/runtime/readiness", () => ({ RuntimeReadinessError: class extends Error {}, waitForRuntimeCapability: api.wait }));
vi.mock("@/modules/knowledge/components/TagSelect", () => ({ default: () => null }));
vi.mock("../DragUpload", () => ({
  ALLOWED_FILE_TYPES: ["pdf"],
  default: ({ onChange, value }: { onChange: (value: unknown[]) => void; value?: unknown[] }) => <button type="button" onClick={() => onChange([{ originFile: new File(["pdf"], "paper.pdf"), path: "paper.pdf" }])}>{value?.length ? "paper.pdf" : "select a file"}</button>,
}));
vi.mock("@/modules/knowledge/store/dataset_permission", () => ({
  useDatasetPermissionStore: (selector: (state: unknown) => unknown) => selector({ hasOnlyReadPermission: () => false, hasUploadPermission: () => false, hasWritePermission: () => true }),
}));

import ImportKnowledgeModal, { type IImportKnowledgeModalRef } from "./index";

beforeEach(() => {
    vi.clearAllMocks();
    api.availability = "ready";
    api.wait.mockResolvedValue(undefined);
    api.start.mockResolvedValue({ data: {} });
    api.build.mockResolvedValue([{ upload_file_id: "upload-1", task: { display_name: "paper.pdf" } }]);
    api.create.mockResolvedValue({ data: { tasks: [{ task_id: "task-1" }] } });
    vi.spyOn(message, "success").mockImplementation(() => (() => {}) as never);
    vi.spyOn(message, "error").mockImplementation(() => (() => {}) as never);
    vi.spyOn(console, "error").mockImplementation(() => {});
  });
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe("ordinary import error reporting", () => {
  it("keeps the file and dialog open, showing the actual start failure without success callbacks", async () => {
    api.start.mockRejectedValue(new Error("parser unavailable (503)"));
    const onOk = vi.fn();
    const ref = createRef<IImportKnowledgeModalRef>();
    render(<ConfigProvider theme={{ token: { motion: false } }}><ImportKnowledgeModal ref={ref} onOk={onOk} /></ConfigProvider>);
    act(() => ref.current?.handleOpen({ dataset_id: "library" }));
    fireEvent.click(screen.getByRole("button", { name: "select a file" }));
    fireEvent.click(screen.getByRole("button", { name: "knowledge.parseAndImport" }));
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("parser unavailable (503)"));
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "paper.pdf" })).toBeInTheDocument();
    expect(message.success).not.toHaveBeenCalled();
    expect(onOk).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.getByRole("button", { name: "knowledge.parseAndImport" })).toBeEnabled());
  });

  it("passes started task references to monitoring even when conversion creates a replacement task", async () => {
    api.create.mockResolvedValue({ data: { tasks: [{ task_id: "original", document_id: "doc", target_pid: "folder", display_name: "paper.pdf" }] } });
    api.start.mockResolvedValue({ data: { tasks: [{ task_id: "replacement", document_id: "doc", status: "STARTED" }] } });
    const onOk = vi.fn();
    const ref = createRef<IImportKnowledgeModalRef>();
    render(<ConfigProvider theme={{ token: { motion: false } }}><ImportKnowledgeModal ref={ref} onOk={onOk} /></ConfigProvider>);
    act(() => ref.current?.handleOpen({ dataset_id: "library", p_id: "folder" }));
    fireEvent.click(screen.getByRole("button", { name: "select a file" }));
    fireEvent.click(screen.getByRole("button", { name: "knowledge.parseAndImport" }));
    await waitFor(() => expect(onOk).toHaveBeenCalledWith({ pId: "folder", tasks: [{ task_id: "replacement", target_pid: "folder", display_name: "paper.pdf" }] }));
  });
});


describe("storage-only uploads", () => {
  it.each([
    ["missing", false],
    ["ready", true],
  ])("uploads without parser readiness when availability=%s, storageOnly=%s", async (availability, storageOnly) => {
    api.availability = availability as string;
    api.build.mockResolvedValue([{ upload_file_id: "upload-1" }]);
    const ref = createRef<IImportKnowledgeModalRef>();
    const onOk = vi.fn();
    render(<ConfigProvider theme={{ token: { motion: false } }}><ImportKnowledgeModal ref={ref} onOk={onOk} storageOnly={storageOnly as boolean} /></ConfigProvider>);
    await act(async () => ref.current!.handleOpen({ dataset_id: "papers" }));
    expect(screen.getByText(storageOnly
      ? "文件将保留在知识库内，仅保存原文件，不解析、不建立索引。"
      : "无需安装 RAG 即可保存解析文本和基础切片。安装组件并配置 embedding 后自动建立索引；扫描件需要 OCR。")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "select a file" }));
    fireEvent.click(screen.getByRole("button", { name: "上传并保存" }));
    await waitFor(() => expect(onOk).toHaveBeenCalled());
    expect(api.wait).not.toHaveBeenCalled();
    expect(api.create).toHaveBeenCalledWith("papers", { items: [{ upload_file_id: "upload-1" }] });
    expect(api.start).toHaveBeenCalledWith("papers", { task_ids: ["task-1"], start_mode: "DEFAULT" });
  });
});
