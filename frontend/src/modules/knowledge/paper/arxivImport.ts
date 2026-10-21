import type { AxiosRequestConfig, AxiosResponse } from "axios";
import { axiosInstance, BASE_URL } from "@/components/request";

export interface ArxivMetadata {
  title: string;
  authors: string[];
  year: number;
  abstract: string;
  doi?: string;
  venue: string;
  base_id: string;
  version: string;
  abs_url: string;
  pdf_url: string;
}

export interface ArxivImportResult {
  paper: ArxivMetadata;
  work_id: string;
  document_id: string;
  document_task_id?: string;
  filename: string;
  size: number;
  source_url: string;
  source_version: string;
  content_sha256: string;
  status: "imported" | "already_present";
  processing_status: string;
  processing_error?: string;
}

export class ArxivImportError extends Error {
  readonly code?: string | number;
  readonly status?: number;
  readonly detail?: unknown;
  readonly cause?: unknown;

  constructor(message: string, options: { code?: string | number; status?: number; detail?: unknown; cause?: unknown } = {}) {
    super(message);
    this.name = "ArxivImportError";
    this.code = options.code;
    this.status = options.status;
    this.detail = options.detail;
    this.cause = options.cause;
  }
}

const endpoint = `${BASE_URL}/api/core/academic`;
const baseIdPattern = /^(?:\d{4}\.\d{4,5}|[a-z][a-z.-]*\/\d{7})$/i;
const versionPattern = /^v[1-9]\d*$/;

function record(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}

function nonempty(value: unknown): value is string {
  return typeof value === "string" && value.trim().length > 0;
}

function errorCode(value: unknown): string | number | undefined {
  return (typeof value === "number" && Number.isFinite(value)) || nonempty(value) ? value as string | number : undefined;
}

function failureDetails(value: unknown) {
  const body = record(value);
  const nested = record(body?.error);
  const data = record(body?.data);
  const detail = record(body?.detail);
  const candidates = [body, nested, detail, data, record(data?.error)];
  const messages = candidates.flatMap((candidate) => [candidate?.message, candidate?.detail]);
  const codes = candidates.flatMap((candidate) => [candidate?.code, candidate?.error_code, candidate?.err_code, candidate?.err_msg]);
  return {
    message: messages.find(nonempty),
    code: codes.map(errorCode).find((code) => code !== undefined),
    detail: body?.detail ?? body?.data ?? body,
  };
}

function failure(error: unknown): ArxivImportError {
  if (error instanceof ArxivImportError) return error;
  const value = record(error);
  const response = record(value?.response);
  const server = failureDetails(response?.data);
  return new ArxivImportError(server.message || (nonempty(value?.message) ? value.message : "arXiv 请求失败，请重试"), {
    code: server.code ?? errorCode(value?.code),
    status: typeof response?.status === "number" ? response.status : undefined,
    detail: server.detail,
    cause: error,
  });
}

function invalidResponse(): never {
  throw new ArxivImportError("arXiv 服务返回的数据不完整或格式无效，请重试", { code: "ARXIV_INVALID_RESPONSE" });
}

function unwrap(payload: unknown): Record<string, unknown> {
  const body = record(payload);
  if (!body) return invalidResponse();
  const code = errorCode(body.code ?? body.error_code ?? body.err_code);
  if ((code !== undefined && !["0", "200"].includes(String(code))) || body.success === false || record(body.error)) {
    const server = failureDetails(body);
    throw new ArxivImportError(server.message || "arXiv 请求失败，请重试", { code: server.code, detail: server.detail });
  }
  if (Object.prototype.hasOwnProperty.call(body, "data")) {
    const data = record(body.data);
    if (!data) return invalidResponse();
    return data;
  }
  return body;
}

function officialUrl(value: unknown, path: "abs" | "pdf"): boolean {
  if (!nonempty(value)) return false;
  try {
    const url = new URL(value);
    return url.protocol === "https:" && /^(?:www\.|export\.)?arxiv\.org$/i.test(url.hostname)
      && !url.username && !url.password && url.pathname.startsWith(`/${path}/`);
  } catch {
    return false;
  }
}

function metadata(value: unknown): ArxivMetadata {
  const paper = record(value);
  if (!paper || !nonempty(paper.title) || !Array.isArray(paper.authors) || !paper.authors.length || !paper.authors.every(nonempty)
    || typeof paper.year !== "number" || !Number.isSafeInteger(paper.year) || paper.year <= 0
    || typeof paper.abstract !== "string" || !nonempty(paper.venue)
    || (paper.doi !== undefined && typeof paper.doi !== "string")
    || !nonempty(paper.base_id) || !baseIdPattern.test(paper.base_id)
    || !nonempty(paper.version) || !versionPattern.test(paper.version)
    || !officialUrl(paper.abs_url, "abs") || !officialUrl(paper.pdf_url, "pdf")) return invalidResponse();
  return paper as unknown as ArxivMetadata;
}

function importResult(value: unknown): ArxivImportResult {
  const result = record(value);
  if (!result) return invalidResponse();
  metadata(result.paper);
  if (!nonempty(result.work_id) || !nonempty(result.document_id) || !nonempty(result.filename)
    || (result.document_task_id !== undefined && typeof result.document_task_id !== "string")
    || typeof result.size !== "number" || !Number.isSafeInteger(result.size) || result.size <= 0
    || !officialUrl(result.source_url, "pdf") || !nonempty(result.source_version) || !versionPattern.test(result.source_version)
    || !nonempty(result.content_sha256) || !/^[a-f0-9]{64}$/i.test(result.content_sha256)
    || (result.status !== "imported" && result.status !== "already_present") || !nonempty(result.processing_status)
    || (result.processing_error !== undefined && typeof result.processing_error !== "string")) return invalidResponse();
  return result as unknown as ArxivImportResult;
}

function requestOptions(timeout: number): AxiosRequestConfig & { silentError: true } {
  return { timeout, silentError: true };
}

async function request<T>(send: () => Promise<AxiosResponse<unknown>>, parse: (body: Record<string, unknown>) => T): Promise<T> {
  try {
    const response = await send();
    return parse(unwrap(response.data));
  } catch (error) {
    throw failure(error);
  }
}

export function requestArxivMetadata(input: string): Promise<ArxivMetadata> {
  return request(() => axiosInstance.post(`${endpoint}/arxiv:lookup`, { input }, requestOptions(60_000)), (body) => metadata(body.paper));
}

export function importArxivPaper(input: string, datasetId: string): Promise<ArxivImportResult> {
  return request(() => axiosInstance.post(`${endpoint}/arxiv:import`, { input, target_dataset_id: datasetId }, requestOptions(200_000)), importResult);
}

export function listImportedArxivPapers(datasetId: string): Promise<ArxivImportResult[]> {
  return request(() => axiosInstance.get(`${endpoint}/arxiv/library`, { ...requestOptions(30_000), params: { target_dataset_id: datasetId } }), (body) => {
    if (!Array.isArray(body.items)) return invalidResponse();
    return body.items.map(importResult);
  });
}

/** A routing hint only. The backend parses and validates the original input. */
export function isArxivInput(input: string): boolean {
  const value = input.trim();
  return /(?:^|[\s(<])(?:https?:\/\/)?(?:www\.|export\.)?arxiv\.org(?:[/?#]|$)/i.test(value)
    || /(?:^|[\s(<])arxiv\s*:/i.test(value)
    || /(?:^|[\s(/])10\.48550\/arxiv\./i.test(value)
    || /^(?:\d{4}\.\d{4,5}|[a-z][a-z.-]*\/\d{7})(?:v\d+)?(?:\.pdf)?(?:\s+\[[^\]]+\])?$/i.test(value);
}
