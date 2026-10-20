import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { KnowledgeMarketTaskFileOpenAPIResponse, KnowledgeMarketTaskListItemOpenAPIResponse } from "@/api/generated/core-client";
import * as api from "@/modules/knowledge/api/knowledgeMarket";
import KnowledgeMarketTaskModal from "./KnowledgeMarketTaskModal";

vi.mock("@/modules/knowledge/api/knowledgeMarket", () => ({
  listKnowledgeMarketTaskPage: vi.fn(), listKnowledgeMarketTaskFiles: vi.fn(),
  retryKnowledgeMarketTask: vi.fn(), deleteKnowledgeMarketTask: vi.fn(), cancelKnowledgeMarketTask: vi.fn(),
}));
vi.mock("react-i18next", async () => {
  const { default: messages } = await import("@/i18n/locales/zh-CN");
  return { useTranslation: () => ({ t: (key: string, values: Record<string, unknown> = {}) => {
    const value = key.split(".").reduce<unknown>((part, name) => part && typeof part === "object" ? (part as Record<string, unknown>)[name] : undefined, messages);
    return typeof value === "string" ? value.replace(/{{(\w+)}}/g, (_, name) => String(values[name] ?? "")) : key;
  } }) };
});
const original: KnowledgeMarketTaskListItemOpenAPIResponse = {
  job_id: "original", job_type: "knowledge_market_install", job_status: "succeeded", market_item_id: "kb", name: "历史安装任务", icon: "", dataset_id: "dataset", install_state: "partial_failed", error_message: "", created_at: "2026-10-14T01:00:00Z", progress: {current: 2, total: 2}, stage: "partial_failed", overall_percent: 100, display_state: "partial_failed", can_retry: true, can_delete: true,
  parse: {state: "partial_failed", total: 23, done: 12, failed: 11, pending: 0, parsing: 0},
};
let tasks: KnowledgeMarketTaskListItemOpenAPIResponse[];
let files: KnowledgeMarketTaskFileOpenAPIResponse[];
async function click(element: HTMLElement) { await act(async () => { fireEvent.click(element); }); }
async function mount() { await act(async () => { render(<ConfigProvider button={{autoInsertSpace: false}} theme={{token: {motion: false}}}><KnowledgeMarketTaskModal open refreshKey="files" onClose={() => {}} onTasksChanged={() => {}} /></ConfigProvider>); }); }
beforeEach(() => {
  vi.useFakeTimers(); vi.clearAllMocks();
  globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
  tasks = [{...original}];
  files = Array.from({length: 23}, (_, i) => ({file_id: `file-${i}`, name: `文档${i}.md`, status: i < 11 ? "failed" : "success", reason_code: i < 11 ? "parse_failed" : "success"}));
  vi.mocked(api.listKnowledgeMarketTaskPage).mockImplementation(async (page) => ({items: tasks.slice((page-1)*10,page*10), total: tasks.length, page, page_size: 10}));
  vi.mocked(api.listKnowledgeMarketTaskFiles).mockImplementation(async (_id, status, page) => {
    const matching = files.filter((file) => file.status === status);
    return {items: matching.slice((page-1)*10,page*10), total: matching.length, counts: {failed: 11, success: 12, canceled: 0, unfinished: 0}, page, page_size: 10, file_details_available: true};
  });
});
afterEach(() => { cleanup(); vi.useRealTimers(); });

describe("background task files", () => {
  it("loads only the current task page and the expanded file page; filters reset pagination", async () => {
    await mount();
    expect(api.listKnowledgeMarketTaskPage).toHaveBeenCalledTimes(1);
    expect(api.listKnowledgeMarketTaskFiles).not.toHaveBeenCalled();
    await click(screen.getByRole("button", {name: "展开任务详情 历史安装任务"}));
    expect(screen.getByRole("tab", {name: "失败 11"})).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("文档0.md")).toBeInTheDocument();
    expect(screen.queryByText("文档10.md")).toBeNull();
    await click(screen.getByRole("button", {name: "文件下一页"}));
    expect(api.listKnowledgeMarketTaskFiles).toHaveBeenLastCalledWith("original", "failed", 2, expect.objectContaining({signal: expect.anything()}));
    expect(screen.getByText("文档10.md")).toBeInTheDocument();
    await click(screen.getByRole("tab", {name: "成功 12"}));
    expect(api.listKnowledgeMarketTaskFiles).toHaveBeenLastCalledWith("original", "success", 1, expect.anything());
    expect(within(screen.getByRole("tabpanel")).queryByRole("button", {name: "重试"})).toBeNull();
    await click(screen.getByRole("tab", {name: "取消 0"}));
    expect(screen.getByText("暂无取消文件")).toBeInTheDocument();
    expect(screen.getByRole("button", {name: "文件下一页"})).toBeDisabled();
    await act(async () => { fireEvent.keyDown(screen.getByRole("tab", {name: "取消 0"}), {key: "Home"}); });
    expect(screen.getByRole("tab", {name: "成功 12"})).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tab", {name: "成功 12"})).toHaveFocus();

  });
  it("retries all failed files regardless of page or row highlight and follows the new job once", async () => {
    let finish!: (value: {job_id: string; state: string}) => void;
    vi.mocked(api.retryKnowledgeMarketTask).mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    await mount();
    await click(screen.getByRole("button", {name: /展开任务详情/}));
    await click(screen.getByRole("button", {name: "文件下一页"}));
    await click(screen.getByRole("button", {name: /文档10.md/}));
    const buttons = screen.getAllByRole("button", {name: "重试"});
    await act(async () => { fireEvent.click(buttons[1]); fireEvent.click(buttons[0]); });
    expect(api.retryKnowledgeMarketTask).toHaveBeenCalledTimes(1);
    expect(api.retryKnowledgeMarketTask).toHaveBeenCalledWith("original");
    tasks.unshift({...original, job_id: "new", name: "新重试任务", job_status: "pending", stage: "pending", display_state: "pending", can_retry: false, can_delete: false, parse: {state: "pending", total: 0, done: 0, failed: 0, pending: 0, parsing: 0}});
    await act(async () => { finish({job_id: "new", state: "pending"}); });
    expect(screen.getByRole("button", {name: "收起任务详情 新重试任务"})).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText("历史安装任务")).toBeInTheDocument();
    expect(screen.getByText("已创建重试任务，正在跟踪新任务。")).toBeInTheDocument();
  });
  it("ignores stale file responses after switching filters", async () => {
    let finish!: (value: Awaited<ReturnType<typeof api.listKnowledgeMarketTaskFiles>>) => void;
    vi.mocked(api.listKnowledgeMarketTaskFiles).mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
    await mount();
    await click(screen.getByRole("button", {name: /展开任务详情/}));
    await click(screen.getByRole("tab", {name: "成功 12"}));
    expect(screen.getByText("文档11.md")).toBeInTheDocument();
    await act(async () => { finish({items: [{file_id: "stale", name: "过期响应", status: "failed", reason_code: "parse_failed"}], total: 1, counts: {failed: 11, success: 12, canceled: 0, unfinished: 0}, page: 1, page_size: 10, file_details_available: true}); });
    expect(screen.queryByText("过期响应")).toBeNull();
    expect(screen.getByText("文档11.md")).toBeInTheDocument();
  });
  it("preserves file rows on a polling failure and offers a read-only retry", async () => {
    tasks[0] = {...original, display_state: "unknown", can_retry: false};
    await mount(); await click(screen.getByRole("button", {name: /展开任务详情/}));
    vi.mocked(api.listKnowledgeMarketTaskFiles).mockRejectedValueOnce(new Error("network"));
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(screen.getByText("文档0.md")).toBeInTheDocument();
    expect(screen.getByText("暂时无法读取文件列表，请重试。")).toBeInTheDocument();
    expect(screen.getByRole("button", {name: "重试"})).toBeDisabled();
    expect(api.retryKnowledgeMarketTask).not.toHaveBeenCalled();
  });
  it("clamps page jumps and keeps at most one task expanded", async () => {
    tasks.push({...original, job_id: "second", name: "另一个任务"});
    await mount(); await click(screen.getByRole("button", {name: "展开任务详情 历史安装任务"}));
    const input = screen.getByRole("spinbutton", {name: "跳转至文件页码"});
    await act(async () => { fireEvent.change(input, {target: {value: "999"}}); fireEvent.blur(input); });
    expect(input).toHaveValue(2);
    await click(screen.getByRole("button", {name: "展开任务详情 另一个任务"}));
    expect(screen.getAllByRole("tabpanel")).toHaveLength(1);
    expect(screen.getByRole("button", {name: "展开任务详情 历史安装任务"})).toHaveAttribute("aria-expanded", "false");
    expect(screen.getByRole("spinbutton", {name: "跳转至文件页码"})).toHaveValue(1);
  });
  it("shows unavailable historical details without inventing file rows", async () => {
    vi.mocked(api.listKnowledgeMarketTaskFiles).mockResolvedValue({items: [], total: 11, counts: {failed: 11, success: 12, canceled: 0, unfinished: 0}, page: 1, page_size: 10, file_details_available: false});
    await mount(); await click(screen.getByRole("button", {name: /展开任务详情/}));
    expect(screen.getByText("历史文件明细不可用，已保留原任务统计。")).toBeInTheDocument();
    expect(screen.getByRole("tab", {name: "失败 11"})).toBeInTheDocument();
    expect(screen.queryByText("文档0.md")).toBeNull();
  });
});
