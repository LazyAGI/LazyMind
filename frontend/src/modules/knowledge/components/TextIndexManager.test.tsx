import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider, Modal } from "antd";
import { afterEach, beforeAll, beforeEach, expect, it, vi } from "vitest";
import { installDesktopTestDOM } from "@/test/desktopResourceFixtures";
const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), clear: vi.fn(), rag: "ready", role: "system-admin" }));
vi.mock("@/components/request", () => ({ BASE_URL: "", axiosInstance: { get: mocks.get, post: mocks.post } }));
vi.mock("@/components/auth", () => ({ AgentAppsAuth: { getUserInfo: () => ({ role: mocks.role }) } }));
vi.mock("@/modules/modelProvider/api", () => ({ clearEmbeddingChunks: mocks.clear }));
vi.mock("@/modules/modelProvider/contexts/RAGComponentContext", () => ({ useRAGComponent: () => ({ availability: mocks.rag, refresh: vi.fn() }) }));
import TextIndexManager from "./TextIndexManager";
beforeAll(installDesktopTestDOM);
afterEach(() => { vi.restoreAllMocks(); Modal.destroyAll(); cleanup(); });
beforeEach(() => {
  installDesktopTestDOM(); vi.clearAllMocks(); mocks.role = "system-admin"; mocks.rag = "ready";
  mocks.get.mockImplementation(async (url: string) => ({ data: url.endsWith("index-state")
    ? { data: { generation: 7, model_name: "example-embedding", provider_name: "Example", indexed_knowledge_bases: 1, operation_phase: "ready", index_identity: "verified" } }
    : url.endsWith("/datasets") ? { datasets: [{ dataset_id: "library", display_name: "示例资料库" }] }
      : { documents: [{ id: "doc", name: "示例.pdf", parse_status: "succeeded", index_status: "pending" }], total: 1 } }));
  mocks.post.mockResolvedValue({ data: { tasks: [{ status: "STARTED" }] } });
});
async function open() {
  render(<ConfigProvider theme={{ token: { motion: false } }}><TextIndexManager /></ConfigProvider>);
  await act(async () => { fireEvent.click(screen.getByRole("button", { name: /索引管理/ })); });
  return screen.findByRole("dialog", { name: "文本向量索引管理" });
}
it("loads the selected library, keeps parsing visible, and submits only selected documents", async () => {
  const dialog = within(await open());
  await waitFor(() => expect(dialog.getByText("示例.pdf")).toBeVisible());
  expect(dialog.getByText("example-embedding")).toBeVisible();
  expect(dialog.getByRole("button", { name: "建立索引" })).toBeDisabled();
  fireEvent.click(within(dialog.getByRole("row", { name: /示例.pdf/ })).getByRole("checkbox"));
  fireEvent.click(dialog.getByRole("button", { name: "建立索引（1）" }));
  await waitFor(() => expect(mocks.post).toHaveBeenCalledWith("/api/core/datasets/library/text-index", { document_ids: ["doc"] }));
  expect(mocks.clear).not.toHaveBeenCalled();
});
it("keeps global cleanup explicit and explains what is retained", async () => {
  const confirm = vi.spyOn(Modal, "confirm").mockImplementation(() => ({ destroy: vi.fn(), update: vi.fn() }));
  const dialog = within(await open());
  await dialog.findByText("示例.pdf");
  fireEvent.click(dialog.getByRole("button", { name: /清理全部向量/ }));
  expect(confirm).toHaveBeenCalledWith(expect.objectContaining({
    title: "清除全部文本向量索引？",
    content: expect.stringContaining("原文件、解析文本、切片及图像向量均保留"),
    okButtonProps: { danger: true },
    onOk: expect.any(Function),
  }));
  expect(mocks.clear).not.toHaveBeenCalled();
});
it("shows an actionable empty selection and hides administrator cleanup for members", async () => {
  mocks.role = "member";
  mocks.get.mockResolvedValue({ data: { datasets: [], documents: [], total: 0 } });
  const dialog = within(await open());
  await waitFor(() => expect(dialog.getByText("选择资料库，查看文档的解析与索引状态")).toBeVisible());
  expect(dialog.getByRole("button", { name: "建立索引" })).toBeDisabled();
  expect(dialog.queryByRole("button", { name: /清理全部向量/ })).not.toBeInTheDocument();
});
it("explains retained old vectors when the model has been removed", async () => {
  const get = mocks.get.getMockImplementation()!;
  mocks.get.mockImplementation((url: string, ...args: unknown[]) => url.endsWith("index-state")
    ? Promise.resolve({ data: { data: { generation: 8, indexed_knowledge_bases: 1, operation_phase: "idle", index_identity: "legacy_unknown" } } })
    : get(url, ...args));
  const dialog = within(await open());
  expect(await dialog.findByText("旧向量索引待处理")).toBeInTheDocument();
  expect(dialog.getByText(/原文件、解析正文和旧向量保留/)).toBeInTheDocument();
  expect(dialog.getByRole("link", { name: /切换默认模型/ })).toBeInTheDocument();
  expect(await dialog.findByText("示例.pdf")).toBeInTheDocument();
});

it("keeps documents readable while missing RAG blocks only indexing with an installation link", async () => {
  mocks.rag = "missing";
  const dialog = within(await open());
  await waitFor(() => expect(dialog.getByText("示例.pdf")).toBeVisible());
  fireEvent.click(within(dialog.getByRole("row", { name: /示例.pdf/ })).getByRole("checkbox"));
  expect(dialog.getByRole("button", { name: "建立索引（1）" })).toBeDisabled();
  expect(dialog.getByRole("link", { name: "安装并启用组件" })).toHaveAttribute("href", "/settings?section=system_tools#python-rag-dependency");
  expect(mocks.post).not.toHaveBeenCalled();
});

it("opens from the library deep link and stays closed after dismissal", async () => {
  render(<ConfigProvider theme={{ token: { motion: false } }}><TextIndexManager autoOpen /></ConfigProvider>);
  const dialog = await screen.findByRole("dialog", { name: "文本向量索引管理" });
  await within(dialog).findByText("示例.pdf");
  fireEvent.click(within(dialog).getByRole("button", { name: "Close" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
});
