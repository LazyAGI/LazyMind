import { describe, expect, it, vi, beforeEach } from "vitest";

const { getMock, postMock, putMock, deleteMock, getLocalizedErrorMessageMock } = vi.hoisted(() => ({
  getMock: vi.fn(),
  postMock: vi.fn(),
  putMock: vi.fn(),
  deleteMock: vi.fn(),
  getLocalizedErrorMessageMock: vi.fn(() => "本地化错误"),
}));

vi.mock("@/components/request", () => ({
  BASE_URL: "https://example.com",
  axiosInstance: { get: getMock, post: postMock, put: putMock, delete: deleteMock },
  getLocalizedErrorMessage: getLocalizedErrorMessageMock,
}));

import {
  deleteRouterAlgorithm,
  fetchRouterABStrategy,
  fetchRouterAlgorithms,
  fetchRouterStatus,
  getRouterApiErrorMessage,
  normalizeRouterABStrategy,
  normalizeRouterAlgorithm,
  normalizeRouterAlgorithmList,
  normalizeRouterStatus,
  putRouterABStrategy,
  registerRouterAlgorithm,
  runRouterAlgorithmAction,
} from "./routerApi";
import { AGENT_API_BASE } from "./constants";

beforeEach(() => {
  getMock.mockReset();
  postMock.mockReset();
  putMock.mockReset();
  deleteMock.mockReset();
});

describe("normalizeRouterAlgorithm / normalizeRouterAlgorithmList", () => {
  it("normalizes a single algorithm record with defaults", () => {
    const algo = normalizeRouterAlgorithm({ algorithm_id: "a1", healthy_instances: 2, instance_count: 3, owner: { thread_id: "t1" } });
    expect(algo).toMatchObject({ algorithm_id: "a1", status: "missing", healthy_instances: 2, instance_count: 3, owner: { thread_id: "t1" } });
  });

  it("returns undefined when the algorithm_id is missing", () => {
    expect(normalizeRouterAlgorithm({ status: "active" })).toBeUndefined();
  });

  it("normalizes an items list, dropping invalid entries", () => {
    const list = normalizeRouterAlgorithmList({ items: [{ algorithm_id: "a1" }, { status: "bad" }] });
    expect(list).toHaveLength(1);
  });

  it("returns an empty array when items is missing", () => {
    expect(normalizeRouterAlgorithmList({})).toEqual([]);
  });
});

describe("normalizeRouterStatus", () => {
  it("normalizes nested algorithms and ab_strategy sub-objects", () => {
    const status = normalizeRouterStatus({
      status: "ok", router_admin_url: "u", algorithms: { evo_owned: 1, active: 2, healthy: 2 },
      ab_strategy: { active: true, id: 5, weights: { a: 50, b: "bad" } },
    });
    expect(status).toEqual({
      status: "ok", router_admin_url: "u", algorithms: { evo_owned: 1, active: 2, healthy: 2 },
      ab_strategy: { active: true, id: 5, weights: { a: 50 } },
    });
  });

  it("returns null for non-record input", () => {
    expect(normalizeRouterStatus("nope")).toBeNull();
  });
});

describe("normalizeRouterABStrategy", () => {
  it("normalizes updated_by and preserves the router_response", () => {
    const strategy = normalizeRouterABStrategy({
      active: true, id: 1, weights: { a: 10 }, updated_by: { thread_id: "t1", reason: "manual" }, router_response: { ok: true },
    });
    expect(strategy?.updated_by).toEqual({ thread_id: "t1", candidate_ref: undefined, reason: "manual" });
    expect(strategy?.router_response).toEqual({ ok: true });
  });

  it("returns null for non-record input", () => {
    expect(normalizeRouterABStrategy(null)).toBeNull();
  });
});

describe("getRouterApiErrorMessage", () => {
  it("prefers a structured detail.message from the response", () => {
    const message = getRouterApiErrorMessage({ response: { data: { detail: { message: "详情错误" } } } }, "fallback");
    expect(message).toBe("详情错误");
  });

  it("falls back to the localized error message, then the provided fallback", () => {
    expect(getRouterApiErrorMessage({ message: "boom" }, "fallback")).toBe("本地化错误");
    getLocalizedErrorMessageMock.mockReturnValueOnce("");
    expect(getRouterApiErrorMessage({}, "fallback")).toBe("fallback");
  });
});

describe("router api request helpers", () => {
  it("fetchRouterStatus calls the status endpoint and normalizes the result", async () => {
    getMock.mockResolvedValueOnce({ data: { status: "ok", algorithms: {}, ab_strategy: {} } });
    const status = await fetchRouterStatus();
    expect(getMock).toHaveBeenCalledWith(`${AGENT_API_BASE}/router/status`, { silentError: true });
    expect(status?.status).toBe("ok");
  });

  it("fetchRouterAlgorithms passes thread/algorithm filters as query params", async () => {
    getMock.mockResolvedValueOnce({ data: { items: [] } });
    await fetchRouterAlgorithms({ threadId: "t1", algorithmId: "a1", status: "active" });
    expect(getMock).toHaveBeenCalledWith(`${AGENT_API_BASE}/router/algorithms`, {
      params: { status: "active", thread_id: "t1", algorithm_id: "a1" },
      silentError: true,
    });
  });

  it("registerRouterAlgorithm posts the payload and returns response data", async () => {
    postMock.mockResolvedValueOnce({ data: { algorithm_id: "a1" } });
    const result = await registerRouterAlgorithm({ algorithm_id: "a1", code_path: "/x", owner: { thread_id: "t1" } });
    expect(result).toEqual({ algorithm_id: "a1" });
  });

  it("runRouterAlgorithmAction includes wait_ready_seconds only when positive", async () => {
    postMock.mockResolvedValueOnce({ data: {} });
    await runRouterAlgorithmAction("a1", "restart", 30);
    expect(postMock).toHaveBeenCalledWith(
      `${AGENT_API_BASE}/router/algorithms/a1/action`,
      { action: "restart", wait_ready_seconds: 30 },
      { silentError: true },
    );
  });

  it("deleteRouterAlgorithm calls delete with the encoded algorithm id", async () => {
    deleteMock.mockResolvedValueOnce({ data: { ok: true } });
    await deleteRouterAlgorithm("a 1");
    expect(deleteMock).toHaveBeenCalledWith(`${AGENT_API_BASE}/router/algorithms/a%201`, { silentError: true });
  });

  it("fetchRouterABStrategy / putRouterABStrategy normalize their responses", async () => {
    getMock.mockResolvedValueOnce({ data: { active: false, id: null, weights: {}, updated_by: {} } });
    const strategy = await fetchRouterABStrategy();
    expect(strategy?.active).toBe(false);

    putMock.mockResolvedValueOnce({ data: { active: true, id: 2, weights: {}, updated_by: {} } });
    const updated = await putRouterABStrategy({ weights: { a: 1 } });
    expect(updated?.id).toBe(2);
  });
});
