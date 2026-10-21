import { createRef } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ConfigProvider, message } from "antd";

const api = vi.hoisted(() => ({ build: vi.fn(), create: vi.fn(), start: vi.fn() }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("@/components/request", () => ({ localizeErrorCode: (_code: string, fallback = "") => fallback || "import failed" }));
vi.mock("@/modules/knowledge/utils/request", () => ({
  DocumentServiceApi: () => ({ documentServiceAllDocumentTags: () => Promise.resolve({ data: { tags: [] } }) }),
  TaskServiceApi: () => ({ createTasks: api.create, startTasks: api.start }),
}));
vi.mock("@/modules/knowledge/utils/uploadByHash", () => ({ buildUploadTaskItems: api.build }));
vi.mock("@/runtime/readiness", () => ({ RuntimeReadinessError: class extends Error {}, waitForRuntimeCapability: () => Promise.resolve() }));
vi.mock("@/modules/knowledge/components/TagSelect", () => ({ default: () => null }));
vi.mock("../DragUpload", () => ({
  ALLOWED_FILE_TYPES: ["pdf"],
  default: ({ onChange, value }: { onChange: (value: unknown[]) => void; value?: unknown[] }) => <button type="button" onClick={() => onChange([{ originFile: new File(["pdf"], "paper.pdf"), path: "paper.pdf" }])}>{value?.length ? "paper.pdf" : "select a file"}</button>,
}));
vi.mock("@/modules/knowledge/store/dataset_permission", () => ({
  useDatasetPermissionStore: (selector: (state: unknown) => unknown) => selector({ hasOnlyReadPermission: () => false, hasUploadPermission: () => false, hasWritePermission: () => true }),
}));

import ImportKnowledgeModal, { type IImportKnowledgeModalRef } from "./index";

describe("ordinary import error reporting", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.build.mockResolvedValue([{ upload_file_id: "upload-1", task: { display_name: "paper.pdf" } }]);
    api.create.mockResolvedValue({ data: { tasks: [{ task_id: "task-1" }] } });
    vi.spyOn(message, "success").mockImplementation(() => (() => {}) as never);
    vi.spyOn(message, "error").mockImplementation(() => (() => {}) as never);
    vi.spyOn(console, "error").mockImplementation(() => {});
  });
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

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
