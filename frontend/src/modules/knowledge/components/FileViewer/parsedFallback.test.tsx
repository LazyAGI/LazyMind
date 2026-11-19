import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import FileViewer from "./index";
const api = vi.hoisted(() => ({ status: vi.fn(), get: vi.fn(), post: vi.fn() }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("@/components/auth", () => ({ AgentAppsAuth: { getAuthHeaders: () => ({}) } }));
vi.mock("@/components/request", () => ({ BASE_URL: "", localizeErrorCode: (code: string) => code, axiosInstance: { get: api.get, post: api.post } }));
vi.mock("@/modules/knowledge/api/pdfArtifacts", () => ({ getDocumentParseStatus: api.status }));
vi.mock("@/modules/knowledge/utils/request", () => ({ normalizeProxyableUrl: (url: string) => url }));
vi.mock("@/components/ui", () => ({ RenderPdf: () => <p>Native PDF</p>, exportPdfAsImagePdf: vi.fn(), isLearningActionCompatible: () => false }));
vi.mock("./renderers", () => ({ RenderMarkdown: () => null, RenderTxt: () => null, RenderHtml: () => null, RenderWord: () => null, RenderExcel: () => null, RenderPpt: () => null }));
const props = { fileName: "Notebook.ipynb", file: "/original.ipynb", parsedFallbackTarget: { dataset: "library", document: "a" } };
beforeEach(() => {
  api.status.mockReset().mockResolvedValue({ status: "parsed" });
  api.get.mockReset().mockResolvedValue({ data: { data: { segments: [{ content: "Notebook parsed body" }] } } });
  api.post.mockReset();
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, arrayBuffer: async () => new ArrayBuffer(1) }));
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it("reads existing notebook text without fetching or executing the original", async () => {
  render(<FileViewer {...props} />);
  expect(await screen.findByText("Notebook parsed body")).toBeInTheDocument();
  expect(screen.getByText("knowledge.parsedPreviewHint")).toBeInTheDocument();
  expect(api.get).toHaveBeenCalledWith("/api/core/datasets/library/documents/a/segments", expect.objectContaining({ params: expect.objectContaining({ group: "block" }) }));
  expect(fetch).not.toHaveBeenCalled();
  expect(api.post).not.toHaveBeenCalled();
});

it("renders HTML and script text literally without creating active elements", async () => {
  const source = '<script>window.notebookRan=true</script><img src=x onerror="window.notebookRan=true">';
  api.get.mockResolvedValue({ data: { segments: [{ content: source }] } });
  const view = render(<FileViewer {...props} />);
  expect(await screen.findByText(source)).toBeInTheDocument();
  expect(view.container.querySelector("script, img, iframe")).toBeNull();
  expect((window as unknown as { notebookRan?: boolean }).notebookRan).toBeUndefined();
});

it.each(["not_requested", "parsing", "failed", "unknown"])("does not start work for %s documents", async status => {
  api.status.mockResolvedValue({ status });
  render(<FileViewer {...props} />);
  expect(await screen.findByText("knowledge.parsedPreviewUnavailable")).toBeInTheDocument();
  expect(api.get).not.toHaveBeenCalled();
  expect(api.post).not.toHaveBeenCalled();
});

it("explains a parsed document without readable blocks", async () => {
  api.get.mockResolvedValue({ data: { data: { segments: [{ content: " " }] } } });
  render(<FileViewer {...props} />);
  expect(await screen.findByText("knowledge.parsedPreviewEmpty")).toBeInTheDocument();
});

it.each(["resolve", "reject"])("ignores A's late segment %s after switching to B", async outcome => {
  let resolve!: (value: unknown) => void;
  let reject!: (error: Error) => void;
  api.get.mockReturnValueOnce(new Promise((accept, fail) => { resolve = accept; reject = fail; }))
    .mockResolvedValueOnce({ data: { data: { segments: [{ content: "B current text" }] } } });
  const view = render(<FileViewer {...props} />);
  await waitFor(() => expect(api.get).toHaveBeenCalledTimes(1));
  view.rerender(<FileViewer {...props} parsedFallbackTarget={{ dataset: "library", document: "b" }} />);
  expect(await screen.findByText("B current text")).toBeInTheDocument();
  await act(async () => { if (outcome === "resolve") resolve({ data: { data: { segments: [{ content: "A stale text" }] } } }); else reject(new Error("A failed")); });
  expect(screen.getByText("B current text")).toBeInTheDocument();
  expect(screen.queryByText("A stale text")).toBeNull();
  expect(screen.queryByText("knowledge.parsedPreviewFailed")).toBeNull();
});

it("loads later blocks only on request and retains earlier text", async () => {
  api.get.mockResolvedValueOnce({ data: { data: { segments: [{ content: "First page" }], next_page_token: "next" } } })
    .mockResolvedValueOnce({ data: { data: { segments: [{ content: "Second page" }] } } });
  render(<FileViewer {...props} />);
  expect(await screen.findByText("First page")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "knowledge.parsedPreviewMore" }));
  expect(await screen.findByText("Second page")).toBeInTheDocument();
  expect(screen.getByText("First page")).toBeInTheDocument();
  expect(api.get).toHaveBeenLastCalledWith(expect.any(String), expect.objectContaining({ params: expect.objectContaining({ page_token: "next" }) }));
});

it("keeps supported PDF previews on their native renderer", async () => {
  render(<FileViewer {...props} file="/original.pdf" fileName="paper.pdf" />);
  expect(await screen.findByText("Native PDF")).toBeInTheDocument();
  expect(api.status).not.toHaveBeenCalled();
  expect(api.get).not.toHaveBeenCalled();
});

it("does not fetch A's blocks when A's status arrives after switching to B", async () => {
  let resolveA!: (value: { status: string }) => void;
  api.status.mockReturnValueOnce(new Promise(accept => { resolveA = accept; }))
    .mockResolvedValueOnce({ status: "parsed" });
  const view = render(<FileViewer {...props} />);
  await waitFor(() => expect(api.status).toHaveBeenCalledTimes(1));
  view.rerender(<FileViewer {...props} parsedFallbackTarget={{ dataset: "library", document: "b" }} />);
  expect(await screen.findByText("Notebook parsed body")).toBeInTheDocument();
  await act(async () => { resolveA({ status: "parsed" }); });
  expect(api.get).toHaveBeenCalledTimes(1);
  expect(api.get).toHaveBeenCalledWith("/api/core/datasets/library/documents/b/segments", expect.anything());
});

it("shows a retryable message after an unavailable segment request", async () => {
  api.get.mockRejectedValueOnce(new Error("unavailable"));
  render(<FileViewer {...props} />);
  expect(await screen.findByRole("alert")).toHaveTextContent("knowledge.parsedPreviewFailed");
  fireEvent.click(screen.getByRole("button", { name: "knowledge.pdfChatRefreshStatus" }));
  expect(await screen.findByText("Notebook parsed body")).toBeInTheDocument();
  expect(api.post).not.toHaveBeenCalled();
});

it("does not automatically chase an empty page with a continuation token", async () => {
  api.get.mockResolvedValue({ data: { data: { segments: [], next_page_token: "next-empty" } } });
  render(<FileViewer {...props} />);
  expect(await screen.findByText("knowledge.parsedPreviewEmpty")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "knowledge.parsedPreviewMore" })).toBeEnabled();
  expect(api.get).toHaveBeenCalledTimes(1);
  expect(api.get).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ params: expect.objectContaining({ page_size: 100 }) }));
});

it("removes A's displayed text immediately while B's status is still pending", async () => {
  api.status.mockResolvedValueOnce({ status: "parsed" }).mockReturnValueOnce(new Promise(() => {}));
  const view = render(<FileViewer {...props} />);
  expect(await screen.findByText("Notebook parsed body")).toBeInTheDocument();
  view.rerender(<FileViewer {...props} parsedFallbackTarget={{ dataset: "library", document: "b" }} />);
  expect(screen.queryByText("Notebook parsed body")).toBeNull();
  expect(screen.getByRole("status")).toHaveTextContent("knowledge.dataLoading");
});
