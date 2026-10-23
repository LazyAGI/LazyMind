import { AxiosError, type AxiosRequestConfig } from "axios";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  isArxivInput, listImportedArxivPapers, requestArxivMetadata,
  type ArxivImportResult, type ArxivMetadata,
} from "./arxivImport";

const transport = vi.hoisted(() => ({ adapter: vi.fn() }));
vi.mock("@/components/request", async () => {
  const axiosModule = await import("axios");
  return {
    BASE_URL: "https://api.example.test/base",
    axiosInstance: axiosModule.default.create({ adapter: transport.adapter }),
  };
});

const metadata: ArxivMetadata = {
  title: "A paper with a real title", authors: ["First Author", "Second Author"],
  year: 2024, abstract: "A verified abstract.", venue: "arXiv", base_id: "2404.06654",
  version: "v3", abs_url: "https://arxiv.org/abs/2404.06654v3", pdf_url: "https://arxiv.org/pdf/2404.06654v3",
};
const imported: ArxivImportResult = {
  paper: metadata, work_id: "work-1", document_id: "document-1", document_task_id: "task-1",
  filename: "2404.06654v3.pdf", size: 123456, source_url: metadata.pdf_url, source_version: "v3",
  content_sha256: "a".repeat(64), status: "imported", processing_status: "pending",
};

function respond(data: unknown, status = 200) {
  transport.adapter.mockImplementationOnce(async (config) => ({ data, status, statusText: "OK", headers: {}, config }));
}

function fail(status: number, data: unknown) {
  transport.adapter.mockImplementationOnce(async (config) => {
    throw new AxiosError("Request failed", AxiosError.ERR_BAD_RESPONSE, config, undefined, {
      data, status, statusText: "Error", headers: {}, config,
    });
  });
}

function requestConfig(): AxiosRequestConfig & { silentError?: boolean } {
  return transport.adapter.mock.calls[transport.adapter.mock.calls.length - 1]![0];
}

beforeEach(() => vi.resetAllMocks());

describe("arXiv import API contract", () => {
  it("looks up the original input through the authenticated backend and unwraps its envelope", async () => {
    const input = "  https://arxiv.org/pdf/2404.06654v3.pdf?download=1#page=4  ";
    respond({ code: 0, message: "success", data: { paper: metadata } });
    await expect(requestArxivMetadata(input)).resolves.toEqual(metadata);
    const config = requestConfig();
    expect(config).toMatchObject({ method: "post", url: "https://api.example.test/base/api/core/academic/arxiv:lookup", timeout: 60_000, silentError: true });
    expect(JSON.parse(String(config.data))).toEqual({ input });
  });

  it("lists backend-persisted papers scoped to the requested dataset", async () => {
    respond({ code: 0, data: { items: [imported] } });
    await expect(listImportedArxivPapers("dataset / 论文")).resolves.toEqual([imported]);
    expect(requestConfig()).toMatchObject({ method: "get", url: "https://api.example.test/base/api/core/academic/arxiv/library", params: { target_dataset_id: "dataset / 论文" }, timeout: 30_000, silentError: true });
  });

  it("accepts an explicitly empty backend library", async () => {
    respond({ data: { items: [] } });
    await expect(listImportedArxivPapers("empty")).resolves.toEqual([]);
  });

  it("restores a persisted PDF whose background processing failed without treating it as ready", async () => {
    const result = { ...imported, processing_status: "failed", processing_error: "文档解析任务启动失败" };
    respond({ data: { items: [result] } });
    await expect(listImportedArxivPapers("dataset-1")).resolves.toEqual([result]);
  });

  it.each([
    [404, "ARXIV_NOT_FOUND", "未找到这篇 arXiv 论文"],
    [502, "ARXIV_UPSTREAM_FAILED", "arXiv 服务暂时不可用"],
    [413, "ARXIV_PDF_TOO_LARGE", "PDF 超出大小限制"],
  ])("preserves HTTP %i server message and code", async (status, code, message) => {
    fail(status, { code, message, data: null });
    await expect(listImportedArxivPapers("dataset-1")).rejects.toMatchObject({ message, code, status });
  });

  it("preserves a structured nested HTTP error", async () => {
    fail(403, { error: { code: "DATASET_WRITE_FORBIDDEN", message: "没有写入论文库的权限" } });
    await expect(listImportedArxivPapers("dataset-1")).rejects.toMatchObject({ code: "DATASET_WRITE_FORBIDDEN", message: "没有写入论文库的权限", status: 403 });
  });

  it("retains stored document identities in a processing-start error", async () => {
    const detail = { document_id: "document-1", document_task_id: "task-1" };
    fail(502, { code: 2000725, message: "PDF 已保存，文档解析启动失败，请重试任务", detail });
    await expect(listImportedArxivPapers("dataset-1")).rejects.toMatchObject({
      code: 2000725, message: "PDF 已保存，文档解析启动失败，请重试任务", status: 502, detail,
    });
  });

  it("rejects a business error delivered with HTTP 200", async () => {
    respond({ code: "ARXIV_INVALID_INPUT", message: "arXiv 编号格式无效", data: null });
    await expect(requestArxivMetadata("bad input")).rejects.toMatchObject({ code: "ARXIV_INVALID_INPUT", message: "arXiv 编号格式无效" });
  });

  it("does not turn a timeout into a draft paper or an empty library", async () => {
    transport.adapter.mockRejectedValueOnce(new AxiosError("timeout of 60000ms exceeded", "ECONNABORTED"));
    await expect(requestArxivMetadata("2404.06654")).rejects.toMatchObject({ code: "ECONNABORTED", message: "timeout of 60000ms exceeded" });
  });

  it.each([
    ["lookup", () => requestArxivMetadata("2404.06654")],
    ["library", () => listImportedArxivPapers("dataset-1")],
  ])("rejects HTTP 200 HTML on the %s endpoint", async (_name, request) => {
    respond("<!doctype html><html><title>LazyMind</title></html>");
    await expect(request()).rejects.toMatchObject({ code: "ARXIV_INVALID_RESPONSE" });
  });

  it.each([
    { paper: { ...metadata, title: "" } },
    { paper: { ...metadata, authors: [null] } },
    { paper: { ...metadata, year: "2024" } },
    { paper: { ...metadata, base_id: undefined } },
    { paper: { ...metadata, version: undefined } },
    { paper: { ...metadata, pdf_url: undefined } },
    { paper: null },
  ])("rejects incomplete metadata without silently fabricating fields", async (body) => {
    respond({ data: body });
    await expect(requestArxivMetadata("2404.06654")).rejects.toMatchObject({ code: "ARXIV_INVALID_RESPONSE" });
  });

  it.each([
    { ...imported, document_id: "" },
    { ...imported, work_id: undefined },
    { ...imported, size: 0 },
    { ...imported, content_sha256: "" },
    { ...imported, status: "failed" },
    { ...imported, source_version: undefined },
    { ...imported, processing_status: undefined },
  ])("requires a persisted PDF identity when restoring the library", async (body) => {
    respond({ data: { items: [body] } });
    await expect(listImportedArxivPapers("dataset-1")).rejects.toMatchObject({ code: "ARXIV_INVALID_RESPONSE" });
  });

  it.each([{ data: {} }, { data: { items: null } }, { data: { items: [imported, { paper: metadata }] } }])("rejects an incomplete library instead of silently dropping records", async (body) => {
    respond(body);
    await expect(listImportedArxivPapers("dataset-1")).rejects.toMatchObject({ code: "ARXIV_INVALID_RESPONSE" });
  });
});

describe("arXiv candidate routing", () => {
  it.each([
    "2404.06654", "2404.06654v3", "2404.06654v3.pdf", "arXiv:2404.06654v3 [cs.CL]",
    "hep-th/9901001", "math.GT/0309136v1", "https://arxiv.org/abs/2404.06654",
    "https://arxiv.org/pdf/2404.06654v3.pdf?download=1#page=2", "https://arxiv.org/html/2404.06654v3",
    "https://export.arxiv.org/abs/hep-th/9901001", "https://doi.org/10.48550/arXiv.2404.06654",
    "[RULER](https://arxiv.org/abs/2404.06654)", "arxiv.org/pdf/2404.06654",
  ])("routes %s to backend parsing", (input) => {
    expect(isArxivInput(input)).toBe(true);
  });

  it.each(["10.18653/v1/n19-1423", "https://example.com/paper/2404.06654", "https://arxiv.org.evil.test/pdf/2404.06654", "https://evil-arxiv.org/pdf/2404.06654", "ordinary article title", ""]) ("does not mistake %s for arXiv", (input) => {
    expect(isArxivInput(input)).toBe(false);
  });
});
