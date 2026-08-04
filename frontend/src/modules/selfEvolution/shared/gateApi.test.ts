import { describe, expect, it, vi, beforeEach } from "vitest";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));

vi.mock("@/components/request", () => ({
  BASE_URL: "https://example.com",
  axiosInstance: { get: getMock },
}));

import {
  buildThreadGateDownloadUrl,
  buildThreadGatesListUrl,
  buildThreadGateVersionUrl,
  fetchThreadGateContent,
  fetchThreadGateDownload,
  normalizeGateContentResponse,
  normalizeThreadGateRecord,
  resolveThreadGateVersion,
} from "./gateApi";
import { AGENT_API_BASE } from "./constants";

beforeEach(() => {
  getMock.mockReset();
});

describe("normalizeThreadGateRecord", () => {
  it("parses versions and version fields from a raw gate record", () => {
    const gate = normalizeThreadGateRecord({
      step: "dataset", artifact_id: "a1", versions: [1, 2, "3", "bad"], effective_version: 2, latest_version: 3,
    });
    expect(gate).toEqual({ step: "dataset", artifactId: "a1", versions: [1, 2, 3], effectiveVersion: 2, latestVersion: 3 });
  });

  it("returns undefined when the step field is missing", () => {
    expect(normalizeThreadGateRecord({ versions: [] })).toBeUndefined();
    expect(normalizeThreadGateRecord("not-a-record")).toBeUndefined();
  });
});

describe("resolveThreadGateVersion", () => {
  it("prefers effectiveVersion, then latestVersion, then the max of versions", () => {
    expect(resolveThreadGateVersion({ step: "s", versions: [1, 2], effectiveVersion: 5 })).toBe(5);
    expect(resolveThreadGateVersion({ step: "s", versions: [1, 2], latestVersion: 4 })).toBe(4);
    expect(resolveThreadGateVersion({ step: "s", versions: [1, 3, 2] })).toBe(3);
  });

  it("returns undefined when there is no positive version anywhere", () => {
    expect(resolveThreadGateVersion({ step: "s", versions: [] })).toBeUndefined();
  });
});

describe("URL builders", () => {
  it("builds the gates list/version/download urls with encoded segments", () => {
    expect(buildThreadGatesListUrl("t 1")).toBe(`${AGENT_API_BASE}/threads/t%201/gates`);
    expect(buildThreadGateVersionUrl("t1", "dataset", 2)).toBe(`${AGENT_API_BASE}/threads/t1/gates/dataset/versions/2`);
    expect(buildThreadGateDownloadUrl("t1", "dataset", 2)).toBe(`${AGENT_API_BASE}/threads/t1/gates/dataset/versions/2:download`);
  });
});

describe("normalizeGateContentResponse", () => {
  it("unwraps the content field when present", () => {
    expect(normalizeGateContentResponse({ content: { a: 1 } })).toEqual({ a: 1 });
  });

  it("returns the value unchanged when there is no content field or it isn't a record", () => {
    expect(normalizeGateContentResponse({ a: 1 })).toEqual({ a: 1 });
    expect(normalizeGateContentResponse("raw")).toBe("raw");
  });
});

describe("fetchThreadGateContent", () => {
  it("resolves the latest version from the gates list, then fetches its content", async () => {
    getMock.mockResolvedValueOnce({ data: { gates: [{ step: "dataset", versions: [1, 2] }] } });
    getMock.mockResolvedValueOnce({ data: { content: { ok: true } } });
    const result = await fetchThreadGateContent("t1", "datasets");
    expect(result).toEqual({ ok: true });
    expect(getMock).toHaveBeenCalledTimes(2);
  });

  it("uses an explicit version without listing gates first", async () => {
    getMock.mockResolvedValueOnce({ data: { content: "raw" } });
    const result = await fetchThreadGateContent("t1", "datasets", { version: 5 });
    expect(result).toBe("raw");
    expect(getMock).toHaveBeenCalledTimes(1);
  });

  it("throws a 404-shaped error when the matching gate step can't be found", async () => {
    getMock.mockResolvedValueOnce({ data: { gates: [] } });
    await expect(fetchThreadGateContent("t1", "datasets")).rejects.toMatchObject({ response: { status: 404 } });
  });

  it("rejects for an unsupported result kind", async () => {
    await expect(fetchThreadGateContent("t1", "unknown-kind" as never)).rejects.toThrow("unsupported result kind");
  });
});

describe("fetchThreadGateDownload", () => {
  it("resolves the version and downloads the blob content", async () => {
    getMock.mockResolvedValueOnce({ data: { gates: [{ step: "dataset", versions: [1] }] } });
    getMock.mockResolvedValueOnce({ data: "blob-content" });
    const result = await fetchThreadGateDownload("t1", "datasets");
    expect(result).toBe("blob-content");
  });

  it("throws a 404-shaped error when no matching gate has a resolvable version", async () => {
    getMock.mockResolvedValueOnce({ data: { gates: [] } });
    await expect(fetchThreadGateDownload("t1", "datasets")).rejects.toMatchObject({ response: { status: 404 } });
  });
});
