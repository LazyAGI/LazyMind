import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import type { KnowledgeMarketTaskDetailOpenAPIResponse } from "@/api/generated/core-client";
import * as marketApi from "@/modules/knowledge/api/knowledgeMarket";
import KnowledgeMarketTaskModal from "./KnowledgeMarketTaskModal";

const cancelTask = vi.hoisted(() => vi.fn());
vi.mock("@/modules/knowledge/api/knowledgeMarket", () => ({
  getKnowledgeMarketTask: vi.fn(), listKnowledgeMarketTasks: vi.fn(),
  deleteKnowledgeMarketTask: vi.fn(), retryKnowledgeMarketTask: vi.fn(),
  cancelKnowledgeMarketTask: cancelTask,
}));
vi.mock("react-i18next", async () => {
  const { default: messages } = await import("@/i18n/locales/zh-CN");
  return { useTranslation: () => ({ t: (key: string, values: Record<string, unknown> = {}) => {
    const value = key.split(".").reduce<unknown>((part, name) =>
      part && typeof part === "object" ? (part as Record<string, unknown>)[name] : undefined, messages);
    return typeof value === "string" ? value.replace(/{{(\w+)}}/g, (_, name) => String(values[name] ?? "")) : key;
  } }) };
});

type ControlTask = KnowledgeMarketTaskDetailOpenAPIResponse & {
  display_state: string; can_cancel: boolean; can_retry: boolean; can_delete: boolean;
};
let current: ControlTask;
function fixture(): ControlTask {
  return {
    job_id: "control-job", job_type: "knowledge_market_install", job_status: "succeeded",
    market_item_id: "control-item", name: "测试资料库", dataset_id: "dataset",
    icon: "", install_state: "vectorizing", error_message: "", created_at: "2026-09-20T09:00:00Z",
    attempt_count: 1, max_attempts: 2, stage: "parsing", overall_percent: 60,
    progress: { current: 2, total: 2 },
    parse: { total: 2, pending: 2, parsing: 0, done: 0, failed: 0, state: "parsing" },
    display_state: "blocked", can_cancel: true, can_retry: false, can_delete: false,
  };
}
function listTasks(kind?: string) {
  return Promise.resolve({ items: kind === current.job_type ? [current] : [], total: kind === current.job_type ? 1 : 0, page: 1, page_size: 20 });
}
async function mount() {
  await act(async () => { render(
    <ConfigProvider button={{ autoInsertSpace: false }} theme={{ token: { motion: false } }}>
      <KnowledgeMarketTaskModal open refreshKey="control" onClose={() => {}} onTasksChanged={() => {}} />
    </ConfigProvider>,
  ); });
}
async function click(element: HTMLElement) { await act(async () => { fireEvent.click(element); }); }
function expectUnavailableAction(name: string | RegExp) {
  const button = screen.queryByRole("button", { name });
  if (button) expect(button).toBeDisabled();
}
beforeAll(() => {
  window.matchMedia = vi.fn().mockImplementation((media: string) => ({
    matches: false, media, onchange: null, addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  }));
  globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
});
beforeEach(() => {
  vi.useFakeTimers(); vi.clearAllMocks(); cancelTask.mockReset(); current = fixture();
  vi.mocked(marketApi.listKnowledgeMarketTasks).mockImplementation(listTasks);
  vi.mocked(marketApi.getKnowledgeMarketTask).mockImplementation(async () => current);
});
afterEach(() => { cleanup(); vi.useRealTimers(); });

describe("knowledge market task control", () => {
  it.each([
    ["pending", "排队中"], ["processing", "处理中"], ["blocked", "处理受阻"],
    ["unknown", "状态暂不可确认"], ["canceled", "已取消"], ["partial_canceled", "已结束（部分取消）"],
  ])("renders the authoritative %s state", async (state, label) => {
    current.display_state = state;
    if (state === "unknown") current.can_cancel = false;
    if (state === "processing") current.parse = { total: 2, pending: 1, parsing: 1, done: 0, failed: 0, state: "parsing" };
    if (["canceled", "partial_canceled"].includes(state)) {
      current.can_cancel = false; current.can_delete = true; current.stage = state; current.overall_percent = 100;
      current.parse = { total: 2, pending: 0, parsing: 0, done: state === "canceled" ? 0 : 1,
        failed: 0, state, ...{ canceled: state === "canceled" ? 2 : 1 } };
    }
    await mount();
    expect(screen.getByText(label)).toBeInTheDocument();
    expect(screen.queryByText("正在恢复")).toBeNull();
    expect(screen.queryByRole("button", { name: "恢复服务" })).toBeNull();
  });
  it("keeps blocked progress and checks status without resubmitting", async () => {
    await mount();
    expect(screen.getByText("60%")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "删除" })).toBeNull();
    expectUnavailableAction(/重试/);
    const previous = vi.mocked(marketApi.getKnowledgeMarketTask).mock.calls.length;
    await click(screen.getByRole("button", { name: "重新检查状态" }));
    expect(marketApi.getKnowledgeMarketTask).toHaveBeenCalledTimes(previous + 1);
    expect(marketApi.retryKnowledgeMarketTask).not.toHaveBeenCalled();
    expect(cancelTask).not.toHaveBeenCalled();
  });
  it("obeys can_cancel even when waiting files exist", async () => {
    current.can_cancel = false;
    await mount();
    expectUnavailableAction("取消未开始文件");
    expect(screen.getByText("处理受阻")).toBeInTheDocument();
    expect(cancelTask).not.toHaveBeenCalled();
  });
  it("confirms limited cancellation and reports a claim race", async () => {
    cancelTask.mockImplementation(async () => {
      current = { ...current, display_state: "processing", can_cancel: false,
        parse: { total: 2, pending: 0, parsing: 1, done: 0, failed: 0, state: "parsing", ...{ canceled: 1 } } };
      return { job_id: "control-job", canceled: 1, running: 1, unknown: 0 };
    });
    await mount();
    await click(screen.getByRole("button", { name: "取消未开始文件" }));
    expect(screen.getByText(/已开始的文件会继续处理/)).toBeInTheDocument();
    expect(screen.getByText(/成功内容保留/)).toBeInTheDocument();
    expect(cancelTask).not.toHaveBeenCalled();
    await click(screen.getByRole("button", { name: /^(确定|OK)$/ }));
    expect(cancelTask).toHaveBeenCalledTimes(1);
    expect(cancelTask).toHaveBeenCalledWith("control-job");
    expect(screen.getByText(/已取消\s*1\s*个/)).toBeInTheDocument();
    expect(screen.getByText(/1\s*个.*已开始/)).toBeInTheDocument();
    expect(screen.queryByText("已取消", { exact: true })).toBeNull();
    expect(screen.queryByRole("button", { name: "删除" })).toBeNull();
  });
  it("does not announce cancellation after an unavailable response", async () => {
    cancelTask.mockRejectedValue(new Error("unavailable"));
    await mount();
    await click(screen.getByRole("button", { name: "取消未开始文件" }));
    await click(screen.getByRole("button", { name: /^(确定|OK)$/ }));
    expect(cancelTask).toHaveBeenCalledTimes(1);
    expect(screen.getByText("测试资料库")).toBeInTheDocument();
    expect(screen.getByText("处理受阻")).toBeInTheDocument();
    expect(screen.queryByText("已取消", { exact: true })).toBeNull();
    expect(marketApi.deleteKnowledgeMarketTask).not.toHaveBeenCalled();
  });
  it("prevents a second cancellation while the first request is pending", async () => {
    let finish!: (value: { job_id: string; canceled: number; running: number; unknown: number }) => void;
    cancelTask.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    await mount();
    await click(screen.getByRole("button", { name: "取消未开始文件" }));
    await click(screen.getByRole("button", { name: /^(确定|OK)$/ }));
    expectUnavailableAction("取消未开始文件");
    expect(cancelTask).toHaveBeenCalledTimes(1);
    await act(async () => { finish({ job_id: "control-job", canceled: 0, running: 0, unknown: 2 }); });
    expect(cancelTask).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("已取消", { exact: true })).toBeNull();
    expect(screen.getByText(/2\s*个.*待确认/)).toBeInTheDocument();
    const previous = vi.mocked(marketApi.getKnowledgeMarketTask).mock.calls.length;
    await click(screen.getByRole("button", { name: "重新检查状态" }));
    expect(marketApi.getKnowledgeMarketTask).toHaveBeenCalledTimes(previous + 1);
    expect(cancelTask).toHaveBeenCalledTimes(1);
  });
  it("retains last known rows and supports read-only recheck after polling fails", async () => {
    await mount();
    vi.mocked(marketApi.listKnowledgeMarketTasks).mockRejectedValue(new Error("network"));
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(screen.getByText("测试资料库")).toBeInTheDocument();
    expect(screen.getByText("状态暂不可确认")).toBeInTheDocument();
    expectUnavailableAction("取消未开始文件");
    vi.mocked(marketApi.listKnowledgeMarketTasks).mockImplementation(listTasks);
    await click(screen.getByRole("button", { name: "重新检查状态" }));
    expect(screen.getByText("处理受阻")).toBeInTheDocument();
    expect(marketApi.retryKnowledgeMarketTask).not.toHaveBeenCalled();
  });
  it("allows failed-file retry only when the service capability permits it", async () => {
    current = { ...current, display_state: "partial_failed", stage: "partial_failed", overall_percent: 100,
      can_cancel: false, can_retry: false, can_delete: true,
      parse: { total: 2, done: 1, failed: 1, pending: 0, parsing: 0, state: "partial_failed" } };
    await mount();
    expectUnavailableAction(/重试/);
    expect(screen.getByText("已完成（部分文档失败）")).toBeInTheDocument();
    expect(marketApi.retryKnowledgeMarketTask).not.toHaveBeenCalled();
  });
  it("offers an explicit retry for terminal failed files", async () => {
    current = { ...current, display_state: "partial_failed", stage: "partial_failed", overall_percent: 100,
      can_cancel: false, can_retry: true, can_delete: true,
      parse: { total: 2, done: 1, failed: 1, pending: 0, parsing: 0, state: "partial_failed" } };
    vi.mocked(marketApi.retryKnowledgeMarketTask).mockResolvedValue({ job_id: "retry-job", state: "pending" });
    await mount();
    await click(screen.getByRole("button", { name: "重试失败文件" }));
    expect(marketApi.retryKnowledgeMarketTask).toHaveBeenCalledTimes(1);
    expect(marketApi.retryKnowledgeMarketTask).toHaveBeenCalledWith("control-job");
    expect(cancelTask).not.toHaveBeenCalled();
  });
});
